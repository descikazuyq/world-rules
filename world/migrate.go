package world

import (
	"fmt"
	"math"
)

// NameMapping 描述旧规则中一个名称到新规则名称的显式转换关系。
//
// From 必须是来源记录旧规则中存在的地点或物品种类，To 必须是目标规则中
// 存在的对应名称；空名称、来源不存在于旧规则或目标不存在于新规则都在
// 预览时返回规则错误。
type NameMapping struct {
	// From 是旧规则中的地点或物品种类名称。
	From string
	// To 是新规则中的对应名称。
	To string
}

// MigrationPreview 是把槽当前最新记录按对应关系显式迁移到目标规则的
// 预览结果。
//
// 预览只读：返回所依据的来源记录标识、旧新版本、按对应关系转换后的完整
// 世界状态（深拷贝）以及是否可提交。它不改变槽指向、历史或任何记录文件；
// 修改返回的状态也不影响存档。
type MigrationPreview struct {
	// RecordID 是所依据的来源记录标识（槽当前最新记录）。
	RecordID RecordID
	// OldVersion 是来源记录的规则版本。
	OldVersion string
	// TargetVersion 是目标规则版本。
	TargetVersion string
	// State 是转换后的完整世界状态（深拷贝），规则已替换为目标规则。
	// 正式迁移以排他锁下重新读取和计算的结果为准，不使用调用方可能改过
	// 的本状态。
	State State
	// Committable 表示转换后的状态能否在目标规则下提交（无阻碍）。
	Committable bool
	// Blockers 是全部阻碍，排序与升级检查一致；重复预览输出顺序一致。
	Blockers []Blocker
}

// MigrationResult 是正式迁移的返回结果。
type MigrationResult struct {
	// Preview 是对来源记录与目标规则的预览结果（排他锁下重新读取并计算）。
	Preview MigrationPreview
	// Record 是成功时新记录的元信息；被阻碍时为零值。
	Record RecordInfo
}

// validateMappings 校验转换对应关系：来源必须存在于旧规则，目标必须存在
// 于新规则，名称非空，同一来源不重复指定。即使没有角色使用这些名称也要
// 检查。
func validateMappings(old, target Rules, locations, items []NameMapping) error {
	oldLocs := make(map[string]struct{}, len(old.Locations))
	for _, l := range old.Locations {
		oldLocs[l] = struct{}{}
	}
	newLocs := make(map[string]struct{}, len(target.Locations))
	for _, l := range target.Locations {
		newLocs[l] = struct{}{}
	}
	if err := validateMappingSet("地点", locations, oldLocs, newLocs); err != nil {
		return err
	}

	oldKinds := make(map[string]struct{}, len(old.ItemKinds))
	for _, k := range old.ItemKinds {
		oldKinds[k] = struct{}{}
	}
	newKinds := make(map[string]struct{}, len(target.ItemKinds))
	for _, k := range target.ItemKinds {
		newKinds[k] = struct{}{}
	}
	return validateMappingSet("物品种类", items, oldKinds, newKinds)
}

func validateMappingSet(kind string, mappings []NameMapping, oldNames, newNames map[string]struct{}) error {
	seen := make(map[string]struct{}, len(mappings))
	for i, m := range mappings {
		if m.From == "" {
			return ruleErrorf("%s转换关系 %d 的来源名称为空", kind, i)
		}
		if m.To == "" {
			return ruleErrorf("%s转换关系 %d 的目标名称为空", kind, i)
		}
		if _, ok := oldNames[m.From]; !ok {
			return ruleErrorf("%s转换关系的来源 %q 不存在于旧规则", kind, m.From)
		}
		if _, ok := newNames[m.To]; !ok {
			return ruleErrorf("%s转换关系的目标 %q 不存在于新规则", kind, m.To)
		}
		if _, dup := seen[m.From]; dup {
			return ruleErrorf("%s转换关系的来源 %q 重复指定", kind, m.From)
		}
		seen[m.From] = struct{}{}
	}
	return nil
}

// applyMigration 按对应关系转换状态：角色按对应关系改变所在地点（无需
// 符合旧道路），物品改变种类但不增减数量；多个条目转换成同一种物品时
// 合并数量，按原列表中首次出现的位置保留一个条目。转换只作用于原始名称
// 一次，不沿对应关系连续替换，因此互换名称也有确定结果。未指定的名称
// 保持原样。种子、时间片、角色标识及角色排列保持不变。
//
// 合并数量或携带总量超出 int 范围时返回规则错误，不回绕也不丢弃物品。
func applyMigration(st State, locations, items []NameMapping) (State, error) {
	locLookup := make(map[string]string, len(locations))
	for _, m := range locations {
		locLookup[m.From] = m.To
	}
	itemLookup := make(map[string]string, len(items))
	for _, m := range items {
		itemLookup[m.From] = m.To
	}

	out := cloneState(st)

	for i := range out.Characters {
		if to, ok := locLookup[out.Characters[i].Location]; ok {
			out.Characters[i].Location = to
		}
	}

	for i := range out.Characters {
		ch := &out.Characters[i]
		merged := make([]CharacterItem, 0, len(ch.Items))
		indexByName := make(map[string]int, len(ch.Items))
		for _, it := range ch.Items {
			name := it.Item
			if to, ok := itemLookup[name]; ok {
				name = to
			}
			if idx, ok := indexByName[name]; ok {
				sum, ok := addInt(merged[idx].Count, it.Count)
				if !ok {
					return State{}, ruleErrorf("角色 %q 的物品 %q 合并数量超出整数范围", ch.ID, name)
				}
				merged[idx].Count = sum
			} else {
				indexByName[name] = len(merged)
				merged = append(merged, CharacterItem{Item: name, Count: it.Count})
			}
		}
		ch.Items = merged

		// 携带总量溢出检查：各物品数量都合法，但不同物品相加可能超出
		// int 范围，同样不能回绕或丢弃。这是显式迁移特有的整数范围门槛，
		// 与携带上限判断不同：无论目标规则是否给该角色设置上限，真实总量
		// 超出 int 范围都拒绝，不能因为共用总量判断而把“无上限”当成允许
		// 溢出迁移。精确求和与其余入口共用 carryTotal，此处只保留自己的
		// 溢出判定。
		if carryTotal(ch.Items).Cmp(maxIntBig) > 0 {
			return State{}, ruleErrorf("角色 %q 携带总量超出整数范围", ch.ID)
		}
	}
	return out, nil
}

// addInt 做一次带溢出检查的 int 加法，返回结果与是否溢出。
func addInt(a, b int) (int, bool) {
	if b > 0 && a > math.MaxInt-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt-b {
		return 0, false
	}
	return a + b, true
}

// migrationComputeLocked 在调用方已持锁（共享或排他均可）的前提下完成升级
// 与迁移共用的转换前来源判断，再校验转换对应关系，计算转换后的状态与阻碍。
// 不写入。
func (a *Archive) migrationComputeLocked(slot string, acceptedVersions []string, target Rules, locations, items []NameMapping) (MigrationPreview, *envelope, error) {
	latest, env, err := a.readConversionSourceLocked(slot, acceptedVersions, target.Version)
	if err != nil {
		return MigrationPreview{}, nil, err
	}
	if err := validateMappings(env.State.Rules, target, locations, items); err != nil {
		return MigrationPreview{}, nil, err
	}
	transformed, err := applyMigration(env.State, locations, items)
	if err != nil {
		return MigrationPreview{}, nil, err
	}
	// 转换后的状态保存目标规则。
	transformed.Rules = cloneRules(target)
	blockers := checkCompatibility(transformed, target)
	preview := MigrationPreview{
		RecordID:      latest,
		OldVersion:    env.State.Rules.Version,
		TargetVersion: target.Version,
		State:         transformed,
		Committable:   len(blockers) == 0,
		Blockers:      blockers,
	}
	return preview, env, nil
}

// PreviewMigration 预览把槽当前最新记录按对应关系显式迁移到目标规则。
//
// 传入槽名、可接受的旧版本集合、目标规则，以及旧地点到新地点、旧物品到
// 新物品的对应关系，返回所依据的来源记录标识、旧新版本、转换后的完整
// 世界状态（深拷贝）及是否可提交。转换只作用于原始名称一次，不沿对应
// 关系连续替换；未指定的名称保持原样。转换后仍有缺失地点、不被允许的
// 物品或超过目标携带上限时，一次列出全部阻碍，重复预览输出顺序一致。
//
// 预览只读：不改变世界、历史或槽当前记录，修改返回的状态也不影响存档。
// 目标规则须满足规则自身的合法性要求且版本与来源不同；对应关系的来源
// 必须存在于旧规则、目标必须存在于新规则（即使没有角色使用也要检查），
// 空目标或未知名称返回规则错误；合并数量或携带总量超出 int 范围返回规则
// 错误。来源缺失、损坏或版本不被接受时沿用已有对应错误，不自动挑选历史
// 记录。
func (a *Archive) PreviewMigration(slot string, acceptedVersions []string, target Rules, locations, items []NameMapping) (MigrationPreview, error) {
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
	preview, _, err := a.migrationComputeLocked(slot, acceptedVersions, target, locations, items)
	if err != nil {
		return MigrationPreview{}, err
	}
	return preview, nil
}

// Migrate 在预览后正式迁移：用目标规则与转换后的状态生成一条以来源为父
// 的新记录并加入本槽保存次序。
//
// 传入与预览相同的条件，并带预览返回的来源记录标识 source。提交时在
// 排他锁下重新读取并完整校验来源，重新校验目标规则与对应关系、重新计算
// 转换后的状态与阻碍，绝不使用调用方可能改过的预览状态：
//
//   - source 为空，或槽已不再指向该来源（被并发覆盖、升级、确认恢复或
//     另一次迁移抢先提交），返回 *ConflictError。
//   - 来源缺失、损坏或版本不被接受分别返回已有的不存在、损坏或版本拒绝
//     错误，不改选其他记录。
//   - 仍有阻碍时返回阻碍信息并拒绝保存，不改变当前记录或历史。
//
// 成功后产生一条以来源为父的新记录，保存目标规则与转换后的状态，加入
// 本槽保存次序；种子、时间片、角色标识及角色排列保持原样。与覆盖、升级、
// 确认恢复或另一次迁移竞争时，同一来源最多一个成功。任何拒绝都不改变
// 当前记录或历史；中断后重开只能看到迁移前或迁移后的完整记录与历史。
// 旧记录及已有分支保留原状态，恢复读取仍能找到旧版本；迁移后的记录可
// 继续按新规则移动、改变物品并保存，普通读取不自动转换。
func (a *Archive) Migrate(slot string, acceptedVersions []string, target Rules, locations, items []NameMapping, source RecordID) (MigrationResult, error) {
	if !validSlotName(slot) {
		return MigrationResult{}, &NotFoundError{Slot: slot}
	}
	if source == "" {
		return MigrationResult{}, &ConflictError{Slot: slot, Reason: "迁移必须提供所依据的来源记录标识"}
	}
	if err := validateRules(target); err != nil {
		return MigrationResult{}, err
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
	// 乐观并发控制：槽必须仍指向预览时的来源记录。
	if oldPointer.Latest != source {
		return MigrationResult{}, &ConflictError{
			Slot:   slot,
			Reason: "所依据的来源记录已不是该槽最新记录",
		}
	}

	// 排他锁下重新读取、校验并计算，不采信调用方带回的任何状态。
	preview, _, err := a.migrationComputeLocked(slot, acceptedVersions, target, locations, items)
	if err != nil {
		return MigrationResult{}, err
	}
	if !preview.Committable {
		return MigrationResult{Preview: preview}, &RuleError{
			Reason: fmt.Sprintf("迁移被阻碍: 转换后的状态无法在目标规则下提交（%d 处阻碍）", len(preview.Blockers)),
		}
	}

	// 保存目标规则与排他锁下重新计算出的转换结果；以来源为父，沿用
	// 共同的新记录组装、落盘与槽指针提交路径。记录完整落盘后才原子
	// 更新指针：新记录前置进保存次序，旧历史原样保留。崩溃在这一步
	// 之前时槽保持迁移前的指向与历史，新记录因不在历史索引中而不会
	// 进入恢复候选。
	info, err := a.appendSlotRecordLocked(slotSave{
		slot:          slot,
		oldPointer:    oldPointer,
		parent:        source,
		historyAnchor: source,
		state:         preview.State,
	})
	if err != nil {
		return MigrationResult{}, err
	}
	return MigrationResult{Preview: preview, Record: info}, nil
}
