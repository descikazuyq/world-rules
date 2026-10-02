package world

import "math/big"

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

	// 物品数量变化：先按角色+物品种类合并同批全部增减量，再与原数量合并
	// 判断最终结果。中途暂时为负或超出 int 范围都允许，只要最终数量合法
	// （非负且能被 int 表示）且符合携带上限；增减次序不影响成败与最终数量。
	type itemKey struct {
		char string
		item string
	}
	deltas := make(map[itemKey]*big.Int)
	for _, ic := range c.ItemChanges {
		if _, ok := chars[ic.Character]; !ok {
			return State{}, ruleErrorf("物品变化引用了不存在的角色: %q", ic.Character)
		}
		if _, ok := kindSet[ic.Item]; !ok {
			return State{}, ruleErrorf("角色 %q 的物品变化引用了规则不允许的物品: %q",
				ic.Character, ic.Item)
		}
		key := itemKey{ic.Character, ic.Item}
		if deltas[key] == nil {
			deltas[key] = new(big.Int)
		}
		deltas[key].Add(deltas[key], big.NewInt(int64(ic.Delta)))
	}

	// 逐角色合并原数量与增减量，校验最终数量与携带总量。
	for ci := range next.Characters {
		ch := &next.Characters[ci]
		finalByName := make(map[string]*big.Int, len(ch.Items))
		order := make([]string, 0, len(ch.Items))
		for _, it := range ch.Items {
			finalByName[it.Item] = big.NewInt(int64(it.Count))
			order = append(order, it.Item)
		}
		for key, d := range deltas {
			if key.char != ch.ID {
				continue
			}
			if _, ok := finalByName[key.item]; !ok {
				finalByName[key.item] = new(big.Int)
				order = append(order, key.item)
			}
			finalByName[key.item].Add(finalByName[key.item], d)
			delete(deltas, key)
		}

		items := make([]CharacterItem, 0, len(order))
		total := new(big.Int)
		for _, name := range order {
			final := finalByName[name]
			if final.Sign() < 0 {
				return State{}, ruleErrorf("角色 %q 的物品 %q 最终数量不能为负: %s",
					ch.ID, name, final.String())
			}
			if !fitsInInt(final) {
				return State{}, ruleErrorf("角色 %q 的物品 %q 最终数量超出整数范围",
					ch.ID, name)
			}
			items = append(items, CharacterItem{Item: name, Count: int(final.Int64())})
			total.Add(total, final)
		}
		ch.Items = items
		if limit, ok := next.Rules.CarryLimits[ch.ID]; ok && total.Cmp(big.NewInt(int64(limit))) > 0 {
			return State{}, ruleErrorf("角色 %q 携带总量 %s 超过上限 %d", ch.ID, total.String(), limit)
		}
	}

	next.Time = c.Time
	return next, nil
}
