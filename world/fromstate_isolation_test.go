package world

import (
	"errors"
	"reflect"
	"testing"
)

// isolatedSourceRules 构造一份含多个地点、道路、物品种类与不同携带上限的
// 规则（其中 rogue 不设携带上限）。每次调用都返回全新切片/映射，避免各
// 用例互相串改。
func isolatedSourceRules() Rules {
	return Rules{
		Version:   "v1",
		Locations: []string{"hall", "yard", "cave"},
		Edges: []Edge{
			{From: "hall", To: "yard"},
			{From: "yard", To: "cave"},
		},
		ItemKinds:   []string{"gold", "key", "gem"},
		CarryLimits: map[string]int{"hero": 5, "mage": 2},
	}
}

func isolatedSourceState() State {
	return State{
		Seed:  1234,
		Time:  7,
		Rules: isolatedSourceRules(),
		Characters: []Character{
			// 多种物品、特定排列，含一个数量为零的条目（须原样保留）。
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: 0},
				{Item: "key", Count: 2},
				{Item: "gem", Count: 1},
			}},
			{ID: "mage", Location: "yard", Items: []CharacterItem{
				{Item: "gold", Count: 2},
			}},
			// 未设携带上限：允许其携带超过其他角色上限的总量。
			{ID: "rogue", Location: "cave", Items: []CharacterItem{
				{Item: "gold", Count: 40},
			}},
		},
	}
}

// assertStateDeepEqual 逐字段（含规则全部切片与携带上限映射、角色与物品
// 排列）断言两份完整状态完全一致。
func assertStateDeepEqual(t *testing.T, got, want State, ctx string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s 状态与来源不一致:\n got %+v\nwant %+v", ctx, got, want)
	}
}

// TestWorldFromStateRebuildsFullState 重建成功后，地图种子、非零时间片、
// 完整规则以及角色和物品的内容与排列都与来源逐字一致。
func TestWorldFromStateRebuildsFullState(t *testing.T) {
	src := isolatedSourceState()
	w, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("WorldFromState: %v", err)
	}
	assertStateDeepEqual(t, w.Snapshot(), src, "重建后")
}

// TestWorldFromStateSourceMutationIsolation 重建后再修改来源状态的地点列表、
// 道路端点、物品种类和携带上限，以及角色位置与携带物品的名称、数量，重建
// 出的世界仍保留创建时的数据，并继续按重建时的道路、允许物品和携带上限
// 判断提交成败：来源后来删除的道路仍可使用，后来提高的上限也不能让原本
// 超限的提交成功。
func TestWorldFromStateSourceMutationIsolation(t *testing.T) {
	src := isolatedSourceState()
	w, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("WorldFromState: %v", err)
	}

	// 大改来源状态：删地点、删/改道路、删物品、提高携带上限；改角色位置
	// 与物品的名称、数量。
	src.Rules.Locations = src.Rules.Locations[:1] // 只留 hall
	src.Rules.Edges[1] = Edge{From: "hall", To: "hall"}
	src.Rules.ItemKinds = src.Rules.ItemKinds[:1] // 只留 gold
	src.Rules.CarryLimits["hero"] = 1000
	src.Characters[0].Location = "cave"
	src.Characters[0].Items[0].Item = "gem"
	src.Characters[0].Items[0].Count = 9
	src.Characters[0].Items[1].Count = 9
	src.Characters[2].Items[0].Count = 999

	// 世界仍是创建时的完整内容。
	frozen := isolatedSourceState()
	assertStateDeepEqual(t, w.Snapshot(), frozen, "来源被改后")

	// 重建时保存的道路仍可走（来源后来删掉的 yard-cave 道路照常用）。
	if _, err := w.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "yard"},
			{Character: "hero", To: "cave"},
		},
		Time: 8,
	}); err != nil {
		t.Fatalf("应按重建时的道路允许移动: %v", err)
	}

	// 重建时保存的允许物品仍可增减（来源后来删掉的 key）。
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "key", Delta: 1}},
		Time:        9,
	}); err != nil {
		t.Fatalf("应按重建时允许的物品种类增减: %v", err)
	}

	// 来源后来把 hero 上限提高到 1000，但世界仍按重建时的 5 判断：
	// key 3 + gem 1 = 4，再加 2 个 gold（零数量条目仍在）到 6 即超限。
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 2}},
		Time:        10,
	}); err == nil {
		t.Fatal("提高来源上限不应让重建世界放过超限提交")
	} else {
		mustRuleError(t, err, "hero", "上限")
	}
	cur := w.Snapshot()
	if cur.Time != 9 {
		t.Fatalf("失败提交不应推进时间片，得到 %d", cur.Time)
	}
	hero := cur.Characters[0]
	if hero.Location != "cave" || !reflect.DeepEqual(hero.Items, []CharacterItem{
		{Item: "gold", Count: 0},
		{Item: "key", Count: 3},
		{Item: "gem", Count: 1},
	}) {
		t.Fatalf("失败提交应整体撤销: %+v", hero)
	}
}

// TestWorldFromStateTwoWorldsIndependent 从同一份合法状态连续重建两个世界：
// 在一个世界中成功移动、改变物品并推进时间，另一个世界和来源状态都保持
// 原来的完整内容。
func TestWorldFromStateTwoWorldsIndependent(t *testing.T) {
	src := isolatedSourceState()
	w1, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("WorldFromState w1: %v", err)
	}
	w2, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("WorldFromState w2: %v", err)
	}

	if _, err := w1.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "yard"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "key", Delta: -2}},
		Time:        12,
	}); err != nil {
		t.Fatalf("w1 提交失败: %v", err)
	}

	frozen := isolatedSourceState()
	assertStateDeepEqual(t, w2.Snapshot(), frozen, "另一个世界")
	assertStateDeepEqual(t, src, frozen, "来源状态")

	// w2 仍按自己的时间片与规则运行，且不知道 w1 的移动。
	if _, err := w2.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "yard"}},
		Time:  8,
	}); err != nil {
		t.Fatalf("w2 应独立提交: %v", err)
	}
	assertStateDeepEqual(t, src, frozen, "w2 提交后来源状态")
}

// TestWorldFromStateReturnedStatesAreDetached 调用方拿到重建世界的快照、或
// 一次成功提交返回的状态后再修改其中的道路、携带上限、角色和物品，不应
// 改变该世界后续的状态与规则判断。
func TestWorldFromStateReturnedStatesAreDetached(t *testing.T) {
	src := isolatedSourceState()
	w, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("WorldFromState: %v", err)
	}

	// 篡改 Snapshot 的返回值：改道路端点、提高携带上限、改角色与物品。
	snap := w.Snapshot()
	snap.Rules.Edges[0] = Edge{From: "hall", To: "cave"}
	snap.Rules.CarryLimits["hero"] = 1000
	snap.Characters[0].Location = "cave"
	snap.Characters[0].Items[1].Count = 99

	// 世界仍按内部保存的 hall-yard 道路与上限 5 判断：从 hall 不能直跳 cave。
	if _, err := w.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "cave"}},
		Time:  8,
	}); err == nil {
		t.Fatal("被篡改的快照不应让世界放行无道路移动")
	} else {
		mustRuleError(t, err, "连通关系")
	}

	// 成功提交返回的状态同样是独立副本；篡改它不影响后续提交。
	out, err := w.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "yard"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "key", Delta: 1}},
		Time:        9,
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	out.Rules.Edges = nil
	out.Rules.CarryLimits["hero"] = 1000
	out.Characters[0].Location = "cave"
	out.Characters[0].Items[2].Count = 50

	// 世界的道路仍在：yard 可回 hall；上限仍是 5，超限提交照样失败。
	if _, err := w.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "hall"}},
		Time:  10,
	}); err != nil {
		t.Fatalf("世界内部道路不应被返回状态的篡改影响: %v", err)
	}
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 1}},
		Time:        11,
	}); err != nil {
		t.Fatalf("key 3 + gem 1 + gold 1 = 5，恰好达上限应成功: %v", err)
	}
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 1}},
		Time:        12,
	}); err == nil {
		t.Fatal("返回状态里提高的上限不应作用于世界")
	} else {
		mustRuleError(t, err, "hero", "上限")
	}

	cur := w.Snapshot()
	if cur.Time != 11 || cur.Characters[0].Location != "hall" {
		t.Fatalf("世界状态被返回副本污染: time=%d char=%+v", cur.Time, cur.Characters[0])
	}
}

// TestWorldFromStateRejectedCommitLeavesEverythingUntouched 一次提交含合法
// 移动却因物品超出上限失败时，重建世界停在提交前，来源状态和另一个重建
// 世界都不受影响。
func TestWorldFromStateRejectedCommitLeavesEverythingUntouched(t *testing.T) {
	src := isolatedSourceState()
	w1, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("WorldFromState w1: %v", err)
	}
	w2, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("WorldFromState w2: %v", err)
	}
	before := w1.Snapshot()

	// 移动合法（hall->yard），但 hero 物品总量从 3 增到 0+2+5=7，超过
	// 上限 5。
	_, err = w1.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "yard"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gem", Delta: 4}},
		Time:        8,
	})
	if err == nil {
		t.Fatal("超限提交应当失败")
	}
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("期望 *RuleError，得到 %T: %v", err, err)
	}

	assertStateDeepEqual(t, w1.Snapshot(), before, "失败提交后重建世界")
	frozen := isolatedSourceState()
	assertStateDeepEqual(t, w2.Snapshot(), frozen, "另一个世界")
	assertStateDeepEqual(t, src, frozen, "来源状态")

	// 同一移动去掉超限增减后仍可成功，证明世界停在可用的提交前状态。
	if _, err := w1.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "yard"}},
		Time:  8,
	}); err != nil {
		t.Fatalf("失败提交后世界应停在提交前并可继续: %v", err)
	}
}

// TestWorldFromStatePreservesZeroCountEntry 角色带有数量为零的物品时，重建
// 后该条目仍保留在原位置，后续对它的增减照常生效。
func TestWorldFromStatePreservesZeroCountEntry(t *testing.T) {
	src := isolatedSourceState()
	w, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("WorldFromState: %v", err)
	}

	// 零数量的 gold 仍是第一个条目，没有被丢弃或挪位。
	hero := w.Snapshot().Characters[0]
	want := []CharacterItem{
		{Item: "gold", Count: 0},
		{Item: "key", Count: 2},
		{Item: "gem", Count: 1},
	}
	if !reflect.DeepEqual(hero.Items, want) {
		t.Fatalf("零数量条目未保留在原位置: %+v", hero.Items)
	}

	// 直接对零数量条目增减：加 2 后参与上限判断，再减回 0，条目始终在位。
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 2}},
		Time:        8,
	}); err != nil {
		t.Fatalf("对零数量条目增加失败: %v", err)
	}
	if got := w.Snapshot().Characters[0].Items[0]; got != (CharacterItem{Item: "gold", Count: 2}) {
		t.Fatalf("零数量条目增加后内容错误: %+v", got)
	}
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: -2}},
		Time:        9,
	}); err != nil {
		t.Fatalf("对该条目减少失败: %v", err)
	}
	if got := w.Snapshot().Characters[0].Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("减回零后条目应仍在原位置: %+v", got)
	}
	// 继续减到负数仍按原规则拒绝，世界不变。
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: -1}},
		Time:        10,
	}); err == nil {
		t.Fatal("零数量条目继续减到负数应失败")
	}
	if got := w.Snapshot().Characters[0].Items[0]; got.Count != 0 || got.Item != "gold" {
		t.Fatalf("失败提交改变了零数量条目: %+v", got)
	}
}

// TestWorldFromStateKeepsMissingCarryLimitUnbounded 规则没有为某角色设置
// 携带上限时，重建后该角色继续不受总量限制，可携带超过其他角色上限的
// 物品；而其他角色仍受各自上限约束。
func TestWorldFromStateKeepsMissingCarryLimitUnbounded(t *testing.T) {
	src := isolatedSourceState()
	w, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("WorldFromState: %v", err)
	}

	// rogue 没有 CarryLimits 条目：40 已超过 hero 的 5，再加大也合法。
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "rogue", Item: "gold", Delta: 100}},
		Time:        8,
	}); err != nil {
		t.Fatalf("未设上限的角色应允许携带超过其他角色上限: %v", err)
	}
	// 给它新增一种物品同样不受总量限制。
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "rogue", Item: "gem", Delta: 50}},
		Time:        9,
	}); err != nil {
		t.Fatalf("未设上限角色增加新物品失败: %v", err)
	}
	rogue := w.Snapshot().Characters[2]
	if !reflect.DeepEqual(rogue.Items, []CharacterItem{
		{Item: "gold", Count: 140},
		{Item: "gem", Count: 50},
	}) {
		t.Fatalf("无上限角色物品错误: %+v", rogue.Items)
	}

	// 对照：有上限的 mage（上限 2，已有 2）仍被拦截。
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "mage", Item: "gold", Delta: 1}},
		Time:        10,
	}); err == nil {
		t.Fatal("有上限角色不应被无上限角色连带放开")
	} else {
		mustRuleError(t, err, "mage", "上限")
	}
}
