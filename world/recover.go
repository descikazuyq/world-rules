package world

// RecoverPreview 描述预览恢复的结果。
type RecoverPreview struct {
	// Current 是槽指针当前指向的记录标识；即使该记录已损坏或被删除，
	// 它仍是槽指针所指的当前记录。
	Current RecordID
	// Source 是按保存生效次序选中的来源记录：最近一份校验通过且版本
	// 可接受的本槽已保存记录。
	Source RecordID
	// State 是来源记录保存时的完整世界数据（深拷贝，修改不影响存档）。
	State State
}

// PreviewRecover 预览恢复：找出调用方将能恢复的记录，但不改变存档。
//
// 遍历次序与 [Archive.RecoverLatest] 完全一致，来自槽指针维护的历史索引
// （最新在前），跳过缺失、截断、校验失败的记录以及版本不被接受的记录；
// 分支只在分支自身的历史中查找，不读取受损记录里的父标识或槽首标记，
// 也不会被引向别的槽。当前记录本来就可读且版本可接受时，预览可以选它。
//
// 返回槽当前指向的记录标识、选中的来源记录标识及其完整世界状态。
// 预览不会改写槽、指针、历史记录或规则，重复预览也不消耗历史。
//
// 槽不存在返回 *NotFoundError；槽指针不可读返回 *CorruptError；槽中没有
// 任何版本可接受的完好记录时返回包装了 ErrUnrecoverable 的
// *UnrecoverableError，可用 errors.Is(err, ErrUnrecoverable) 判断。
func (a *Archive) PreviewRecover(slot string, acceptedVersions []string) (RecoverPreview, error) {
	if !validSlotName(slot) {
		return RecoverPreview{}, &NotFoundError{Slot: slot}
	}
	lock, err := acquireSharedLock(a.dir)
	if err != nil {
		return RecoverPreview{}, err
	}
	defer lock.release()

	pointer, err := a.readSlotPointerLocked(slot)
	if err != nil {
		return RecoverPreview{}, err
	}

	history, err := a.slotHistoryLocked(slot)
	if err != nil {
		return RecoverPreview{}, err
	}

	var lastReason string
	for _, id := range history {
		env, verifyErr := a.loadAndVerifyLocked(id)
		if verifyErr != nil {
			// 文件缺失、无法解析、校验和不符、记录标识不符、父记录关系
			// 异常或世界状态不自洽：受损记录不能成为恢复结果，越过它
			// 继续看更早的记录（不读其中的父标识或槽首标记）。
			lastReason = verifyErr.Error()
			continue
		}
		if !versionAccepted(env.State.Rules.Version, acceptedVersions) {
			// 版本不被接受：跳过，不自动升级。
			lastReason = (&VersionRejectedError{
				Record:  id,
				Version: env.State.Rules.Version,
			}).Error()
			continue
		}
		return RecoverPreview{
			Current: pointer.Latest,
			Source:  id,
			State:   cloneState(env.State),
		}, nil
	}

	if lastReason == "" {
		lastReason = "槽中没有任何可确认归属的历史记录"
	}
	return RecoverPreview{}, &UnrecoverableError{Slot: slot, Reason: lastReason}
}

// ConfirmRecover 确认恢复：把预览选中的来源记录正式保存回原槽并继续使用。
//
// current 必须是调用方预览时槽指针所指的记录标识，source 是预览选中的
// 来源记录标识，acceptedVersions 是调用方给出的可接受版本集合。
//
// 任一标识为空都返回现有冲突错误。提交在排他锁下重新判断：
//
//   - 槽指针必须仍指向 current，否则返回现有冲突错误。该比较针对槽所指
//     记录本身，即使它已损坏或被删除，也不影响从本槽完好旧记录恢复；
//   - source 必须仍属于该槽历史（其他槽的记录、未生效的残留记录一律按
//     记录不存在拒绝），仍能通过内容与父关系校验，且版本可接受。
//
// source 后来被删除、损坏或版本不被接受时，分别返回已有的不存在、损坏
// 或版本拒绝错误，不改选其他来源，也不使用调用方修改过的预览状态。
//
// 成功后槽指向新记录，父记录是选中的来源，种子、完整规则、时间片、角色
// 及物品的内容和排列均原样复制（允许时间片回到来源时刻）。旧历史按原保存
// 次序保留，新记录排在最前；来源之后仍完好的旧记录可照常读取或分支，
// 已存在的分支不受影响。并发确认、确认与普通覆盖或规则升级竞争时，基于
// 同一当前标识最多一个成功，其余收到冲突。
//
// 写入中断后重开，槽只能保持确认前的指向和历史，或同时看到新记录与更新
// 后的历史；尚未生效的新记录不能进入恢复候选。旧格式存档沿用已有历史
// 归属限制。
func (a *Archive) ConfirmRecover(slot string, current, source RecordID, acceptedVersions []string) (RecordInfo, error) {
	if !validSlotName(slot) {
		return RecordInfo{}, &ConflictError{Slot: slot, Reason: "非法存档槽名"}
	}
	if current == "" || source == "" {
		return RecordInfo{}, &ConflictError{Slot: slot, Reason: "恢复必须提供当前记录标识与来源记录标识"}
	}

	lock, err := acquireLock(a.dir)
	if err != nil {
		return RecordInfo{}, err
	}
	defer lock.release()

	pointer, err := a.readSlotPointerLocked(slot)
	if err != nil {
		return RecordInfo{}, err
	}
	// CAS：槽指针必须仍指向调用方预览时的当前记录。即使该记录已损坏
	// 或被删除，也不影响从本槽完好旧记录恢复。
	if pointer.Latest != current {
		return RecordInfo{}, &ConflictError{
			Slot:   slot,
			Reason: "所依据的记录已不是该槽最新记录",
		}
	}

	// 来源必须仍属于本槽历史：其他槽的记录、未生效的残留记录都按记录
	// 不存在拒绝。
	if err := a.assertInSlotHistoryLocked(slot, source); err != nil {
		return RecordInfo{}, err
	}
	sourceEnv, err := a.loadAndVerifyLocked(source)
	if err != nil {
		if ce, ok := err.(*CorruptError); ok {
			ce.Slot = slot
		}
		return RecordInfo{}, err
	}
	if !versionAccepted(sourceEnv.State.Rules.Version, acceptedVersions) {
		return RecordInfo{}, &VersionRejectedError{
			Slot:     slot,
			Record:   source,
			Version:  sourceEnv.State.Rules.Version,
			Accepted: append([]string(nil), acceptedVersions...),
		}
	}

	id, err := newRecordID()
	if err != nil {
		return RecordInfo{}, err
	}
	env := &envelope{
		Format:    archiveFormatVersion,
		ID:        id,
		Parent:    source,
		SlotFirst: false,
		// 完整复制来源记录的世界数据：种子、规则、时间片、角色及物品
		// 的内容和排列原样保留，允许时间片回到来源时刻。
		State: cloneState(sourceEnv.State),
	}
	env.Checksum = computeChecksum(env)
	if err := writeRecord(a.dir, env); err != nil {
		return RecordInfo{}, err
	}
	if err := a.commitSlotPointerLocked(slot, pointer, current, id); err != nil {
		return RecordInfo{}, err
	}
	return RecordInfo{
		ID:      id,
		Parent:  source,
		Version: sourceEnv.State.Rules.Version,
	}, nil
}
