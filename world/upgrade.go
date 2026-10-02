package world

import (
	"fmt"
	"sort"
)

// BlockerKind 表示升级阻碍的类型。
type BlockerKind string

const (
	// BlockerLocation 表示角色所在地点在目标规则中已不存在。
	BlockerLocation BlockerKind = "location"
	// BlockerItem 表示角色携带的某种物品在目标规则中已不被允许。
	BlockerItem BlockerKind = "item"
	// BlockerLimit 表示角色携带总量超过目标规则给出的上限。
	BlockerLimit BlockerKind = "limit"
)

// Blocker 描述一条“目标规则无法原样承接当前角色状态”的具体阻碍。
type Blocker struct {
	// Character 是受阻碍的角色标识。
	Character string
	// Kind 是阻碍类型。
	Kind BlockerKind
	// Location 是缺失的地点（Kind 为 BlockerLocation 时有值）。
	Location string
	// Item 是被移除的物品种类（Kind 为 BlockerItem 时有值）。
	Item string
	// Total 是角色当前携带总量（Kind 为 BlockerLimit 时有值）。
	Total int
	// Limit 是目标规则给出的新上限（Kind 为 BlockerLimit 时有值）。
	Limit int
}

// UpgradeCheck 描述对槽当前最新记录与目标规则的兼容性检查结果。
type UpgradeCheck struct {
	// RecordID 是所检查的来源记录标识（槽当前最新记录）。
	RecordID RecordID
	// OldVersion 是来源记录的规则版本。
	OldVersion string
	// TargetVersion 是目标规则版本。
	TargetVersion string
	// Compatible 表示目标规则能否原样承接当前角色状态。
	Compatible bool
	// Blockers 是全部阻碍，按角色标识、物品标识排序；重复检查输出顺序一致。
	Blockers []Blocker
}

// UpgradeResult 是升级的返回结果。
type UpgradeResult struct {
	// Check 是对当前来源记录与目标规则的兼容性检查结果。
	Check UpgradeCheck
	// Record 是成功时新记录的元信息；被阻碍时为零值。
	Record RecordInfo
}

// checkCompatibility 计算用 target 规则原样承接 st 中全部角色状态的阻碍。
//
// 兼容只判断能否原样承接当前角色状态：每个角色所在地点仍存在，已列出的
// 物品种类仍被允许（数量为零也算），各角色携带总量不超过目标上限；目标中
// 没有该角色上限时表示不设上限。允许删去无人所在的地点、不再出现的物品种
// 类或旧连通关系，也允许增加内容。升级后移动和物品变化按目标规则处理。
func checkCompatibility(st State, target Rules) []Blocker {
	locSet := make(map[string]struct{}, len(target.Locations))
	for _, l := range target.Locations {
		locSet[l] = struct{}{}
	}
	kindSet := make(map[string]struct{}, len(target.ItemKinds))
	for _, k := range target.ItemKinds {
		kindSet[k] = struct{}{}
	}
	var blockers []Blocker
	for _, ch := range st.Characters {
		if _, ok := locSet[ch.Location]; !ok {
			blockers = append(blockers, Blocker{
				Character: ch.ID,
				Kind:      BlockerLocation,
				Location:  ch.Location,
			})
		}
		total := 0
		for _, it := range ch.Items {
			if _, ok := kindSet[it.Item]; !ok {
				blockers = append(blockers, Blocker{
					Character: ch.ID,
					Kind:      BlockerItem,
					Item:      it.Item,
				})
			}
			total += it.Count
		}
		if limit, ok := target.CarryLimits[ch.ID]; ok && total > limit {
			blockers = append(blockers, Blocker{
				Character: ch.ID,
				Kind:      BlockerLimit,
				Total:     total,
				Limit:     limit,
			})
		}
	}
	// 同类问题按角色标识、物品标识排序；location/limit 阻碍没有物品标识，
	// 以空物品标识参与排序，保证重复检查输出顺序一致。跨类别按
	// location、item、limit 的自然顺序分组，便于阅读。
	kindRank := func(k BlockerKind) int {
		switch k {
		case BlockerLocation:
			return 0
		case BlockerItem:
			return 1
		case BlockerLimit:
			return 2
		}
		return 3
	}
	sort.SliceStable(blockers, func(i, j int) bool {
		if blockers[i].Character != blockers[j].Character {
			return blockers[i].Character < blockers[j].Character
		}
		if blockers[i].Kind != blockers[j].Kind {
			return kindRank(blockers[i].Kind) < kindRank(blockers[j].Kind)
		}
		return blockers[i].Item < blockers[j].Item
	})
	return blockers
}

// checkUpgradeLocked 在调用方已持锁（共享或排他均可）的前提下读取并校验
// 槽当前最新记录，计算目标规则的兼容性。返回检查结果与来源记录信封。
func (a *Archive) checkUpgradeLocked(slot string, acceptedVersions []string, target Rules) (UpgradeCheck, *envelope, error) {
	latest, err := a.readLatestLocked(slot)
	if err != nil {
		return UpgradeCheck{}, nil, err
	}
	env, err := a.loadAndVerifyLocked(latest)
	if err != nil {
		if ce, ok := err.(*CorruptError); ok {
			ce.Slot = slot
		}
		return UpgradeCheck{}, nil, err
	}
	if !versionAccepted(env.State.Rules.Version, acceptedVersions) {
		return UpgradeCheck{}, nil, &VersionRejectedError{
			Slot:     slot,
			Record:   latest,
			Version:  env.State.Rules.Version,
			Accepted: append([]string(nil), acceptedVersions...),
		}
	}
	if target.Version == env.State.Rules.Version {
		return UpgradeCheck{}, nil, &RuleError{Reason: "目标规则版本必须与来源记录版本不同"}
	}
	check := UpgradeCheck{
		RecordID:      latest,
		OldVersion:    env.State.Rules.Version,
		TargetVersion: target.Version,
	}
	blockers := checkCompatibility(env.State, target)
	check.Compatible = len(blockers) == 0
	check.Blockers = blockers
	return check, env, nil
}

// CheckUpgrade 检查槽当前最新记录能否在目标规则下继续使用。
//
// 检查只读取来源记录并计算兼容性，不改变世界、历史或槽当前记录，也不会
// 替换记录中的规则。target 必须先满足规则自身的合法性要求，且版本与来源
// 记录不同（版本只按相等判断，不按数字大小限制升级）。
//
// 目标规则非法返回 *RuleError；版本相同返回 *RuleError；来源损坏返回
// *CorruptError；来源版本不在 acceptedVersions 内返回 *VersionRejectedError；
// 槽不存在沿用现有不存在错误。
func (a *Archive) CheckUpgrade(slot string, acceptedVersions []string, target Rules) (UpgradeCheck, error) {
	if !validSlotName(slot) {
		return UpgradeCheck{}, &NotFoundError{Slot: slot}
	}
	if err := validateRules(target); err != nil {
		return UpgradeCheck{}, err
	}
	lock, err := acquireSharedLock(a.dir)
	if err != nil {
		return UpgradeCheck{}, err
	}
	defer lock.release()
	check, _, err := a.checkUpgradeLocked(slot, acceptedVersions, target)
	if err != nil {
		return UpgradeCheck{}, err
	}
	return check, nil
}

// Upgrade 在检查通过后用目标规则生成一条新记录。
//
// 传入与 CheckUpgrade 相同的条件，并带调用方预期的最新记录标识 expected。
// 提交时在排他锁下重新判断当前来源：仍有阻碍则返回相同的阻碍信息并拒绝
// 写入；expected 为空或已不是该槽最新记录时返回现有冲突错误。
//
// 被拒绝的升级不增加历史记录，也不改变槽当前记录。成功后槽增加一条完整
// 的新记录，以被升级记录为父，保存目标规则；种子、时间片、角色位置及
// 物品数量和排列保持原样。并发升级或升级与普通覆盖竞争时，只有 expected
// 与最新记录一致的一个成功，其余收到冲突。
func (a *Archive) Upgrade(slot string, acceptedVersions []string, target Rules, expected RecordID) (UpgradeResult, error) {
	if !validSlotName(slot) {
		return UpgradeResult{}, &NotFoundError{Slot: slot}
	}
	if err := validateRules(target); err != nil {
		return UpgradeResult{}, err
	}
	if expected == "" {
		return UpgradeResult{}, &ConflictError{Slot: slot, Reason: "升级必须提供所依据的记录标识"}
	}
	lock, err := acquireLock(a.dir)
	if err != nil {
		return UpgradeResult{}, err
	}
	defer lock.release()

	oldPointer, err := a.readSlotPointerLocked(slot)
	if err != nil {
		return UpgradeResult{}, err
	}
	latest := oldPointer.Latest
	if latest != expected {
		return UpgradeResult{}, &ConflictError{
			Slot:   slot,
			Reason: "所依据的记录已不是该槽最新记录",
		}
	}

	check, env, err := a.checkUpgradeLocked(slot, acceptedVersions, target)
	if err != nil {
		return UpgradeResult{}, err
	}
	if !check.Compatible {
		return UpgradeResult{Check: check}, &RuleError{
			Reason: fmt.Sprintf("升级被阻碍: 目标规则无法承接当前角色状态（%d 处阻碍）", len(check.Blockers)),
		}
	}

	id, err := newRecordID()
	if err != nil {
		return UpgradeResult{}, err
	}
	newState := State{
		Seed:       env.State.Seed,
		Rules:      cloneRules(target),
		Time:       env.State.Time,
		Characters: cloneCharacters(env.State.Characters),
	}
	newEnv := &envelope{
		Format:    archiveFormatVersion,
		ID:        id,
		Parent:    expected,
		SlotFirst: false,
		State:     newState,
	}
	newEnv.Checksum = computeChecksum(newEnv)
	if err := writeRecord(a.dir, newEnv); err != nil {
		return UpgradeResult{}, err
	}
	if err := a.commitSlotPointerLocked(slot, oldPointer, latest, id); err != nil {
		return UpgradeResult{}, err
	}
	info := RecordInfo{
		ID:      id,
		Parent:  expected,
		Version: target.Version,
	}
	return UpgradeResult{Check: check, Record: info}, nil
}
