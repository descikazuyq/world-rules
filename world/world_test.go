package world

import (
	"testing"
)

func baseRules() Rules {
	return Rules{
		Version:   "v1",
		Locations: []string{"hall", "yard", "cave"},
		Edges: []Edge{
			{From: "hall", To: "yard"},
			{From: "yard", To: "cave"},
		},
		ItemKinds:   []string{"gold", "key"},
		CarryLimits: map[string]int{"hero": 5, "mage": 2},
	}
}

func baseWorld(t *testing.T) *World {
	t.Helper()
	w, err := NewWorld(InitialData{
		Seed:  42,
		Rules: baseRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
			{ID: "mage", Location: "yard"},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	return w
}

func TestNewWorldValidation(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*InitialData)
	}{
		{"空规则版本", func(d *InitialData) { d.Rules.Version = "" }},
		{"地点重复", func(d *InitialData) { d.Rules.Locations = []string{"a", "a"} }},
		{"连通引用缺失地点", func(d *InitialData) { d.Rules.Edges = []Edge{{From: "a", To: "zzz"}} }},
		{"物品种类重复", func(d *InitialData) { d.Rules.ItemKinds = []string{"gold", "gold"} }},
		{"携带上限为负", func(d *InitialData) { d.Rules.CarryLimits["hero"] = -1 }},
		{"角色标识重复", func(d *InitialData) {
			d.Characters = append(d.Characters, Character{ID: "hero", Location: "hall"})
		}},
		{"角色在不存在地点", func(d *InitialData) { d.Characters[0].Location = "void" }},
		{"非法物品", func(d *InitialData) {
			d.Characters[0].Items = []CharacterItem{{Item: "rock", Count: 1}}
		}},
		{"物品数量为负", func(d *InitialData) {
			d.Characters[0].Items = []CharacterItem{{Item: "gold", Count: -1}}
		}},
		{"超过携带上限", func(d *InitialData) {
			d.Characters[0].Items = []CharacterItem{{Item: "gold", Count: 6}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := InitialData{
				Seed:       42,
				Rules:      baseRules(),
				Characters: []Character{{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}}},
			}
			tc.mod(&data)
			if _, err := NewWorld(data); err == nil {
				t.Fatal("期望建立世界失败，但成功了")
			} else if _, ok := err.(*RuleError); !ok {
				t.Fatalf("期望 *RuleError，得到 %T", err)
			}
		})
	}
}

func TestTimeStartsAtZero(t *testing.T) {
	w := baseWorld(t)
	if got := w.Snapshot().Time; got != 0 {
		t.Fatalf("初始时间片应为 0，得到 %d", got)
	}
}

func TestApplySuccessAndIsolation(t *testing.T) {
	w := baseWorld(t)
	st, err := w.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "yard"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 2}},
		Time:        3,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if st.Time != 3 || st.Characters[0].Location != "yard" {
		t.Fatalf("提交结果不符: %+v", st)
	}
	// 修改返回状态不得影响世界。
	st.Characters[0].Location = "cave"
	st.Rules.Version = "hacked"
	again := w.Snapshot()
	if again.Characters[0].Location != "yard" || again.Rules.Version != "v1" {
		t.Fatal("返回状态被修改后影响到了世界")
	}
}

func TestApplyRollback(t *testing.T) {
	cases := []struct {
		name string
		c    Commit
	}{
		{"时间倒退", Commit{Time: -1}},
		{"角色不存在", Commit{Moves: []Move{{Character: "ghost", To: "yard"}}, Time: 1}},
		{"地点不存在", Commit{Moves: []Move{{Character: "hero", To: "void"}}, Time: 1}},
		{"移动无连通", Commit{Moves: []Move{{Character: "hero", To: "cave"}}, Time: 1}},
		{"物品角色不存在", Commit{ItemChanges: []ItemChange{{Character: "x", Item: "gold"}}, Time: 1}},
		{"物品种类不存在", Commit{ItemChanges: []ItemChange{{Character: "hero", Item: "rock"}}, Time: 1}},
		{"数量变负", Commit{ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: -5}}, Time: 1}},
		{"超上限", Commit{
			ItemChanges: []ItemChange{{Character: "hero", Item: "key", Delta: 6}}, Time: 1,
		}},
		{"多项中一项非法", Commit{
			Moves:       []Move{{Character: "hero", To: "yard"}},
			ItemChanges: []ItemChange{{Character: "mage", Item: "gold", Delta: 9}},
			Time:        2,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := baseWorld(t)
			before := w.Snapshot()
			if _, err := w.Apply(tc.c); err == nil {
				t.Fatal("期望提交失败")
			} else if _, ok := err.(*RuleError); !ok {
				t.Fatalf("期望 *RuleError，得到 %T: %v", err, err)
			}
			after := w.Snapshot()
			if !statesEqual(before, after) {
				t.Fatal("失败提交改变了世界状态")
			}
		})
	}
}

func TestMoveAlongUndirectedEdge(t *testing.T) {
	w := baseWorld(t)
	// hero hall -> yard -> cave
	if _, err := w.Apply(Commit{Moves: []Move{{Character: "hero", To: "yard"}}, Time: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{Moves: []Move{{Character: "hero", To: "cave"}}, Time: 2}); err != nil {
		t.Fatal(err)
	}
	// 反向也允许。
	if _, err := w.Apply(Commit{Moves: []Move{{Character: "hero", To: "yard"}}, Time: 3}); err != nil {
		t.Fatal(err)
	}
}

func TestInputNotAliased(t *testing.T) {
	rules := baseRules()
	chars := []Character{{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}}}
	w, err := NewWorld(InitialData{Seed: 1, Rules: rules, Characters: chars})
	if err != nil {
		t.Fatal(err)
	}
	rules.Version = "mutated"
	chars[0].Location = "cave"
	if got := w.Snapshot().Rules.Version; got != "v1" {
		t.Fatalf("世界持有了入参引用: %s", got)
	}
	if got := w.Snapshot().Characters[0].Location; got != "hall" {
		t.Fatalf("世界持有了入参引用: %s", got)
	}
}

func statesEqual(a, b State) bool {
	if a.Seed != b.Seed || a.Time != b.Time || a.Rules.Version != b.Rules.Version {
		return false
	}
	if len(a.Characters) != len(b.Characters) {
		return false
	}
	for i := range a.Characters {
		ca, cb := a.Characters[i], b.Characters[i]
		if ca.ID != cb.ID || ca.Location != cb.Location || len(ca.Items) != len(cb.Items) {
			return false
		}
		for j := range ca.Items {
			if ca.Items[j] != cb.Items[j] {
				return false
			}
		}
	}
	return true
}
