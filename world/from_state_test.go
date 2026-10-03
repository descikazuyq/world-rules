package world

import (
	"reflect"
	"testing"
)

// isolationRules 是多层可修改内容齐全的规则：三个地点、两条道路、两种
// 物品，并只为部分角色设置携带上限（rogue 不设上限）。
func isolationRules() Rules {
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

// isolationState 构造一份带非零时间片、多角色多物品且排列固定的完整状态。
// hero 携带 gold=1、key=3（总量 4，贴在 5 的上限内），mage 携带 gold=2
// （恰好等于上限 2），另有一个规则未设携带上限的 rogue。
func isolationState() State {
	return State{
		Seed:  4242,
		Rules: isolationRules(),
		Time:  7,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: 1},
				{Item: "key", Count: 3},
			}},
			{ID: "mage", Location: "yard", Items: []CharacterItem{
				{Item: "gold", Count: 2},
			}},
			{ID: "rogue", Location: "cave", Items: []CharacterItem{
				{Item: "gold", Count: 100},
			}},
		},
	}
}

// TestWorldFromStateCopiesFullContents 重建成功后，地图种子、非零时间片、
// 完整规则以及角色和物品的内容与排列都与来源一致。
func TestWorldFromStateCopiesFullContents(t *testing.T) {
	src := isolationState()
	w, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("WorldFromState: %v", err)
	}
	got := w.Snapshot()

	if got.Seed != 4242 || got.Time != 7 {
		t.Fatalf("种子/时间片未保留: %+v", got)
	}
	if got.Rules.Version != "v1" {
		t.Fatalf("规则版本错误: %q", got.Rules.Version)
	}
	if !reflect.DeepEqual(got.Rules.Locations, []string{"hall", "yard", "cave"}) {
		t.Fatalf("地点内容/排列错误: %v", got.Rules.Locations)
	}
	if !reflect.DeepEqual(got.Rules.Edges, []Edge{
		{From: "hall", To: "yard"},
		{From: "yard", To: "cave"},
	}) {
		t.Fatalf("道路内容/排列错误: %+v", got.Rules.Edges)
	}
	if !reflect.DeepEqual(got.Rules.ItemKinds, []string{"gold", "key"}) {
		t.Fatalf("物品种类/排列错误: %v", got.Rules.ItemKinds)
	}
	if !reflect.DeepEqual(got.Rules.CarryLimits, map[string]int{"hero": 5, "mage": 2}) {
		t.Fatalf("携带上限错误: %+v", got.Rules.CarryLimits)
	}
	wantChars := src.Characters
	if !reflect.DeepEqual(got.Characters, wantChars) {
		t.Fatalf("角色/物品的内容与排列未保留:\n got %+v\nwant %+v", got.Characters, wantChars)
	}
}

// TestWorldFromStateSourceMutationsIsolated 调用方把读档状态交给
// WorldFromState 后，再去修改来源里规则的地点列表、道路端点、物品种类、
// 携带上限，以及角色位置与携带物品的名称、数量，都不应改变重建出的世界；
// 世界继续按重建时保存的道路、允许物品和携带上限判断提交成败。
func TestWorldFromStateSourceMutationsIsolated(t *testing.T) {
	src := isolationState()
	w, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("WorldFromState: %v", err)
	}

	// 改动来源：删掉一个地点、删掉重建世界仍需要的道路 yard-cave、删掉
	// key 种类、把 hero 上限提到 9，并改角色位置与物品名称/数量。
	src.Rules.Locations = src.Rules.Locations[:2]
	src.Rules.Edges[1] = Edge{From: "hall", To: "hall"} // 去掉 yard-cave
	src.Rules.ItemKinds = src.Rules.ItemKinds[:1]       // 去掉 key
	src.Rules.CarryLimits["hero"] = 9
	src.Characters[0].Location = "cave"
	src.Characters[0].Items[0].Item = "stone"
	src.Characters[0].Items[0].Count = 88
	src.Characters[0].Items[1].Count = 88

	// 世界快照仍保留创建时的完整内容。
	got := w.Snapshot()
	if !reflect.DeepEqual(got.Rules, isolationRules()) {
		t.Fatalf("来源的规则修改泄漏进世界: %+v", got.Rules)
	}
	if !reflect.DeepEqual(got.Characters, isolationState().Characters) {
		t.Fatalf("来源的角色/物品修改泄漏进世界: %+v", got.Characters)
	}

	// 来源已删除的道路 yard-cave 在重建世界中仍可使用（hero 在 hall，
	// 先 hall->yard 再 yard->cave），且时间从重建时的 7 向前推进。
	if _, err := w.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "yard"}},
		Time:  8,
	}); err != nil {
		t.Fatalf("重建世界应保留 hall-yard 道路: %v", err)
	}
	if _, err := w.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "cave"}},
		Time:  9,
	}); err != nil {
		t.Fatalf("来源删除的 yard-cave 道路仍应可用: %v", err)
	}

	// 来源已删除的物品种类 key 在世界中仍被允许。
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "key", Delta: 1}},
		Time:        10,
	}); err != nil {
		t.Fatalf("重建世界应继续允许来源已删除的物品种类 key: %v", err)
	}

	// hero 当前总量 5（1 gold + 4 key），重建时上限为 5：再加 1 个 gold
	// 总量到 6 必须失败，即便来源把上限提高到了 9。
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 1}},
		Time:        11,
	}); err == nil {
		t.Fatal("来源提高携带上限后，原本超限的提交不应成功")
	} else {
		mustRuleError(t, err, "hero", "上限")
	}
	// 失败提交未推动时间，世界仍停在时间片 10、hero 仍在 cave。
	snap := w.Snapshot()
	if snap.Time != 10 || snap.Characters[0].Location != "cave" {
		t.Fatalf("被上限拒绝后世界状态异常: %+v", snap)
	}

	// 移动合法性同样按重建时的地点集合判断：来源删掉 cave 不影响世界。
	if _, err := w.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "yard"}},
		Time:  11,
	}); err != nil {
		t.Fatalf("重建世界应保留 cave 地点: %v", err)
	}
}

// TestWorldFromStateTwoRebuildsIndependent 从同一份合法状态连续重建两个
// 世界：在其中一个里成功移动、改变物品并推进时间，另一个世界以及来源
// 状态都保持原来的完整内容。
func TestWorldFromStateTwoRebuildsIndependent(t *testing.T) {
	src := isolationState()
	original := isolationState()

	w1, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("重建 w1 失败: %v", err)
	}
	w2, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("重建 w2 失败: %v", err)
	}

	// 在 w1 中移动、增减物品并推进时间。hero 起始总量 4（gold1+key3），
	// gold +1 后总量恰好贴上重建时的上限 5。
	if _, err := w1.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "yard"},
			{Character: "hero", To: "cave"},
		},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 1}},
		Time:        12,
	}); err != nil {
		t.Fatalf("w1 提交失败: %v", err)
	}
	s1 := w1.Snapshot()
	if s1.Time != 12 || s1.Characters[0].Location != "cave" {
		t.Fatalf("w1 提交结果错误: %+v", s1)
	}
	if got := s1.Characters[0].Items[0]; got != (CharacterItem{Item: "gold", Count: 2}) {
		t.Fatalf("w1 物品数量错误: %+v", got)
	}

	// w2 仍是重建时的完整内容。
	s2 := w2.Snapshot()
	if !reflect.DeepEqual(s2, original) {
		t.Fatalf("w2 受 w1 操作影响:\n got %+v\nwant %+v", s2, original)
	}
	// 来源状态同样纹丝不动。
	if !reflect.DeepEqual(src, original) {
		t.Fatalf("来源状态受重建世界操作影响:\n got %+v\nwant %+v", src, original)
	}

	// w2 仍可独立地按自己的时间片（7）继续：倒退到 w1 的更早目标应失败，
	// 而沿自己保存的道路移动成功。
	if _, err := w2.Apply(Commit{Time: 6}); err == nil {
		t.Fatal("w2 不应受 w1 影响而允许时间倒退")
	}
	if _, err := w2.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "yard"}},
		Time:  13,
	}); err != nil {
		t.Fatalf("w2 独立移动失败: %v", err)
	}
}

// TestWorldFromStateReturnedStatesAreDetached 调用方拿到重建世界的快照、或
// 一次成功提交返回的状态后，即使深度修改其中的道路、携带上限、角色和
// 物品，也不改变该世界后续的状态和规则判断。
func TestWorldFromStateReturnedStatesAreDetached(t *testing.T) {
	src := isolationState()
	w, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("WorldFromState: %v", err)
	}

	// 1) 篡改 Snapshot 返回值。
	snap := w.Snapshot()
	snap.Rules.Edges = nil
	snap.Rules.CarryLimits["hero"] = 1000
	snap.Rules.Locations = append(snap.Rules.Locations, "moon")
	snap.Rules.ItemKinds = append(snap.Rules.ItemKinds, "stone")
	snap.Characters[0].Location = "cave"
	snap.Characters[0].Items[0].Count = 999

	// 2) 做一次成功提交并篡改其返回状态：返回状态声称 hero 已在 cave 且
	// gold 很多、上限很高。
	out, err := w.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "yard"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "key", Delta: 1}},
		Time:        8,
	})
	if err != nil {
		t.Fatalf("成功提交失败: %v", err)
	}
	out.Rules.Edges = nil
	out.Rules.CarryLimits["hero"] = 1000
	out.Characters[0].Location = "hall"
	out.Characters[0].Items[1].Count = 99

	// 世界内部仍以提交后的真实状态为准：hero 在 yard（不是返回状态里的
	// hall），gold=1 key=4 总量 5 贴上 hero 上限 5。
	cur := w.Snapshot()
	if cur.Time != 8 || cur.Characters[0].Location != "yard" {
		t.Fatalf("世界内部状态被返回值篡改污染: %+v", cur)
	}
	if !reflect.DeepEqual(cur.Characters[0].Items, []CharacterItem{
		{Item: "gold", Count: 1},
		{Item: "key", Count: 4},
	}) {
		t.Fatalf("世界内部物品被返回值篡改: %+v", cur.Characters[0].Items)
	}
	if !reflect.DeepEqual(cur.Rules, isolationRules()) {
		t.Fatalf("世界内部规则被返回值篡改: %+v", cur.Rules)
	}

	// 后续规则判断仍按世界内部的道路与上限：
	// yard->cave 道路仍在（返回值把 Edges 置空不应生效），移动成功。
	if _, err := w.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "cave"}},
		Time:  9,
	}); err != nil {
		t.Fatalf("世界内部道路应仍可用: %v", err)
	}
	// 上限仍为 5，当前总量 5：再 +1 gold 到 6 必须失败（返回值声称 1000
	// 不应生效）。
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 1}},
		Time:        10,
	}); err == nil {
		t.Fatal("世界应仍按原携带上限拒绝超限提交")
	} else {
		mustRuleError(t, err, "hero", "上限")
	}
	// 被拒绝的提交连时间都不推进。
	if got := w.Snapshot().Time; got != 9 {
		t.Fatalf("超限失败后时间不应推进，得到 %d", got)
	}
}

// TestWorldFromStateFailedCommitLeavesAllUntouched 一次提交包含合法移动，
// 却因物品超出携带上限而整体失败：重建世界停在提交前，来源状态与从同一
// 状态重建的另一个世界都不受影响。
func TestWorldFromStateFailedCommitLeavesAllUntouched(t *testing.T) {
	src := isolationState()
	original := isolationState()
	w1, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("重建 w1 失败: %v", err)
	}
	w2, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("重建 w2 失败: %v", err)
	}
	before := w1.Snapshot()

	// hero 在 hall，hall->yard 是合法移动；但 gold 1->7（总量 10）超过
	// hero 上限 5，整次提交必须失败。
	if _, err := w1.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "yard"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 6}},
		Time:        8,
	}); err == nil {
		t.Fatal("合法移动 + 超限物品的提交应整体失败")
	} else {
		mustRuleError(t, err, "hero", "上限")
	}

	// w1 完全停在提交前（位置、物品、时间都不变）。
	if after := w1.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("失败提交改变了重建世界:\n before %+v\n after  %+v", before, after)
	}
	// 失败后仍可在提交前状态上重试一次合法提交，证明世界没有被污染。
	if _, err := w1.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "yard"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 1}},
		Time:        8,
	}); err != nil {
		t.Fatalf("回滚后重试合法提交应成功: %v", err)
	}
	// w2 与来源状态仍是原始完整内容。
	if got := w2.Snapshot(); !reflect.DeepEqual(got, original) {
		t.Fatalf("另一个重建世界受失败提交影响: %+v", got)
	}
	if !reflect.DeepEqual(src, original) {
		t.Fatalf("来源状态受失败提交影响: %+v", src)
	}
}

// TestWorldFromStateKeepsZeroQuantityItem 角色带有数量为零的物品是合法
// 状态：重建后该条目仍保留在原位置（既不丢失也不重排），后续增减照常
// 生效。
func TestWorldFromStateKeepsZeroQuantityItem(t *testing.T) {
	src := State{
		Seed:  5,
		Rules: isolationRules(),
		Time:  3,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: 0},
				{Item: "key", Count: 2},
			}},
			{ID: "mage", Location: "yard"},
		},
	}
	w, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("含零数量物品的状态重建不应失败: %v", err)
	}
	got := w.Snapshot()
	want := []CharacterItem{{Item: "gold", Count: 0}, {Item: "key", Count: 2}}
	if !reflect.DeepEqual(got.Characters[0].Items, want) {
		t.Fatalf("零数量条目应保留在原位置: %+v", got.Characters[0].Items)
	}

	// 对零数量条目增减照常生效：+3 后位于原位且数量为 3。
	out, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 3}},
		Time:        4,
	})
	if err != nil {
		t.Fatalf("对零数量物品增加应成功: %v", err)
	}
	if !reflect.DeepEqual(out.Characters[0].Items, []CharacterItem{
		{Item: "gold", Count: 3},
		{Item: "key", Count: 2},
	}) {
		t.Fatalf("增减后物品内容/排列错误: %+v", out.Characters[0].Items)
	}
	// 再减回 0：条目仍保留，不被移除。
	out2, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: -3}},
		Time:        5,
	})
	if err != nil {
		t.Fatalf("减回零应成功: %v", err)
	}
	if !reflect.DeepEqual(out2.Characters[0].Items, want) {
		t.Fatalf("减回零后条目应保留在原位置: %+v", out2.Characters[0].Items)
	}
	// 继续减到负数则按既有规则失败并整体回滚。
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: -1}},
		Time:        6,
	}); err == nil {
		t.Fatal("零数量再减应失败")
	} else {
		mustRuleError(t, err, "hero", "gold")
	}
	if cur := w.Snapshot(); !reflect.DeepEqual(cur.Characters[0].Items, want) || cur.Time != 5 {
		t.Fatalf("失败提交后状态异常: %+v", cur)
	}
}

// TestWorldFromStateCharacterWithoutCarryLimit 规则没有为某角色设置携带
// 上限时，即使其携带总量超过其他角色的上限，重建也应成功并继续允许其
// 携带这么多（以及继续增加）；而设了上限的角色仍照常受限。
func TestWorldFromStateCharacterWithoutCarryLimit(t *testing.T) {
	src := State{
		Seed:  9,
		Rules: isolationRules(), // hero 上限 5、mage 上限 2、rogue 无上限
		Time:  1,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 5}}},
			{ID: "rogue", Location: "cave", Items: []CharacterItem{
				{Item: "gold", Count: 50},
				{Item: "key", Count: 50},
			}},
		},
	}
	w, err := WorldFromState(src)
	if err != nil {
		t.Fatalf("无上限角色携带大量物品应允许重建: %v", err)
	}

	// rogue 总量 100，远超 hero 的上限 5，但重建后继续增加仍被允许。
	out, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "rogue", Item: "gold", Delta: 100}},
		Time:        2,
	})
	if err != nil {
		t.Fatalf("无上限角色应能继续增加物品: %v", err)
	}
	if !reflect.DeepEqual(out.Characters[1].Items, []CharacterItem{
		{Item: "gold", Count: 150},
		{Item: "key", Count: 50},
	}) {
		t.Fatalf("无上限角色物品数量错误: %+v", out.Characters[1].Items)
	}

	// 设了上限的 hero 仍照常受限：5->6 必须失败。
	if _, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 1}},
		Time:        3,
	}); err == nil {
		t.Fatal("有上限的 hero 超限应失败")
	} else {
		mustRuleError(t, err, "hero", "上限")
	}
	if cur := w.Snapshot(); cur.Time != 2 || cur.Characters[0].Items[0].Count != 5 {
		t.Fatalf("hero 失败提交不应改变世界: %+v", cur)
	}
}
