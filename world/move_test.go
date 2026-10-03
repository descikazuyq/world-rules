package world

import "testing"

// 单次提交中同一角色的连续移动：每条移动都从前一条结束后的地点出发，
// 整段路线作为一次原子提交生效。

func TestApplyMultiStepMoveInOneCommit(t *testing.T) {
	w := baseWorld(t)
	// 地图只有 hall-yard、yard-cave 两条连通，hall 与 cave 无直达道路；
	// 同一提交中 hero 必须经由 yard 才能到达 cave。
	st, err := w.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "yard"},
			{Character: "hero", To: "cave"},
		},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 2}},
		Time:        7,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := st.Characters[0].Location; got != "cave" {
		t.Fatalf("连续移动后角色应位于 cave，得到 %q", got)
	}
	if got := st.Characters[0].Items[0]; got != (CharacterItem{Item: "gold", Count: 3}) {
		t.Fatalf("同次提交的物品增减未生效: %+v", got)
	}
	// 时间片等于提交指定的绝对值，走了两步也不额外增加。
	if st.Time != 7 {
		t.Fatalf("时间片应为提交指定的 7，得到 %d", st.Time)
	}
}

func TestApplyMultiStepMoveWithinSameTimeSlice(t *testing.T) {
	w := baseWorld(t)
	// 先把时间片推进到非零。
	if _, err := w.Apply(Commit{Time: 5}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// 已处于非零时间片时，允许在该时间片内完成连续移动，
	// 不要求每一步分别推进时间。
	st, err := w.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "yard"},
			{Character: "hero", To: "cave"},
		},
		Time: 5,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if st.Time != 5 || st.Characters[0].Location != "cave" {
		t.Fatalf("同一时间片内的连续移动结果不符: %+v", st)
	}
}

func TestApplyMultiStepMoveRoundTrip(t *testing.T) {
	w := baseWorld(t)
	// 沿相同道路反向移动仍允许：同一提交中按合法路线折返至起点，
	// 中间地点不必留在最终状态里。
	st, err := w.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "yard"},
			{Character: "hero", To: "hall"},
		},
		Time: 2,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := st.Characters[0].Location; got != "hall" {
		t.Fatalf("折返后角色应回到 hall，得到 %q", got)
	}
	// 角色和物品的排列保持原有约定。
	wantChars := []Character{
		{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		{ID: "mage", Location: "yard"},
	}
	if !statesEqual(State{Seed: 42, Rules: baseRules(), Time: 2, Characters: wantChars}, st) {
		t.Fatalf("折返后的状态排列不符: %+v", st)
	}
}

func TestApplyMultiStepMoveRollbackOnBrokenRoute(t *testing.T) {
	w := baseWorld(t)
	before := w.Snapshot()
	// hall -> yard -> cave 均合法，但 cave -> hall 没有道路；
	// 即使最终地点与起点相同，整段路线也必须被拒绝。
	_, err := w.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "yard"},
			{Character: "hero", To: "cave"},
			{Character: "hero", To: "hall"},
		},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 1}},
		Time:        3,
	})
	if err == nil {
		t.Fatal("期望整段路线被拒绝，但提交成功了")
	} else if _, ok := err.(*RuleError); !ok {
		t.Fatalf("期望 *RuleError，得到 %T: %v", err, err)
	}
	// 角色位置、物品数量和时间片都必须与提交前一致，
	// 前面已经合法经过的地点不能留下部分移动结果。
	if after := w.Snapshot(); !statesEqual(before, after) {
		t.Fatalf("失败提交留下了部分结果:\n之前: %+v\n之后: %+v", before, after)
	}
}

func TestApplyMultiStepMoveRollbackOnCarryOverflow(t *testing.T) {
	w := baseWorld(t)
	before := w.Snapshot()
	// 路线全部合法，但物品增减使 hero 超过携带上限 5，整次提交必须失败。
	_, err := w.Apply(Commit{
		Moves: []Move{
			{Character: "hero", To: "yard"},
			{Character: "hero", To: "cave"},
		},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 5}},
		Time:        3,
	})
	if err == nil {
		t.Fatal("期望超携带上限导致整次失败，但提交成功了")
	} else if _, ok := err.(*RuleError); !ok {
		t.Fatalf("期望 *RuleError，得到 %T: %v", err, err)
	}
	// 前面的移动不能保留下来。
	if after := w.Snapshot(); !statesEqual(before, after) {
		t.Fatalf("失败提交留下了部分移动结果:\n之前: %+v\n之后: %+v", before, after)
	}
	// 之后仍能从提交前的位置按原规则继续操作。
	st, err := w.Apply(Commit{Moves: []Move{{Character: "hero", To: "yard"}}, Time: 1})
	if err != nil {
		t.Fatalf("失败后应能从原位置继续操作: %v", err)
	}
	if got := st.Characters[0].Location; got != "yard" {
		t.Fatalf("继续操作后角色应位于 yard，得到 %q", got)
	}
}
