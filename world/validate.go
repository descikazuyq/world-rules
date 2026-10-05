package world

import "fmt"

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
func validateRules(r Rules) error {
	if r.Version == "" {
		return ruleErrorf("规则版本不能为空")
	}
	locs := make(map[string]struct{}, len(r.Locations))
	for _, l := range r.Locations {
		if l == "" {
			return ruleErrorf("地点标识不能为空")
		}
		if _, ok := locs[l]; ok {
			return ruleErrorf("地点标识重复: %q", l)
		}
		locs[l] = struct{}{}
	}
	for i, e := range r.Edges {
		if _, ok := locs[e.From]; !ok {
			return ruleErrorf("连通关系 %d 引用了不存在的地点: %q", i, e.From)
		}
		if _, ok := locs[e.To]; !ok {
			return ruleErrorf("连通关系 %d 引用了不存在的地点: %q", i, e.To)
		}
	}
	kinds := make(map[string]struct{}, len(r.ItemKinds))
	for _, k := range r.ItemKinds {
		if k == "" {
			return ruleErrorf("物品种类标识不能为空")
		}
		if _, ok := kinds[k]; ok {
			return ruleErrorf("物品种类重复: %q", k)
		}
		kinds[k] = struct{}{}
	}
	for who, limit := range r.CarryLimits {
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
	for _, c := range chars {
		if c.ID == "" {
			return ruleErrorf("角色标识不能为空")
		}
		if _, ok := ids[c.ID]; ok {
			return ruleErrorf("角色标识重复: %q", c.ID)
		}
		ids[c.ID] = struct{}{}
		if _, ok := locSet[c.Location]; !ok {
			return ruleErrorf("角色 %q 位于不存在的地点: %q", c.ID, c.Location)
		}
		seen := make(map[string]struct{}, len(c.Items))
		for _, it := range c.Items {
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
		// 携带上限按真实总量判断；未设上限的角色不检查总量。
		if err := checkCarryLimit(c.ID, c.Items, r.CarryLimits); err != nil {
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
