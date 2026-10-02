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
// 物品最终数量为负或超出 int 范围、真实总量超过携带上限、时间倒退等）
// 都会使整次提交失败，世界保持提交前状态。同一角色同一物品的全部增减
// 与原始数量合并后判断最终结果，中途暂时为负或超出 int 范围不失败，
// 增减条目次序不影响成败与最终数量。成功时返回提交后的状态深拷贝。
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

	// 物品数量变化：同一角色同一物品的全部增减先与原始数量合并（精确整数
	// 运算，不回绕），再统一判断最终结果。中途暂时为负或超出 int 范围都
	// 不失败，因此增减条目的次序不影响成败与最终数量。不存在的角色与不被
	// 规则允许的物品仍按条目拒绝，即使其增减相互抵消。
	totals := make(map[string]map[string]*big.Int, len(next.Characters))
	extras := make(map[string][]string)
	for _, ic := range c.ItemChanges {
		ch, ok := chars[ic.Character]
		if !ok {
			return State{}, ruleErrorf("物品变化引用了不存在的角色: %q", ic.Character)
		}
		if _, ok := kindSet[ic.Item]; !ok {
			return State{}, ruleErrorf("角色 %q 的物品变化引用了规则不允许的物品: %q",
				ic.Character, ic.Item)
		}
		items, ok := totals[ic.Character]
		if !ok {
			items = make(map[string]*big.Int, len(ch.Items)+1)
			for _, it := range ch.Items {
				items[it.Item] = bigInt(it.Count)
			}
			totals[ic.Character] = items
		}
		acc, ok := items[ic.Item]
		if !ok {
			acc = new(big.Int)
			items[ic.Item] = acc
			extras[ic.Character] = append(extras[ic.Character], ic.Item)
		}
		acc.Add(acc, bigInt(ic.Delta))
	}
	// 把合并后的最终数量写回工作副本：已有条目保持原位，新物品按增减
	// 条目首次出现的次序追加，零数量条目照常保留。
	for i := range next.Characters {
		ch := &next.Characters[i]
		items, ok := totals[ch.ID]
		if !ok {
			continue
		}
		for j := range ch.Items {
			count, err := finalCount(ch.ID, ch.Items[j].Item, items[ch.Items[j].Item])
			if err != nil {
				return State{}, err
			}
			ch.Items[j].Count = count
		}
		for _, name := range extras[ch.ID] {
			count, err := finalCount(ch.ID, name, items[name])
			if err != nil {
				return State{}, err
			}
			ch.Items = append(ch.Items, CharacterItem{Item: name, Count: count})
		}
	}
	// 携带上限按真实总量判断：精确求和，不因加法回绕而放过超限。未设
	// 上限的角色不检查总量，其总量允许超过 int 最大值。
	for _, ch := range next.Characters {
		limit, ok := next.Rules.CarryLimits[ch.ID]
		if !ok {
			continue
		}
		total := new(big.Int)
		for _, it := range ch.Items {
			total.Add(total, bigInt(it.Count))
		}
		if total.Cmp(bigInt(limit)) > 0 {
			return State{}, ruleErrorf("角色 %q 携带总量 %s 超过上限 %d", ch.ID, total.String(), limit)
		}
	}

	next.Time = c.Time
	return next, nil
}
