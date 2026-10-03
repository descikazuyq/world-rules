package world

import "errors"

// RecoveryPreview 是一次恢复预览的结果。
//
// 预览只读：返回槽当前指向的记录标识、按本槽保存生效次序选中的来源记录
// 标识，以及来源记录的完整世界状态（深拷贝）。它不改变槽指向、历史或
// 任何记录文件。
type RecoveryPreview struct {
	// Current 是槽指针当前指向的记录标识。它可能已损坏或被删除，
	// 但仍是之后确认恢复时必须一致的“当前标识”。
	Current RecordID
	// Source 是选中的来源记录标识：本槽保存次序最近一份校验通过且
	// 版本可接受的记录。它可能与 Current 相同（当前记录本就可读）。
	Source RecordID
	// State 是来源记录保存时的完整世界状态（深拷贝，修改不影响存档）。
	State State
}

// PreviewRecovery 预览将恢复的内容而不改写存档。
//
// 传入槽名与调用方可接受的规则版本集合，返回槽当前指向的记录标识、选中
// 的来源记录标识及其完整世界状态。来源按本槽“保存生效次序”选择最近一份
// 内容与父关系校验通过且版本可接受的记录，越过缺失、截断、校验失败与
// 版本不被接受的记录；分支只在分支自身的历史中选择，不跨入来源槽。当前
// 记录本就完好且版本可接受时可以选中它本身。
//
// 槽不存在返回 *NotFoundError；槽指针无法解析返回 *CorruptError（即使其
// 指向的记录已损坏或被删除，也仍会返回该当前标识用于预览）；槽中没有
// 任何可用记录时返回包装了 ErrUnrecoverable 的 *UnrecoverableError。
// 预览绝不写入、修补或删除任何数据，可重复调用。
func (a *Archive) PreviewRecovery(slot string, acceptedVersions []string) (RecoveryPreview, error) {
	if !validSlotName(slot) {
		return RecoveryPreview{}, &NotFoundError{Slot: slot}
	}
	lock, err := acquireSharedLock(a.dir)
	if err != nil {
		return RecoveryPreview{}, err
	}
	defer lock.release()
	return a.previewRecoveryLocked(slot, acceptedVersions)
}

// previewRecoveryLocked 在调用方已持锁（共享或排他均可）的前提下执行预览。
func (a *Archive) previewRecoveryLocked(slot string, acceptedVersions []string) (RecoveryPreview, error) {
	pointer, err := a.readSlotPointerLocked(slot)
	if err != nil {
		return RecoveryPreview{}, err
	}
	// slotHistoryLocked 以指针当前指向为链头，并负责旧格式裸指针沿可信
	// 父链的重建；当前记录损坏或缺失只会让重建停在那里，不妨碍指针内嵌
	// 历史索引中更早的本槽记录成为候选。
	history, err := a.slotHistoryLocked(slot)
	if err != nil {
		return RecoveryPreview{}, err
	}
	env, err := a.firstRecoverableLocked(history, acceptedVersions)
	if err != nil {
		var ue *UnrecoverableError
		if errors.As(err, &ue) {
			ue.Slot = slot
		}
		return RecoveryPreview{}, err
	}
	return RecoveryPreview{
		Current: pointer.Latest,
		Source:  env.ID,
		State:   cloneState(env.State),
	}, nil
}

// ConfirmRecovery 把预览选中的来源记录正式保存回本槽并继续使用。
//
// 入参为槽名、预览返回的当前记录标识 current 与来源记录标识 source，以及
// 调用方可接受的规则版本集合。确认在排他锁下重新判断全部前提，绝不使用
// 调用方可能修改过的预览状态，而是重新读取并校验来源记录：
//
//   - current 或 source 为空，或槽已不再指向 current（被并发覆盖、升级
//     或另一次恢复抢先提交），返回 *ConflictError。比较只针对槽所指记录，
//     即使它已损坏或被删除也不例外，因此损坏/删除的当前标识不会妨碍从
//     本槽完好旧记录恢复。
//   - 来源必须仍属于该槽、仍能通过内容与父关系校验且版本可接受。其他槽
//     的记录、未生效的残留/孤儿记录按 *NotFoundError 拒绝；来源后来被
//     删除返回 *NotFoundError，损坏（截断、校验失败、父关系断裂）返回
//     *CorruptError，版本不再被接受返回 *VersionRejectedError。来源失效
//     时不改选其他记录。
//
// 成功后产生一条独立的新记录（即使来源就是当前记录），槽指向新记录，
// 新记录以来源为父，种子、完整规则、时间片、角色及物品的内容和排列原样
// 复制，允许时间片回到来源时刻。旧历史按原保存次序保留，新记录排在最
// 前；来源之后仍完好的旧记录可照常读取或分支，已存在的分支不受影响。
// 同一 current 上的并发确认、以及确认与普通覆盖或规则升级竞争时，最多
// 一个成功。
func (a *Archive) ConfirmRecovery(slot string, current, source RecordID, acceptedVersions []string) (RecordInfo, error) {
	if !validSlotName(slot) {
		return RecordInfo{}, &NotFoundError{Slot: slot}
	}
	if current == "" || source == "" {
		return RecordInfo{}, &ConflictError{Slot: slot, Reason: "确认恢复必须提供当前记录标识与来源记录标识"}
	}

	lock, err := acquireLock(a.dir)
	if err != nil {
		return RecordInfo{}, err
	}
	defer lock.release()

	oldPointer, err := a.readSlotPointerLocked(slot)
	if err != nil {
		return RecordInfo{}, err
	}
	// 乐观并发控制：只有槽仍指向预览时的当前记录才允许确认。当前记录
	// 本身可能已损坏或被删除——那正是需要恢复的情形——故这里只比较标识，
	// 不读取它。
	if oldPointer.Latest != current {
		return RecordInfo{}, &ConflictError{
			Slot:   slot,
			Reason: "槽当前记录已改变，请重新预览后再确认",
		}
	}

	// 来源必须仍在本槽保存次序内：其他槽记录与未生效的孤儿/残留记录
	// 都不在其中，按记录不存在拒绝。
	history, err := a.slotHistoryLocked(slot)
	if err != nil {
		return RecordInfo{}, err
	}
	if err := assertRecordInOrder(history, source); err != nil {
		return RecordInfo{}, &NotFoundError{Slot: slot, Record: source}
	}

	// 重新读取并完整校验来源，不采信调用方带回的任何状态。
	env, err := a.loadAndVerifyLocked(source)
	if err != nil {
		if ce, ok := err.(*CorruptError); ok {
			ce.Slot = slot
		}
		return RecordInfo{}, err
	}
	if !versionAccepted(env.State.Rules.Version, acceptedVersions) {
		return RecordInfo{}, &VersionRejectedError{
			Slot:     slot,
			Record:   source,
			Version:  env.State.Rules.Version,
			Accepted: append([]string(nil), acceptedVersions...),
		}
	}

	info, err := a.saveSlotRecordLocked(slot, oldPointer, savedRecord{
		// 新记录以选中的来源为父；来源是槽首记录时也不沿用其槽首标记，
		// 新记录只是本槽保存次序最前的一条普通记录。
		parent: source,
		// 种子、完整规则、时间片、角色及物品的内容和排列原样复制，
		// 允许时间片回到来源时刻。
		state: cloneState(env.State),
	})
	if err != nil {
		return RecordInfo{}, err
	}
	// 记录完整落盘后才原子更新指针：新记录前置进保存次序，旧历史原样
	// 保留。崩溃在这一步之前，槽保持确认前的指向与历史，新记录因不在
	// 历史索引中而不会进入恢复候选（指针提交由共享保存路径完成）。
	return info, nil
}

// assertRecordInOrder 确认 id 出现在按保存生效次序排列的标识序列中。
func assertRecordInOrder(order []RecordID, id RecordID) error {
	for _, cid := range order {
		if cid == id {
			return nil
		}
	}
	return &NotFoundError{Record: id}
}
