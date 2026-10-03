package world

import "testing"

// findCharacter 按标识查找角色，找不到时测试失败。
func findCharacter(t *testing.T, st State, id string) Character {
	t.Helper()
	for _, ch := range st.Characters {
		if ch.ID == id {
			return ch
		}
	}
	t.Fatalf("状态中不存在角色 %q", id)
	return Character{}
}

// 同一次提交中的连续移动按给定次序逐步生效，每一步都从前一步结束后的
// 地点出发：地图只有 hall-yard、yard-cave 相连，hero 从 hall 出发在同一
// 提交中经 yard 到 cave 应当成功。成功后时间片等于提交指定的绝对值，
// 不因走了两步而额外增加；同次提交的合法物品增减照常生效。
func TestApplyMultiMoveThroughIntermediate(t *testing.T) {
	w := baseWorld(t)
	st, err := w.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "yard"},
			{Character: "hero", To: "cave"},
		},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 2}},
		Time:        5,
	})
	if err != nil {
		t.Fatalf("经中间地点的连续移动应成功: %v", err)
	}
	if st.Time != 5 {
		t.Fatalf("时间片应等于提交的绝对值 5，得到 %d", st.Time)
	}
	hero := findCharacter(t, st, "hero")
	if hero.Location != "cave" {
		t.Fatalf("hero 应位于 cave，得到 %q", hero.Location)
	}
	if len(hero.Items) != 1 || hero.Items[0] != (CharacterItem{Item: "gold", Count: 3}) {
		t.Fatalf("hero 物品应为 3 个 gold，得到 %+v", hero.Items)
	}
	if mage := findCharacter(t, st, "mage"); mage.Location != "yard" || len(mage.Items) != 0 {
		t.Fatalf("mage 不应受影响，得到 %+v", mage)
	}
	// 世界状态与返回状态一致。
	if got := findCharacter(t, w.Snapshot(), "hero"); got.Location != "cave" {
		t.Fatalf("世界中的 hero 应位于 cave，得到 %q", got.Location)
	}
}

// 角色已处于非零时间片时，允许在不推进时间的情况下于当前时间片内完成
// 合法的连续移动，不要求每一步分别推进时间。
func TestApplyMultiMoveWithinCurrentTimeSlice(t *testing.T) {
	w := baseWorld(t)
	if _, err := w.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "yard"}},
		Time:  4,
	}); err != nil {
		t.Fatalf("首次提交: %v", err)
	}
	st, err := w.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "cave"},
			{Character: "hero", To: "yard"},
		},
		Time: 4,
	})
	if err != nil {
		t.Fatalf("当前时间片内的连续移动应成功: %v", err)
	}
	if st.Time != 4 {
		t.Fatalf("时间片应保持 4，得到 %d", st.Time)
	}
	if got := findCharacter(t, st, "hero").Location; got != "yard" {
		t.Fatalf("hero 应回到 yard，得到 %q", got)
	}
}

// 沿相同的无向道路反向移动仍然允许：同一提交中按合法路线折返至起点
// 应当成功，中间地点不必留在最终状态里，角色与物品的排列保持原有约定。
func TestApplyMultiMoveRoundTrip(t *testing.T) {
	w := baseWorld(t)
	before := w.Snapshot()
	st, err := w.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "yard"},
			{Character: "hero", To: "cave"},
			{Character: "hero", To: "yard"},
			{Character: "hero", To: "hall"},
		},
		Time: 2,
	})
	if err != nil {
		t.Fatalf("折返起点的连续移动应成功: %v", err)
	}
	if st.Time != 2 {
		t.Fatalf("时间片应为 2，得到 %d", st.Time)
	}
	hero := findCharacter(t, st, "hero")
	if hero.Location != "hall" {
		t.Fatalf("hero 应回到 hall，得到 %q", hero.Location)
	}
	if len(hero.Items) != 1 || hero.Items[0] != (CharacterItem{Item: "gold", Count: 1}) {
		t.Fatalf("hero 物品应保持不变，得到 %+v", hero.Items)
	}
	if mage := findCharacter(t, st, "mage"); mage.Location != "yard" || len(mage.Items) != 0 {
		t.Fatalf("mage 不应受影响，得到 %+v", mage)
	}
	// 除时间片外，世界状态与提交前一致。
	after := w.Snapshot()
	after.Time = before.Time
	if !statesEqual(before, after) {
		t.Fatal("折返后除时间片外世界状态应与提交前一致")
	}
}

// 移动条目按次序逐步判定：目的地集合相同但次序非法时（第一步就没有
// 道路）必须失败，不能只根据起点和最终目的地判断是否允许。
func TestApplyMultiMoveOrderMatters(t *testing.T) {
	w := baseWorld(t)
	before := w.Snapshot()
	_, err := w.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "cave"},
			{Character: "hero", To: "yard"},
		},
		Time: 1,
	})
	mustRuleError(t, err, "hero", "hall", "cave")
	if after := w.Snapshot(); !statesEqual(before, after) {
		t.Fatal("次序非法的提交改变了世界状态")
	}
}

// 整段路线中最后一步没有道路时整次提交失败：hall→yard→cave 合法，但
// cave→hall 没有连通关系，即使最终地点与起点相同也不能算成功。失败后
// 角色位置、物品数量和时间片都与提交前一致，不留下部分移动结果。
func TestApplyMultiMoveRejectedLeavesNoTrace(t *testing.T) {
	w := baseWorld(t)
	before := w.Snapshot()
	_, err := w.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "yard"},
			{Character: "hero", To: "cave"},
			{Character: "hero", To: "hall"},
		},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 1}},
		Time:        3,
	})
	mustRuleError(t, err, "hero", "cave", "hall")
	after := w.Snapshot()
	if !statesEqual(before, after) {
		t.Fatalf("失败提交留下了部分结果: 前 %+v, 后 %+v", before, after)
	}
	if after.Time != 0 {
		t.Fatalf("时间片不应推进，得到 %d", after.Time)
	}
}

// 同次提交中路线全部合法，但物品增减使角色超过携带上限时整次失败，
// 前面的移动不能保留；之后仍能从提交前的位置按原规则继续操作。
func TestApplyMultiMoveItemOverflowRollsBack(t *testing.T) {
	w := baseWorld(t)
	before := w.Snapshot()
	_, err := w.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "yard"},
			{Character: "hero", To: "cave"},
		},
		ItemChanges: []ItemChange{{Character: "hero", Item: "key", Delta: 6}},
		Time:        2,
	})
	mustRuleError(t, err, "hero", "上限")
	if after := w.Snapshot(); !statesEqual(before, after) {
		t.Fatal("超上限的提交应整体回滚，但世界状态发生了变化")
	}
	// 仍能从提交前的位置继续操作。
	st, err := w.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "yard"}},
		Time:  1,
	})
	if err != nil {
		t.Fatalf("失败后应能从原位置继续移动: %v", err)
	}
	if got := findCharacter(t, st, "hero").Location; got != "yard" {
		t.Fatalf("hero 应从 hall 移动到 yard，得到 %q", got)
	}
}
