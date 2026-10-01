package world

// loadLatestVerifiedLocked 在调用方已持锁的前提下读取槽最新记录并完成
// 内容校验；损坏错误会带上槽名。
func (a *Archive) loadLatestVerifiedLocked(slot string) (RecordID, *envelope, error) {
	id, err := a.readLatestLocked(slot)
	if err != nil {
		return "", nil, err
	}
	env, err := a.loadAndVerifyLocked(id)
	if err != nil {
		if ce, ok := err.(*CorruptError); ok {
			ce.Slot = slot
		}
		return "", nil, err
	}
	return id, env, nil
}

// checkSourceLocked 校验目标规则与来源记录的共同前提：来源版本必须在
// 调用方可接受版本集合内，且目标版本必须与来源版本不同（版本名只按
// 相等判断，不按数字大小比较）。
func checkSourceLocked(slot string, id RecordID, sourceVersion string,
	acceptedVersions []string, target Rules) error {
	if !versionAccepted(sourceVersion, acceptedVersions) {
		return &VersionRejectedError{
			Slot:     slot,
			Record:   id,
			Version:  sourceVersion,
			Accepted: append([]string(nil), acceptedVersions...),
		}
	}
	if target.Version == sourceVersion {
		return &RuleError{Reason: "目标规则版本必须与来源记录版本不同: 均为 " +
			target.Version}
	}
	return nil
}

// CheckUpgrade 检查槽 slot 的最新存档能否在目标规则 target 下原样承接。
//
// acceptedVersions 是调用方明确给出的可接受旧版本集合：最新记录版本不
// 在集合内时返回 *VersionRejectedError；记录损坏返回 *CorruptError；槽
// 不存在返回 *NotFoundError。target 必须先满足规则自身的合法性要求，
// 否则返回 *RuleError；其版本还必须与来源记录版本不同（只按字符串相等
// 判断，不按数字大小限制“升级”方向）。
//
// 兼容只判断当前角色状态能否原样延续：每个角色所在地点仍存在、已列出
// 的物品种类仍被允许（数量为零也算）、各角色携带总量不超过目标上限
// （目标中没有该角色上限时表示不设上限）。删去无人所在的地点、不再
// 出现的物品种类或旧连通关系，以及增加内容，都不构成阻碍。存在多个
// 阻碍时一次全部返回，按角色标识、物品标识排序。
//
// 检查是只读操作：不改变世界、历史或槽当前记录；入参规则在内部持有
// 副本，调用方事后修改 target 不影响已保存数据。
func (a *Archive) CheckUpgrade(slot string, acceptedVersions []string, target Rules) (UpgradeCheck, error) {
	if !validSlotName(slot) {
		return UpgradeCheck{}, &NotFoundError{Slot: slot}
	}
	target = cloneRules(target)
	if err := validateRules(target); err != nil {
		return UpgradeCheck{}, err
	}

	lock, err := acquireSharedLock(a.dir)
	if err != nil {
		return UpgradeCheck{}, err
	}
	defer lock.release()

	id, env, err := a.loadLatestVerifiedLocked(slot)
	if err != nil {
		return UpgradeCheck{}, err
	}
	if err := checkSourceLocked(slot, id, env.State.Rules.Version, acceptedVersions, target); err != nil {
		return UpgradeCheck{}, err
	}

	blockers := findBlockers(env.State.Characters, target)
	return UpgradeCheck{
		Record:        id,
		OldVersion:    env.State.Rules.Version,
		TargetVersion: target.Version,
		Compatible:    len(blockers) == 0,
		Blockers:      blockers,
	}, nil
}

// UpgradeRules 在明确请求下把槽 slot 的最新存档升级到目标规则 target。
//
// 入参与 [Archive.CheckUpgrade] 相同，另带 expected：调用方此前读到并
// 预期仍为最新的记录标识。提交时在写入锁内重新读取并判断当前来源：
//
//   - expected 为空或已不是该槽最新记录，返回 *ConflictError，不写入；
//   - 槽不存在、来源损坏、版本不被接受、目标规则非法或版本相同，
//     返回与读取/检查相同的错误，不写入；
//   - expected 仍是最新但当前状态存在阻碍时，返回的 UpgradeCheck 与
//     CheckUpgrade 的结果一致（Compatible 为 false 且带全部有序阻碍），
//     不写入。
//
// 成功后槽增加一条完整的新记录，以被升级记录为父，保存目标规则；
// 种子、时间片、角色位置及物品的数量和排列保持原样。新记录不是槽首
// 记录，旧记录继续保留原规则；已经分出的分支不受影响。同一目录的不
// 同实例或进程基于同一记录同时升级、或升级与普通覆盖竞争时，flock 与
// 记录标识比较保证只有一个成功，其余收到冲突。
//
// 被拒绝的升级不增加历史记录，也不改变槽当前记录；入参规则在内部
// 持有副本，调用方事后修改 target 或读回的状态不影响已保存数据。
func (a *Archive) UpgradeRules(slot string, acceptedVersions []string,
	target Rules, expected RecordID) (RecordInfo, UpgradeCheck, error) {
	if !validSlotName(slot) {
		return RecordInfo{}, UpgradeCheck{}, &ConflictError{Slot: slot, Reason: "非法存档槽名"}
	}
	if expected == "" {
		return RecordInfo{}, UpgradeCheck{}, &ConflictError{
			Slot:   slot,
			Reason: "升级必须提供所依据的最新记录标识",
		}
	}
	target = cloneRules(target)
	if err := validateRules(target); err != nil {
		return RecordInfo{}, UpgradeCheck{}, err
	}

	lock, err := acquireLock(a.dir)
	if err != nil {
		return RecordInfo{}, UpgradeCheck{}, err
	}
	defer lock.release()

	latest, env, err := a.loadLatestVerifiedLocked(slot)
	if err != nil {
		return RecordInfo{}, UpgradeCheck{}, err
	}

	// 乐观锁优先：所依据的记录必须仍是槽最新记录，与 Replace 的竞争
	// 语义一致。并发升级中败方的可接受版本集合可能已不含胜者写入的
	// 新版本，此时仍按冲突报告，而不是版本拒绝；标识过期一律冲突，
	// 调用方需重新读取后再决定。
	if latest != expected {
		return RecordInfo{}, UpgradeCheck{}, &ConflictError{
			Slot:   slot,
			Reason: "所依据的记录已不是该槽最新记录",
		}
	}

	// 依据记录未变，再重新判断当前来源本身。
	if err := checkSourceLocked(slot, latest, env.State.Rules.Version, acceptedVersions, target); err != nil {
		return RecordInfo{}, UpgradeCheck{}, err
	}

	blockers := findBlockers(env.State.Characters, target)
	check := UpgradeCheck{
		Record:        latest,
		OldVersion:    env.State.Rules.Version,
		TargetVersion: target.Version,
		Compatible:    len(blockers) == 0,
		Blockers:      blockers,
	}
	if len(blockers) > 0 {
		// 拒绝写入：不产生记录文件，历史与槽指针保持原样。
		return RecordInfo{}, check, nil
	}

	id, err := newRecordID()
	if err != nil {
		return RecordInfo{}, UpgradeCheck{}, err
	}
	next := cloneState(env.State)
	// 只替换规则；种子、时间片、角色位置与物品的数量、排列原样保留。
	next.Rules = target
	upEnv := &envelope{
		Format: archiveFormatVersion,
		ID:     id,
		Parent: latest,
		State:  next,
	}
	upEnv.Checksum = computeChecksum(upEnv)
	if err := writeRecord(a.dir, upEnv); err != nil {
		return RecordInfo{}, UpgradeCheck{}, err
	}
	if err := a.replaceSlotPointer(slot, id); err != nil {
		return RecordInfo{}, UpgradeCheck{}, err
	}
	return RecordInfo{
		ID:      id,
		Parent:  latest,
		Version: target.Version,
	}, check, nil
}
