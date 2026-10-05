package world

import (
	"reflect"
	"testing"
)

// itemOrderRules 是含三个地点、四种物品的规则。四种物品让每个角色都可能
// 获得多个原先没有的新种类，从而区分“按首次出现追加”与“按名称排序”。
func itemOrderRules() Rules {
	return Rules{
		Version:   "v1",
		Locations: []string{"hall", "yard", "cave"},
		Edges: []Edge{
			{From: "hall", To: "yard"},
			{From: "yard", To: "cave"},
		},
		ItemKinds:   []string{"gold", "key", "gem", "orb"},
		CarryLimits: map[string]int{"hero": 30, "mage": 30},
	}
}

// itemOrderWorld 建立两个角色、物品列表刻意不按名称排序且含零数量条目的
// 世界：hero 为 key(1)、gold(0)，mage 为 gem(1)、key(0)。hero 可新增的
// 种类是 orb、gem；mage 可新增的是 orb、gold，两边的新增次序互相独立。
func itemOrderWorld(t *testing.T) *World {
	t.Helper()
	w, err := NewWorld(InitialData{
		Seed:  7,
		Rules: itemOrderRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "key", Count: 1},
				{Item: "gold", Count: 0},
			}},
			{ID: "mage", Location: "yard", Items: []CharacterItem{
				{Item: "gem", Count: 1},
				{Item: "key", Count: 0},
			}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	return w
}

// wantItems 按 (物品, 数量) 对校验物品列表的完整排列：长度、名称次序与
// 数量都必须逐位相同，不能只比总量。
func wantItems(t *testing.T, got []CharacterItem, want ...CharacterItem) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("物品列表长度不符: 得 %+v，想 %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("物品列表第 %d 项不符: 得 %+v，想 %+v（完整得 %+v）", i, got[i], want[i], got)
		}
	}
}

// 已有条目保持原位置、未涉及条目原样不动；新增种类只追加一个条目，并按
// 它们在该角色增减条目中首次出现的先后排列（orb 先于 gem，与名称次序
// 相反）；已有物品减到零仍留在原位置；先增后减、最终为零的新物品（orb）
// 也要追加并保留，不得因净变化为零而省略；同一种新增物品（gem）多次出现
// 不得产生重复条目，也不得移到最后一次出现的位置。
func TestApplyItemListOrderAndZeroEntries(t *testing.T) {
	w := itemOrderWorld(t)
	st, err := w.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "key", Delta: 2},  // 已有：1 -> 3，位置不动
			{Character: "hero", Item: "orb", Delta: 1},  // 新种类首次出现（orb 先）
			{Character: "hero", Item: "orb", Delta: -1}, // 先增后减，净 0，仍保留
			{Character: "hero", Item: "gem", Delta: 5},  // 新种类首次出现（gem 后）
			{Character: "hero", Item: "gem", Delta: -2}, // 同一种新增物品重复出现
			{Character: "hero", Item: "gem", Delta: 1},
		},
		Time: 1,
	})
	if err != nil {
		t.Fatalf("合法提交应成功: %v", err)
	}
	hero := findCharacter(t, st, "hero")
	wantItems(t, hero.Items,
		CharacterItem{Item: "key", Count: 3},
		CharacterItem{Item: "gold", Count: 0},
		CharacterItem{Item: "orb", Count: 0},
		CharacterItem{Item: "gem", Count: 4},
	)
	// 返回状态与世界当前快照的数量、排列一致，而不只是总量相同。
	snapHero := findCharacter(t, w.Snapshot(), "hero")
	if !reflect.DeepEqual(hero.Items, snapHero.Items) {
		t.Fatalf("返回状态与世界快照的物品列表不一致: %+v vs %+v", hero.Items, snapHero.Items)
	}
}

// 同一次提交中不同角色的增减条目交错出现且涉及相同物品名称时，每个角色
// 独立承接自己的原列表与新增次序：hero 先见 gem 再见 orb，mage 先见 orb
// 再见 gold（两者都与按名称排序相反，也与跨角色的全局首次出现次序相反）；
// 不能把另一角色的首次出现次序套用过来，角色本身的排列也保持不变。
func TestApplyInterleavedCharactersKeepOwnOrder(t *testing.T) {
	w := itemOrderWorld(t)
	st, err := w.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "gold", Delta: 3}, // hero 已有零条目：0 -> 3
			{Character: "mage", Item: "orb", Delta: 4},  // mage 新种类 1：orb
			{Character: "hero", Item: "gem", Delta: 3},  // hero 新种类 1：gem
			{Character: "mage", Item: "gem", Delta: -1}, // mage 已有：1 -> 0，留在原位
			{Character: "mage", Item: "gold", Delta: 1}, // mage 新种类 2：gold
			{Character: "hero", Item: "orb", Delta: 2},  // hero 新种类 2：orb
			{Character: "hero", Item: "key", Delta: -1}, // hero 已有：1 -> 0，留在原位
			{Character: "mage", Item: "gold", Delta: -1}, // 新物品净变化为 0，仍保留
			{Character: "hero", Item: "gem", Delta: 1},  // 重复出现：不新增条目、不移位
			{Character: "hero", Item: "gem", Delta: -1},
		},
		Time: 2,
	})
	if err != nil {
		t.Fatalf("合法提交应成功: %v", err)
	}
	if len(st.Characters) != 2 || st.Characters[0].ID != "hero" || st.Characters[1].ID != "mage" {
		t.Fatalf("角色排列不应改变: %+v", st.Characters)
	}
	hero := findCharacter(t, st, "hero")
	wantItems(t, hero.Items,
		CharacterItem{Item: "key", Count: 0},
		CharacterItem{Item: "gold", Count: 3},
		CharacterItem{Item: "gem", Count: 3},
		CharacterItem{Item: "orb", Count: 2},
	)
	mage := findCharacter(t, st, "mage")
	wantItems(t, mage.Items,
		CharacterItem{Item: "gem", Count: 0},
		CharacterItem{Item: "key", Count: 0},
		CharacterItem{Item: "orb", Count: 4},
		CharacterItem{Item: "gold", Count: 0},
	)
	snap := w.Snapshot()
	snapHero := findCharacter(t, snap, "hero")
	snapMage := findCharacter(t, snap, "mage")
	if !reflect.DeepEqual(hero.Items, snapHero.Items) || !reflect.DeepEqual(mage.Items, snapMage.Items) {
		t.Fatalf("返回状态与世界快照不一致: hero %+v vs %+v; mage %+v vs %+v",
			hero.Items, snapHero.Items, mage.Items, snapMage.Items)
	}
}

// 最终数量合法的前提下，只改变同一物品增减条目的次序不改变最终数量与
// 列表排列；若同时改变了不同新增种类第一次出现的先后，新增条目的列表
// 次序应随之改变。
func TestApplyDeltaOrderVersusFirstSeenOrder(t *testing.T) {
	// 同一物品的增减条目重排（两个新种类都是 orb 先首次出现）：
	// 数量与排列都不变，且中途暂时为负（gem 先 -1）仍成功。
	first := []ItemChange{
		{Character: "hero", Item: "orb", Delta: 3},
		{Character: "hero", Item: "key", Delta: 3},
		{Character: "hero", Item: "gem", Delta: -1},
		{Character: "hero", Item: "gem", Delta: 3},
	}
	second := []ItemChange{
		{Character: "hero", Item: "orb", Delta: 3},
		{Character: "hero", Item: "gem", Delta: 3},
		{Character: "hero", Item: "key", Delta: 3},
		{Character: "hero", Item: "gem", Delta: -1},
	}
	want := []CharacterItem{
		{Item: "key", Count: 4},
		{Item: "gold", Count: 0},
		{Item: "orb", Count: 3},
		{Item: "gem", Count: 2},
	}
	for i, changes := range [][]ItemChange{first, second} {
		w := itemOrderWorld(t)
		st, err := w.Apply(Commit{ItemChanges: changes, Time: 1})
		if err != nil {
			t.Fatalf("第 %d 个提交应成功: %v", i, err)
		}
		got := findCharacter(t, st, "hero").Items
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("仅重排同一物品增减条目时结果应不变: 得 %+v，想 %+v", got, want)
		}
	}

	// 同样的增减集合，只改变两个新增种类第一次出现的先后（gem 先）：
	// 最终数量不变，但新增条目次序应变为 gem 在前、orb 在后。
	w := itemOrderWorld(t)
	st, err := w.Apply(Commit{ItemChanges: []ItemChange{
		{Character: "hero", Item: "gem", Delta: -1},
		{Character: "hero", Item: "orb", Delta: 3},
		{Character: "hero", Item: "key", Delta: 3},
		{Character: "hero", Item: "gem", Delta: 3},
	}, Time: 1})
	if err != nil {
		t.Fatalf("gem 首次先出现的提交应成功: %v", err)
	}
	wantItems(t, findCharacter(t, st, "hero").Items,
		CharacterItem{Item: "key", Count: 4},
		CharacterItem{Item: "gold", Count: 0},
		CharacterItem{Item: "gem", Count: 2},
		CharacterItem{Item: "orb", Count: 3},
	)
}

// 一次提交同时含合法移动、新增物品和已有物品增减，但某个角色最终数量为
// 负时整次提交失败并返回 *RuleError；同次提交中其他角色也不能留下新条目、
// 归零结果或次序变化，所有角色的位置、物品列表和时间片都保持提交前内容。
func TestApplyNegativeFinalLeavesNoListChange(t *testing.T) {
	w := itemOrderWorld(t)
	before := w.Snapshot()
	_, err := w.Apply(Commit{
		Moves: []Move{{Character: "mage", To: "cave"}},
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "gem", Delta: 4},  // hero 新增合法物品
			{Character: "mage", Item: "gold", Delta: 2}, // mage 新增合法物品
			{Character: "hero", Item: "key", Delta: 1},
			{Character: "mage", Item: "gem", Delta: -2}, // mage: 1 - 2 = -1，非法
		},
		Time: 5,
	})
	mustRuleError(t, err, "mage", "gem", "负")
	after := w.Snapshot()
	if !statesEqual(before, after) {
		t.Fatalf("失败提交留下列表变化: 前 %+v，后 %+v", before.Characters, after.Characters)
	}
	if after.Time != 0 {
		t.Fatalf("时间片不应推进，得到 %d", after.Time)
	}
	// 明确核对其他角色没有留下新条目或归零结果。
	wantItems(t, findCharacter(t, after, "hero").Items,
		CharacterItem{Item: "key", Count: 1},
		CharacterItem{Item: "gold", Count: 0},
	)
	mage := findCharacter(t, after, "mage")
	if mage.Location != "yard" {
		t.Fatalf("mage 的移动也应撤销，得到 %q", mage.Location)
	}
	wantItems(t, mage.Items,
		CharacterItem{Item: "gem", Count: 1},
		CharacterItem{Item: "key", Count: 0},
	)
}

// 同上，但失败原因是某个角色最终携带总量超过上限：其他角色在同次提交中的
// 新条目、数量变化与排列变化同样必须全部撤销。
func TestApplyCarryOverflowLeavesNoListChange(t *testing.T) {
	rules := itemOrderRules()
	rules.CarryLimits = map[string]int{"hero": 10, "mage": 3}
	w, err := NewWorld(InitialData{
		Seed:  7,
		Rules: rules,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "key", Count: 2},
				{Item: "gold", Count: 0},
			}},
			{ID: "mage", Location: "yard", Items: []CharacterItem{
				{Item: "gem", Count: 1},
				{Item: "key", Count: 0},
			}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	before := w.Snapshot()
	_, err = w.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "yard"}},
		ItemChanges: []ItemChange{
			{Character: "mage", Item: "gold", Delta: 1}, // mage 新条目，最终总量 3，合法
			{Character: "mage", Item: "key", Delta: 1},
			{Character: "hero", Item: "gem", Delta: 11}, // hero 最终: 0+0+11 = 11 > 10
			{Character: "hero", Item: "key", Delta: -2}, // hero 已有条目归零
		},
		Time: 6,
	})
	mustRuleError(t, err, "hero", "上限")
	after := w.Snapshot()
	if !statesEqual(before, after) {
		t.Fatalf("失败提交留下列表变化: 前 %+v，后 %+v", before.Characters, after.Characters)
	}
	if after.Time != 0 {
		t.Fatalf("时间片不应推进，得到 %d", after.Time)
	}
	hero := findCharacter(t, after, "hero")
	if hero.Location != "hall" {
		t.Fatalf("hero 的移动应撤销，得到 %q", hero.Location)
	}
	wantItems(t, hero.Items,
		CharacterItem{Item: "key", Count: 2},
		CharacterItem{Item: "gold", Count: 0},
	)
	wantItems(t, findCharacter(t, after, "mage").Items,
		CharacterItem{Item: "gem", Count: 1},
		CharacterItem{Item: "key", Count: 0},
	)
}
