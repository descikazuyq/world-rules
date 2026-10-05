package world

import "testing"

// orderRules 提供三个地点、五种物品的规则，默认不设携带上限。
func orderRules() Rules {
	return Rules{
		Version:   "v1",
		Locations: []string{"hall", "yard", "cave"},
		Edges: []Edge{
			{From: "hall", To: "yard"},
			{From: "yard", To: "cave"},
		},
		ItemKinds: []string{"gold", "key", "gem", "ore", "wood"},
	}
}

// orderWorld 建立一个两角色世界：hero 的物品列表刻意不按名称排序且含
// 零数量条目（key=2, gold=0, gem=3），mage 持有一种物品（ore=1）。
func orderWorld(t *testing.T) *World {
	t.Helper()
	w, err := NewWorld(InitialData{
		Seed:  7,
		Rules: orderRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "key", Count: 2},
				{Item: "gold", Count: 0},
				{Item: "gem", Count: 3},
			}},
			{ID: "mage", Location: "yard", Items: []CharacterItem{
				{Item: "ore", Count: 1},
			}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	return w
}

// requireItems 断言角色的物品列表与期望逐项一致（种类、数量与排列）。
func requireItems(t *testing.T, charID string, got, want []CharacterItem) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("角色 %q 物品条数应为 %d，得到 %+v", charID, len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("角色 %q 物品[%d] 应为 %+v，得到 %+v（完整列表 %+v）",
				charID, i, want[i], got[i], got)
		}
	}
}

// 已有条目保持原位、未涉及的条目原样保留；新增种类只追加一个条目，按其在
// 该角色增减条目中首次出现的先后排列。同一种新增物品出现多次时数量合并到
// 首次出现的位置，不产生重复条目，也不被移到最后一次出现的位置。
func TestApplyItemOrderKeepsExistingAndAppendsNew(t *testing.T) {
	w := orderWorld(t)
	st, err := w.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "gem", Delta: -1}, // 已有条目，原位改数量
			{Character: "hero", Item: "ore", Delta: 2},  // 新增种类，首次出现
			{Character: "hero", Item: "gold", Delta: 2}, // 已有零数量条目，原位改数量
			{Character: "hero", Item: "wood", Delta: 1}, // 新增种类，第二次出现的新种类
			{Character: "hero", Item: "ore", Delta: 1},  // ore 再次出现：合并，不重复、不后移
		},
		Time: 3,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	wantHero := []CharacterItem{
		{Item: "key", Count: 2},  // 未涉及的条目保持原样
		{Item: "gold", Count: 2}, // 已有条目保持原位
		{Item: "gem", Count: 2},
		{Item: "ore", Count: 3}, // 新增种类按首次出现次序追加，数量合并
		{Item: "wood", Count: 1},
	}
	hero := findCharacter(t, st, "hero")
	requireItems(t, "hero", hero.Items, wantHero)
	// 未涉及的角色不受影响。
	requireItems(t, "mage", findCharacter(t, st, "mage").Items,
		[]CharacterItem{{Item: "ore", Count: 1}})
	// 返回状态与世界当前快照都应体现正确的数量和排列。
	requireItems(t, "hero", findCharacter(t, w.Snapshot(), "hero").Items, wantHero)
}

// 已有物品减到零后条目仍保留在原位置；原先未列出的物品先增加再减少、最终
// 为零时同样追加并保留，不因净变化为零而省略，位置按首次出现确定。
func TestApplyItemZeroCountEntriesRetained(t *testing.T) {
	w := orderWorld(t)
	st, err := w.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "gem", Delta: -3}, // 已有物品减到零
			{Character: "hero", Item: "ore", Delta: 2},  // 新物品先增加
			{Character: "hero", Item: "wood", Delta: 1}, // 另一新种类，首次出现晚于 ore
			{Character: "hero", Item: "ore", Delta: -2}, // ore 再减少，净变化为零
		},
		Time: 2,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	requireItems(t, "hero", findCharacter(t, st, "hero").Items, []CharacterItem{
		{Item: "key", Count: 2},
		{Item: "gold", Count: 0}, // 原有的零数量条目保留
		{Item: "gem", Count: 0},  // 减到零的已有条目保留在原位
		{Item: "ore", Count: 0},  // 净变化为零的新物品仍追加并保留
		{Item: "wood", Count: 1},
	})
}

// 不同角色的物品增减交错出现、且涉及相同物品名称时，每个角色独立承接自己
// 的原列表和新增次序：mage 的新增种类按 mage 自己的首次出现次序排列，不套
// 用 hero 的次序；角色本身的排列也保持不变。
func TestApplyItemOrderInterleavedCharacters(t *testing.T) {
	w, err := NewWorld(InitialData{
		Seed:  7,
		Rules: orderRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "key", Count: 2},
				{Item: "gold", Count: 0},
			}},
			{ID: "mage", Location: "yard", Items: []CharacterItem{
				{Item: "gold", Count: 1},
			}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	st, err := w.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "ore", Delta: 1},  // hero 的新增：ore 先出现
			{Character: "mage", Item: "key", Delta: 2},  // mage 的新增：key 先出现
			{Character: "hero", Item: "gold", Delta: 1}, // hero 的已有条目
			{Character: "mage", Item: "ore", Delta: 1},  // mage 的新增：ore 晚于 key
			{Character: "hero", Item: "wood", Delta: 1}, // hero 的新增：wood 最后
			{Character: "mage", Item: "gold", Delta: 1}, // mage 的已有条目
		},
		Time: 4,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// 角色排列保持提交前次序。
	if len(st.Characters) != 2 || st.Characters[0].ID != "hero" || st.Characters[1].ID != "mage" {
		t.Fatalf("角色排列应保持不变，得到 %+v", st.Characters)
	}
	// hero 的新增次序是 ore、wood；mage 的新增次序是 key、ore，各自独立。
	requireItems(t, "hero", st.Characters[0].Items, []CharacterItem{
		{Item: "key", Count: 2},
		{Item: "gold", Count: 1},
		{Item: "ore", Count: 1},
		{Item: "wood", Count: 1},
	})
	requireItems(t, "mage", st.Characters[1].Items, []CharacterItem{
		{Item: "gold", Count: 2},
		{Item: "key", Count: 2},
		{Item: "ore", Count: 1},
	})
	// 世界快照与返回状态一致。
	snap := w.Snapshot()
	requireItems(t, "hero", findCharacter(t, snap, "hero").Items, st.Characters[0].Items)
	requireItems(t, "mage", findCharacter(t, snap, "mage").Items, st.Characters[1].Items)
}

// 在最终数量合法的前提下，只改变同一物品增减条目的次序不会改变最终数量与
// 列表排列；若同时改变了不同新增种类第一次出现的先后，列表次序随之改变，
// 而各种类的最终数量不变。
func TestApplyItemDeltaReorderVsKindFirstAppearance(t *testing.T) {
	newHeroWorld := func(t *testing.T) *World {
		t.Helper()
		w, err := NewWorld(InitialData{
			Seed:  7,
			Rules: orderRules(),
			Characters: []Character{
				{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
			},
		})
		if err != nil {
			t.Fatalf("NewWorld: %v", err)
		}
		return w
	}

	// 同一物品（gold）的三条增减以不同次序出现，新增种类次序相同。
	wa, wb := newHeroWorld(t), newHeroWorld(t)
	sta, err := wa.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "gold", Delta: 1},
			{Character: "hero", Item: "gold", Delta: 2},
			{Character: "hero", Item: "ore", Delta: 1},
			{Character: "hero", Item: "wood", Delta: 1},
			{Character: "hero", Item: "gold", Delta: -1},
		},
		Time: 1,
	})
	if err != nil {
		t.Fatalf("Apply A: %v", err)
	}
	stb, err := wb.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "gold", Delta: 2},
			{Character: "hero", Item: "gold", Delta: -1},
			{Character: "hero", Item: "gold", Delta: 1},
			{Character: "hero", Item: "ore", Delta: 1},
			{Character: "hero", Item: "wood", Delta: 1},
		},
		Time: 1,
	})
	if err != nil {
		t.Fatalf("Apply B: %v", err)
	}
	if !statesEqual(sta, stb) {
		t.Fatalf("只重排同一物品的增减条目不应改变结果: A %+v, B %+v",
			sta.Characters[0].Items, stb.Characters[0].Items)
	}
	requireItems(t, "hero", sta.Characters[0].Items, []CharacterItem{
		{Item: "gold", Count: 3},
		{Item: "ore", Count: 1},
		{Item: "wood", Count: 1},
	})

	// 最终数量相同，但 wood 的首次出现早于 ore：列表次序随之改变。
	wc := newHeroWorld(t)
	stc, err := wc.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "gold", Delta: 1},
			{Character: "hero", Item: "gold", Delta: 2},
			{Character: "hero", Item: "gold", Delta: -1},
			{Character: "hero", Item: "wood", Delta: 1},
			{Character: "hero", Item: "ore", Delta: 1},
		},
		Time: 1,
	})
	if err != nil {
		t.Fatalf("Apply C: %v", err)
	}
	requireItems(t, "hero", stc.Characters[0].Items, []CharacterItem{
		{Item: "gold", Count: 3},
		{Item: "wood", Count: 1},
		{Item: "ore", Count: 1},
	})
}

// 提交中含合法移动、新增物品和已有物品增减，但某个角色的最终数量为负或
// 最终携带量超过上限时整次失败：返回 *RuleError，其他角色也不留下新条目、
// 归零结果或次序变化，所有角色的位置、物品列表和时间片保持提交前的内容。
func TestApplyItemFailureLeavesNoListTrace(t *testing.T) {
	cases := []struct {
		name    string
		changes []ItemChange
		substrs []string
	}{
		{"最终数量为负", []ItemChange{
			{Character: "mage", Item: "wood", Delta: 2}, // 其他角色的新增物品
			{Character: "mage", Item: "ore", Delta: -1}, // 其他角色的已有物品会归零
			{Character: "hero", Item: "gem", Delta: -4}, // hero 最终数量 -1，整次失败
		}, []string{"hero", "gem"}},
		{"超过携带上限", []ItemChange{
			{Character: "mage", Item: "wood", Delta: 2},
			{Character: "mage", Item: "ore", Delta: -1},
			{Character: "hero", Item: "key", Delta: 1}, // hero 总量 6 超过上限 5
		}, []string{"hero", "上限"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules := orderRules()
			rules.CarryLimits = map[string]int{"hero": 5}
			w, err := NewWorld(InitialData{
				Seed:  7,
				Rules: rules,
				Characters: []Character{
					{ID: "hero", Location: "hall", Items: []CharacterItem{
						{Item: "key", Count: 2},
						{Item: "gold", Count: 0},
						{Item: "gem", Count: 3},
					}},
					{ID: "mage", Location: "yard", Items: []CharacterItem{
						{Item: "ore", Count: 1},
					}},
				},
			})
			if err != nil {
				t.Fatalf("NewWorld: %v", err)
			}
			before := w.Snapshot()
			_, err = w.Apply(Commit{
				Moves:       []Move{{Character: "mage", To: "cave"}}, // 合法移动
				ItemChanges: tc.changes,
				Time:        7,
			})
			mustRuleError(t, err, tc.substrs...)
			after := w.Snapshot()
			if !statesEqual(before, after) {
				t.Fatalf("失败提交留下了变化: 前 %+v, 后 %+v", before, after)
			}
			// 时间片不推进。
			if after.Time != 0 {
				t.Fatalf("时间片应保持 0，得到 %d", after.Time)
			}
			// 其他角色不留下新条目、归零结果或次序变化，位置也不变。
			mage := findCharacter(t, after, "mage")
			if mage.Location != "yard" {
				t.Fatalf("mage 应仍在 yard，得到 %q", mage.Location)
			}
			requireItems(t, "mage", mage.Items, []CharacterItem{{Item: "ore", Count: 1}})
			requireItems(t, "hero", findCharacter(t, after, "hero").Items, []CharacterItem{
				{Item: "key", Count: 2},
				{Item: "gold", Count: 0},
				{Item: "gem", Count: 3},
			})
		})
	}
}
