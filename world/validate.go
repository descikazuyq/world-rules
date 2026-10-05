package world

import (
	"fmt"
	"unicode/utf8"
)

// RuleError 表示数据或提交违反了世界规则。
type RuleError struct {
	Reason string
}

func (e *RuleError) Error() string { return "world: " + e.Reason }

func ruleErrorf(format string, args ...any) error {
	return &RuleError{Reason: fmt.Sprintf(format, args...)}
}

// validateRules 校验规则本身：非空版本、地点不重复且非空标识、
// 连通关系只引用已有地点、物品不重复、携带上限非负。
//
// 全部文本标识（版本、地点、道路端点、物品种类、携带上限的角色键）还
// 必须是合法 UTF-8：含无效字节的字符串经 JSON 保存会被悄悄改写成替换
// 字符，可能使不同标识重名，因此必须在接纳世界数据时拒绝，即使该地点、
// 物品或上限键暂时无人使用。
func validateRules(r Rules) error {
	if !utf8.ValidString(r.Version) {
		return ruleErrorf("规则版本不是合法 UTF-8: %q", r.Version)
	}
	if r.Version == "" {
		return ruleErrorf("规则版本不能为空")
	}
	locs := make(map[string]struct{}, len(r.Locations))
	for i, l := range r.Locations {
		if !utf8.ValidString(l) {
			return ruleErrorf("第 %d 个地点标识不是合法 UTF-8: %q", i, l)
		}
		if l == "" {
			return ruleErrorf("地点标识不能为空")
		}
		if _, ok := locs[l]; ok {
			return ruleErrorf("地点标识重复: %q", l)
		}
		locs[l] = struct{}{}
	}
	for i, e := range r.Edges {
		if !utf8.ValidString(e.From) {
			return ruleErrorf("连通关系 %d 的起点不是合法 UTF-8: %q", i, e.From)
		}
		if !utf8.ValidString(e.To) {
			return ruleErrorf("连通关系 %d 的终点不是合法 UTF-8: %q", i, e.To)
		}
		if _, ok := locs[e.From]; !ok {
			return ruleErrorf("连通关系 %d 引用了不存在的地点: %q", i, e.From)
		}
		if _, ok := locs[e.To]; !ok {
			return ruleErrorf("连通关系 %d 引用了不存在的地点: %q", i, e.To)
		}
	}
	kinds := make(map[string]struct{}, len(r.ItemKinds))
	for i, k := range r.ItemKinds {
		if !utf8.ValidString(k) {
			return ruleErrorf("第 %d 个物品种类标识不是合法 UTF-8: %q", i, k)
		}
		if k == "" {
			return ruleErrorf("物品种类标识不能为空")
		}
		if _, ok := kinds[k]; ok {
			return ruleErrorf("物品种类重复: %q", k)
		}
		kinds[k] = struct{}{}
	}
	for who, limit := range r.CarryLimits {
		if !utf8.ValidString(who) {
			return ruleErrorf("携带上限的角色键不是合法 UTF-8: %q", who)
		}
		if limit < 0 {
			return ruleErrorf("角色 %q 的携带上限不能为负: %d", who, limit)
		}
	}
	return nil
}

func (r Rules) adjacent(from, to string) bool {
	for _, e := range r.Edges {
		if (e.From == from && e.To == to) || (e.From == to && e.To == from) {
			return true
		}
	}
	return false
}

// validateCharacters 校验角色集合：标识不重复、位于已有地点；
// 物品种类合法、数量非负、不重复，总量不超过携带上限。
//
// 角色标识、所在地点与所持物品名称同样必须是合法 UTF-8，理由与
// validateRules 相同：否则保存后标识可能被改写或与其他标识重名。
func validateCharacters(chars []Character, r Rules) error {
	locSet := make(map[string]struct{}, len(r.Locations))
	for _, l := range r.Locations {
		locSet[l] = struct{}{}
	}
	kindSet := make(map[string]struct{}, len(r.ItemKinds))
	for _, k := range r.ItemKinds {
		kindSet[k] = struct{}{}
	}
	ids := make(map[string]struct{}, len(chars))
	for i, c := range chars {
		if !utf8.ValidString(c.ID) {
			return ruleErrorf("第 %d 个角色标识不是合法 UTF-8: %q", i, c.ID)
		}
		if c.ID == "" {
			return ruleErrorf("角色标识不能为空")
		}
		if _, ok := ids[c.ID]; ok {
			return ruleErrorf("角色标识重复: %q", c.ID)
		}
		ids[c.ID] = struct{}{}
		if !utf8.ValidString(c.Location) {
			return ruleErrorf("角色 %q 的所在地点不是合法 UTF-8: %q", c.ID, c.Location)
		}
		if _, ok := locSet[c.Location]; !ok {
			return ruleErrorf("角色 %q 位于不存在的地点: %q", c.ID, c.Location)
		}
		seen := make(map[string]struct{}, len(c.Items))
		for _, it := range c.Items {
			if !utf8.ValidString(it.Item) {
				return ruleErrorf("角色 %q 持有名称不是合法 UTF-8 的物品: %q", c.ID, it.Item)
			}
			if _, ok := kindSet[it.Item]; !ok {
				return ruleErrorf("角色 %q 持有规则不允许的物品: %q", c.ID, it.Item)
			}
			if _, dup := seen[it.Item]; dup {
				return ruleErrorf("角色 %q 的物品 %q 重复列出", c.ID, it.Item)
			}
			seen[it.Item] = struct{}{}
			if it.Count < 0 {
				return ruleErrorf("角色 %q 的物品 %q 数量不能为负: %d", c.ID, it.Item, it.Count)
			}
		}
		// 携带上限按真实总量判断，规则与建立世界、提交物品变化共用同一口径：
		// 精确求和不因回绕放过超限；未设上限的角色不检查总量，其总量允许
		// 超过 int 最大值。
		if err := carryLimitError(c.ID, c.Items, r.CarryLimits); err != nil {
			return err
		}
	}
	return nil
}

// validateInitialData 校验建立世界的初始数据。
func validateInitialData(d InitialData) error {
	if err := validateRules(d.Rules); err != nil {
		return err
	}
	if err := validateCharacters(d.Characters, d.Rules); err != nil {
		return err
	}
	return nil
}
