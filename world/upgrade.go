package world

import (
	"fmt"
	"sort"
)

// UpgradeCheck 是对槽最新存档能否在新规则下原样承接的一次检查结果。
//
// 检查只判断当前角色状态能否原样延续：每个角色所在地点仍存在、已列出
// 的物品种类仍被允许（数量为零也算）、各角色携带总量不超过目标上限；
// 不判断移动是否仍沿旧连通关系（允许删去无人所在的地点、旧边或不再
// 出现的物品种类，也允许增加内容）。检查本身不改变世界、历史或槽当前
// 记录。
type UpgradeCheck struct {
	// Record 是所检查的槽最新记录标识。
	Record RecordID
	// OldVersion 是来源记录的规则版本。
	OldVersion string
	// TargetVersion 是目标规则版本。
	TargetVersion string
	// Compatible 为 true 时表示当前状态可在目标规则下原样承接。
	Compatible bool
	// Blockers 是按角色标识、物品标识确定顺序的全部具体阻碍；
	// 兼容时为空。
	Blockers []UpgradeBlocker
}

// UpgradeBlocker 描述一项阻碍当前状态在目标规则下原样承接的具体问题。
type UpgradeBlocker struct {
	// Character 是有问题的角色标识。
	Character string
	// Kind 是阻碍类型：missing-location（角色所在地点被移除）、
	// removed-item（角色已列出的物品种类被移除，数量为零也算）、
	// over-limit（携带总量超过目标新上限）。
	Kind string
	// Location 仅在 Kind == "missing-location" 时有效，为缺失的地点。
	Location string
	// Item 仅在 Kind == "removed-item" 时有效，为被移除的物品种类。
	Item string
	// Total 与 Limit 仅在 Kind == "over-limit" 时有效，分别为实际
	// 携带总量与目标规则中的新上限。
	Total int
	Limit int
}

// 阻碍类型常量，供调用方按 UpgradeBlocker.Kind 区分具体问题。
const (
	// BlockerMissingLocation：角色所在地点在目标规则中不存在。
	BlockerMissingLocation = "missing-location"
	// BlockerRemovedItem：角色已列出的物品种类被目标规则移除
	// （数量为零也算）。
	BlockerRemovedItem = "removed-item"
	// BlockerOverLimit：角色实际携带总量超过目标规则中的新上限。
	BlockerOverLimit = "over-limit"
)

func (b UpgradeBlocker) Error() string {
	switch b.Kind {
	case BlockerMissingLocation:
		return fmt.Sprintf("角色 %q 所在地点 %q 在目标规则中不存在", b.Character, b.Location)
	case BlockerRemovedItem:
		return fmt.Sprintf("角色 %q 已列出的物品 %q 被目标规则移除", b.Character, b.Item)
	case BlockerOverLimit:
		return fmt.Sprintf("角色 %q 携带总量 %d 超过新上限 %d", b.Character, b.Total, b.Limit)
	default:
		return fmt.Sprintf("角色 %q 存在规则阻碍", b.Character)
	}
}

// sortBlockers 按角色标识、阻碍类型、物品/地点标识排序，使重复检查的
// 输出顺序保持一致。
func sortBlockers(bs []UpgradeBlocker) {
	sort.Slice(bs, func(i, j int) bool {
		a, b := bs[i], bs[j]
		if a.Character != b.Character {
			return a.Character < b.Character
		}
		if a.Kind != b.Kind {
			return blockerOrder(a.Kind) < blockerOrder(b.Kind)
		}
		// 同类问题按物品标识（removed-item）或地点标识
		// （missing-location）排序；over-limit 每角色至多一项。
		if a.Item != b.Item {
			return a.Item < b.Item
		}
		return a.Location < b.Location
	})
}

func blockerOrder(kind string) int {
	switch kind {
	case BlockerMissingLocation:
		return 0
	case BlockerRemovedItem:
		return 1
	case BlockerOverLimit:
		return 2
	default:
		return 3
	}
}

// findBlockers 检查 chars 在目标规则 target 下能否原样承接，一次返回
// 全部阻碍。目标规则的合法性由调用方保证。
func findBlockers(chars []Character, target Rules) []UpgradeBlocker {
	locations := make(map[string]struct{}, len(target.Locations))
	for _, l := range target.Locations {
		locations[l] = struct{}{}
	}
	items := make(map[string]struct{}, len(target.ItemKinds))
	for _, k := range target.ItemKinds {
		items[k] = struct{}{}
	}

	var blockers []UpgradeBlocker
	for _, c := range chars {
		if _, ok := locations[c.Location]; !ok {
			blockers = append(blockers, UpgradeBlocker{
				Character: c.ID,
				Kind:      BlockerMissingLocation,
				Location:  c.Location,
			})
		}
		total := 0
		itemBlockers := make([]UpgradeBlocker, 0, len(c.Items))
		for _, it := range c.Items {
			// 数量为零也算阻碍：它仍是“已列出的物品种类”。
			if _, ok := items[it.Item]; !ok {
				itemBlockers = append(itemBlockers, UpgradeBlocker{
					Character: c.ID,
					Kind:      BlockerRemovedItem,
					Item:      it.Item,
				})
			}
			total += it.Count
		}
		blockers = append(blockers, itemBlockers...)
		// 目标规则中没有该角色的上限时，仍表示不设上限。
		if limit, ok := target.CarryLimits[c.ID]; ok && total > limit {
			blockers = append(blockers, UpgradeBlocker{
				Character: c.ID,
				Kind:      BlockerOverLimit,
				Total:     total,
				Limit:     limit,
			})
		}
	}

	sortBlockers(blockers)
	return blockers
}
