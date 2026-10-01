package world

// World 是一个本地世界实例。它持有当前完整状态，零值不可用，
// 必须通过 NewWorld 建立。
type World struct {
	state State
}

// NewWorld 用初始数据建立世界。时间片从零开始；初始数据违反规则时
// 返回 *RuleError，且不会产生可用世界。
func NewWorld(data InitialData) (*World, error) {
	if err := validateInitialData(data); err != nil {
		return nil, err
	}
	return &World{state: State{
		Seed:       data.Seed,
		Rules:      cloneRules(data.Rules),
		Time:       0,
		Characters: cloneCharacters(data.Characters),
	}}, nil
}

// WorldFromState 用一份完整状态（典型地来自存档记录）重建世界，
// 保留其中的时间片。与 NewWorld 不同，它用于“读档后继续”：状态必须
// 自洽且时间片非负，否则返回 *RuleError。入参会被深拷贝。
func WorldFromState(st State) (*World, error) {
	if st.Time < 0 {
		return nil, ruleErrorf("时间片不能为负: %d", st.Time)
	}
	if err := validateInitialData(InitialData{
		Seed:       st.Seed,
		Rules:      st.Rules,
		Characters: st.Characters,
	}); err != nil {
		return nil, err
	}
	return &World{state: cloneState(st)}, nil
}

// Snapshot 返回当前世界状态的深拷贝，修改返回值不会影响世界。
func (w *World) Snapshot() State {
	return cloneState(w.state)
}

// Apply 在一次原子提交中应用若干移动、物品数量变化和时间推进。
//
// 提交中的任意一项不合法（角色/物品不存在、移动不沿允许的连通关系、
// 物品数量变负、总量超过携带上限、时间倒退等）都会使整次提交失败，
// 世界保持提交前状态。成功时返回提交后的状态深拷贝。
func (w *World) Apply(c Commit) (State, error) {
	next, err := w.state.apply(c)
	if err != nil {
		return State{}, err
	}
	w.state = next
	return cloneState(w.state), nil
}

func (s State) apply(c Commit) (State, error) {
	if c.Time < s.Time {
		return State{}, ruleErrorf("时间不能倒退: 当前 %d, 提交目标 %d", s.Time, c.Time)
	}

	next := cloneState(s)

	locSet := make(map[string]struct{}, len(next.Rules.Locations))
	for _, l := range next.Rules.Locations {
		locSet[l] = struct{}{}
	}
	kindSet := make(map[string]struct{}, len(next.Rules.ItemKinds))
	for _, k := range next.Rules.ItemKinds {
		kindSet[k] = struct{}{}
	}
	chars := make(map[string]*Character, len(next.Characters))
	for i := range next.Characters {
		chars[next.Characters[i].ID] = &next.Characters[i]
	}

	// 移动：逐条应用，每条都必须沿规则允许的连通关系。
	for _, m := range c.Moves {
		ch, ok := chars[m.Character]
		if !ok {
			return State{}, ruleErrorf("移动引用了不存在的角色: %q", m.Character)
		}
		if _, ok := locSet[m.To]; !ok {
			return State{}, ruleErrorf("角色 %q 移动到不存在的地点: %q", m.Character, m.To)
		}
		if !next.Rules.adjacent(ch.Location, m.To) {
			return State{}, ruleErrorf("角色 %q 不能从 %q 移动到 %q：没有允许的连通关系",
				m.Character, ch.Location, m.To)
		}
		ch.Location = m.To
	}

	// 物品数量变化：先在工作副本上累计，最后统一校验非负与上限。
	for _, ic := range c.ItemChanges {
		ch, ok := chars[ic.Character]
		if !ok {
			return State{}, ruleErrorf("物品变化引用了不存在的角色: %q", ic.Character)
		}
		if _, ok := kindSet[ic.Item]; !ok {
			return State{}, ruleErrorf("角色 %q 的物品变化引用了规则不允许的物品: %q",
				ic.Character, ic.Item)
		}
		idx := -1
		for i := range ch.Items {
			if ch.Items[i].Item == ic.Item {
				idx = i
				break
			}
		}
		if idx == -1 {
			ch.Items = append(ch.Items, CharacterItem{Item: ic.Item, Count: ic.Delta})
		} else {
			ch.Items[idx].Count += ic.Delta
		}
	}
	for _, ch := range next.Characters {
		total := 0
		for _, it := range ch.Items {
			if it.Count < 0 {
				return State{}, ruleErrorf("角色 %q 的物品 %q 数量不能为负: %d",
					ch.ID, it.Item, it.Count)
			}
			total += it.Count
		}
		if limit, ok := next.Rules.CarryLimits[ch.ID]; ok && total > limit {
			return State{}, ruleErrorf("角色 %q 携带总量 %d 超过上限 %d", ch.ID, total, limit)
		}
	}

	next.Time = c.Time
	return next, nil
}
