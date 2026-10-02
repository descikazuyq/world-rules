package world

import (
	"fmt"
	"math"
)

// NameRename 表示一条旧名称到新名称的显式对应关系。
type NameRename struct {
	// From 是来源记录旧规则中使用的名称（地点或物品种类）。
	From string
	// To 是目标规则中使用的名称。
	To string
}

// NameMapping 是一次显式迁移使用的名称对应关系：地点改名与物品改名。
//
// 对应关系按切片给出的次序解释与报错。未出现在对应关系中的名称保持原样；
// 每条对应关系只作用于来源状态中的原始名称一次，不会沿对应关系连续替换，
// 因此互换名称（A->B、B->A）也有确定结果。
type NameMapping struct {
	// Locations 是旧地点到新地点的对应关系。
	Locations []NameRename
	// Items 是旧物品种类到新物品种类的对应关系。
	Items []NameRename
}

// MigrationPreview 是一次显式迁移预览的结果。
type MigrationPreview struct {
	// RecordID 是所依据的来源记录标识（槽当前最新记录）。
	RecordID RecordID
	// OldVersion 是来源记录的规则版本。
	OldVersion string
	// TargetVersion 是目标规则版本。
	TargetVersion string
	// State 是按目标规则与对应关系转换后的完整世界状态（深拷贝，
	// 修改不影响存档，也不会被正式迁移采信）。
	State State
	// Committable 表示转换后状态是否可提交：没有缺失地点、不被允许的
	// 物品或超过目标携带上限等阻碍。
	Committable bool
	// Blockers 是转换后仍存在的全部阻碍，排序规则与升级检查一致，
	// 重复预览输出顺序一致。
	Blockers []Blocker
}

// MigrationResult 是正式迁移的返回结果。
type MigrationResult struct {
	// Preview 是对当前来源记录重新读取并计算得到的预览结果。
	Preview MigrationPreview
	// Record 是成功时新记录的元信息；被拒绝时为零值。
	Record RecordInfo
}

// validateNameMapping 校验对应关系本身：来源名称必须存在于旧规则、
// 目标名称必须存在于新规则；即使暂时没有角色使用也要检查。空名称必然
// 通不过存在性检查；同一类别内来源重复也按规则错误拒绝。
func validateNameMapping(m NameMapping, old, target Rules) error {
	oldLoc := make(map[string]struct{}, len(old.Locations))
	for _, l := range old.Locations {
		oldLoc[l] = struct{}{}
	}
	newLoc := make(map[string]struct{}, len(target.Locations))
	for _, l := range target.Locations {
		newLoc[l] = struct{}{}
	}
	oldKind := make(map[string]struct{}, len(old.ItemKinds))
	for _, k := range old.ItemKinds {
		oldKind[k] = struct{}{}
	}
	newKind := make(map[string]struct{}, len(target.ItemKinds))
	for _, k := range target.ItemKinds {
		newKind[k] = struct{}{}
	}
	seenLoc := make(map[string]struct{}, len(m.Locations))
	for i, r := range m.Locations {
		if _, ok := oldLoc[r.From]; !ok {
			return ruleErrorf("地点对应关系 %d 的来源 %q 不存在于旧规则", i, r.From)
		}
		if _, ok := newLoc[r.To]; !ok {
			return ruleErrorf("地点对应关系 %d 的目标 %q 不存在于新规则", i, r.To)
		}
		if _, dup := seenLoc[r.From]; dup {
			return ruleErrorf("地点对应关系的来源重复: %q", r.From)
		}
		seenLoc[r.From] = struct{}{}
	}
	seenKind := make(map[string]struct{}, len(m.Items))
	for i, r := range m.Items {
		if _, ok := oldKind[r.From]; !ok {
			return ruleErrorf("物品对应关系 %d 的来源 %q 不存在于旧规则", i, r.From)
		}
		if _, ok := newKind[r.To]; !ok {
			return ruleErrorf("物品对应关系 %d 的目标 %q 不存在于新规则", i, r.To)
		}
		if _, dup := seenKind[r.From]; dup {
			return ruleErrorf("物品对应关系的来源重复: %q", r.From)
		}
		seenKind[r.From] = struct{}{}
	}
	return nil
}

// addSafe 在整数类型范围内求和，超出范围时返回错误而不是回绕。
func addSafe(a, b int) (int, error) {
	if b > 0 && a > math.MaxInt-b {
		return 0, ruleErrorf("物品数量合并后超出整数范围: %d + %d", a, b)
	}
	return a + b, nil
}

// migrateState 按目标规则与对应关系转换一份完整状态。
//
// 种子、时间片、角色标识与角色排列保持不变；地点转换不要求符合旧道路。
// 角色所在地点与物品条目种类只按原始名称替换一次，不沿对应关系连续替换。
// 多个物品条目转换成同一种类时合并数量，条目位置取原列表中首次出现者，
// 数量为零的条目仍保留。合并数量或角色携带总量超出整数范围时返回规则
// 错误，不回绕也不丢弃物品。返回的状态是新建副本，入参不会被修改。
func migrateState(st State, target Rules, m NameMapping) (State, error) {
	locRename := make(map[string]string, len(m.Locations))
	for _, r := range m.Locations {
		locRename[r.From] = r.To
	}
	itemRename := make(map[string]string, len(m.Items))
	for _, r := range m.Items {
		itemRename[r.From] = r.To
	}

	next := State{
		Seed:       st.Seed,
		Rules:      cloneRules(target),
		Time:       st.Time,
		Characters: make([]Character, len(st.Characters)),
	}
	for ci, ch := range st.Characters {
		nc := Character{ID: ch.ID, Location: ch.Location}
		if to, ok := locRename[ch.Location]; ok {
			nc.Location = to
		}
		// 按原列表次序合并到同一目标种类：首次出现的位置保留一个条目。
		items := make([]CharacterItem, 0, len(ch.Items))
		pos := make(map[string]int, len(ch.Items))
		carryTotal := 0
		for _, it := range ch.Items {
			name := it.Item
			if to, ok := itemRename[it.Item]; ok {
				name = to
			}
			if idx, ok := pos[name]; ok {
				sum, err := addSafe(items[idx].Count, it.Count)
				if err != nil {
					return State{}, ruleErrorf("角色 %q 的物品 %q 合并数量超出整数范围（不回绕、不丢弃）",
						ch.ID, name)
				}
				items[idx].Count = sum
			} else {
				pos[name] = len(items)
				items = append(items, CharacterItem{Item: name, Count: it.Count})
			}
			total, err := addSafe(carryTotal, it.Count)
			if err != nil {
				return State{}, ruleErrorf("角色 %q 携带总量超出整数范围", ch.ID)
			}
			carryTotal = total
		}
		nc.Items = items
		next.Characters[ci] = nc
	}
	return next, nil
}

// previewMigrationLocked 在调用方已持锁（共享或排他均可）的前提下读取并
// 校验槽当前最新记录，校验目标规则与对应关系，计算转换后状态与阻碍。
func (a *Archive) previewMigrationLocked(slot string, acceptedVersions []string, target Rules, mapping NameMapping) (MigrationPreview, error) {
	latest, err := a.readLatestLocked(slot)
	if err != nil {
		return MigrationPreview{}, err
	}
	env, err := a.loadAndVerifyLocked(latest)
	if err != nil {
		if ce, ok := err.(*CorruptError); ok {
			ce.Slot = slot
		}
		return MigrationPreview{}, err
	}
	if !versionAccepted(env.State.Rules.Version, acceptedVersions) {
		return MigrationPreview{}, &VersionRejectedError{
			Slot:     slot,
			Record:   latest,
			Version:  env.State.Rules.Version,
			Accepted: append([]string(nil), acceptedVersions...),
		}
	}
	if target.Version == env.State.Rules.Version {
		return MigrationPreview{}, &RuleError{Reason: "目标规则版本必须与来源记录版本不同"}
	}
	if err := validateNameMapping(mapping, env.State.Rules, target); err != nil {
		return MigrationPreview{}, err
	}
	converted, err := migrateState(env.State, target, mapping)
	if err != nil {
		return MigrationPreview{}, err
	}
	blockers := checkCompatibility(converted, target)
	// 返回深拷贝，调用方修改不影响任何后续计算。
	converted = cloneState(converted)
	return MigrationPreview{
		RecordID:      latest,
		OldVersion:    env.State.Rules.Version,
		TargetVersion: target.Version,
		State:         converted,
		Committable:   len(blockers) == 0,
		Blockers:      blockers,
	}, nil
}

// PreviewMigration 预览把槽当前最新记录显式转换到目标规则后的状态。
//
// 调用方提供可接受的旧规则版本集合、合法且版本与来源不同的目标规则，以及
// 旧地点到新地点、旧物品到新物品的对应关系。每条对应关系的来源必须存在于
// 旧规则、目标必须存在于新规则（即使暂时没有角色使用也要检查）；空目标或
// 未知名称返回 *RuleError。未指定的名称保持原样；对应关系只作用于原始名称
// 一次，不连续替换，互换名称也有确定结果。角色按对应关系改变所在地点（不
// 要求符合旧道路），物品改变种类但不增减数量；多个条目转换成同一种类时
// 合并数量并按原列表首次出现的位置保留一个条目，零数量仍保留。种子、时间
// 片、角色标识及角色排列保持不变。合并数量或携带总量超出整数范围时返回
// *RuleError，不回绕或丢弃物品。
//
// 返回来源记录标识、旧新版本、转换后的完整状态（深拷贝）及是否可提交；
// 转换后仍有缺失地点、不被允许的物品或超过目标携带上限时，一次列出全部
// 阻碍，注明角色与相关名称或数量，重复预览顺序一致。预览不改动存档，
// 修改返回状态也不影响来源记录。来源缺失、损坏或版本不被接受时沿用已有
// 的对应错误，不自动挑选历史记录。
func (a *Archive) PreviewMigration(slot string, acceptedVersions []string, target Rules, mapping NameMapping) (MigrationPreview, error) {
	if !validSlotName(slot) {
		return MigrationPreview{}, &NotFoundError{Slot: slot}
	}
	if err := validateRules(target); err != nil {
		return MigrationPreview{}, err
	}
	lock, err := acquireSharedLock(a.dir)
	if err != nil {
		return MigrationPreview{}, err
	}
	defer lock.release()
	preview, err := a.previewMigrationLocked(slot, acceptedVersions, target, mapping)
	if err != nil {
		return MigrationPreview{}, err
	}
	return preview, nil
}

// Migrate 在预览可提交后把槽当前最新记录显式转换为目标规则并保存。
//
// 必须带预览返回的来源记录标识 expected，并重新提交目标规则与对应关系。
// 迁移在排他锁下重新读取来源记录并重新计算，绝不直接保存调用方可能改过
// 的预览状态。expected 为空或槽已更新（覆盖、升级、确认恢复或另一次迁移
// 抢先成功）时返回 *ConflictError；仍有阻碍时返回阻碍信息并拒绝保存。
// 任何拒绝都不改变当前记录或历史。
//
// 成功后产生一条以来源为父的独立新记录，保存目标规则与转换后的状态，
// 并加入本槽保存次序最前；旧记录及已分出的分支保留原状态，只接受旧版本
// 的恢复读取仍能找到迁移前记录。迁移后的记录可用 WorldFromState 继续按
// 新规则移动、改变物品并保存，普通读取不自动转换。写入中断后重开，只能
// 看到迁移前或迁移后的完整记录与历史。
func (a *Archive) Migrate(slot string, acceptedVersions []string, target Rules, mapping NameMapping, expected RecordID) (MigrationResult, error) {
	if !validSlotName(slot) {
		return MigrationResult{}, &NotFoundError{Slot: slot}
	}
	if err := validateRules(target); err != nil {
		return MigrationResult{}, err
	}
	if expected == "" {
		return MigrationResult{}, &ConflictError{Slot: slot, Reason: "迁移必须提供所依据的记录标识"}
	}
	lock, err := acquireLock(a.dir)
	if err != nil {
		return MigrationResult{}, err
	}
	defer lock.release()

	oldPointer, err := a.readSlotPointerLocked(slot)
	if err != nil {
		return MigrationResult{}, err
	}
	if oldPointer.Latest != expected {
		return MigrationResult{}, &ConflictError{
			Slot:   slot,
			Reason: "所依据的记录已不是该槽最新记录",
		}
	}

	// 重新读取来源并重新计算，不采信调用方带回的预览状态。
	preview, err := a.previewMigrationLocked(slot, acceptedVersions, target, mapping)
	if err != nil {
		return MigrationResult{}, err
	}
	if !preview.Committable {
		return MigrationResult{Preview: preview}, &RuleError{
			Reason: fmt.Sprintf("迁移被阻碍: 转换后的状态仍不满足目标规则（%d 处阻碍）", len(preview.Blockers)),
		}
	}

	id, err := newRecordID()
	if err != nil {
		return MigrationResult{}, err
	}
	newEnv := &envelope{
		Format:    archiveFormatVersion,
		ID:        id,
		Parent:    expected,
		SlotFirst: false,
		// 用重新读取的来源状态重新转换，绝不使用调用方持有的预览副本。
		State: cloneState(preview.State),
	}
	newEnv.Checksum = computeChecksum(newEnv)
	if err := writeRecord(a.dir, newEnv); err != nil {
		return MigrationResult{}, err
	}
	if err := a.commitSlotPointerLocked(slot, oldPointer, expected, id); err != nil {
		return MigrationResult{}, err
	}
	info := RecordInfo{
		ID:      id,
		Parent:  expected,
		Version: target.Version,
	}
	return MigrationResult{Preview: preview, Record: info}, nil
}
