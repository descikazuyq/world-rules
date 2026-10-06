package world_test

import (
	"errors"
	"fmt"
	"log"

	"github.com/descikazuyq/world-rules/world"
)

// multiDeltaWorld 建立示例世界：hero 在 village 持有一件木材，village 与
// forest 之间有道路，规则只允许 wood，hero 携带上限 4（恰好容纳最终的
// 四件木材）。每次调用都返回相同初始状态的全新世界。
func multiDeltaWorld() *world.World {
	w, err := world.NewWorld(world.InitialData{
		Seed: 20240801,
		Rules: world.Rules{
			Version:     "v1",
			Locations:   []string{"village", "forest"},
			Edges:       []world.Edge{{From: "village", To: "forest"}},
			ItemKinds:   []string{"wood"},
			CarryLimits: map[string]int{"hero": 4},
		},
		Characters: []world.Character{{
			ID:       "hero",
			Location: "village",
			Items:    []world.CharacterItem{{Item: "wood", Count: 1}},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	return w
}

// Example_sameItemMultiDelta 演示同一次提交中对同一种物品列出多条增减：
// 判断依据是原有数量与本次全部增减合并后的最终结果，与条目先后次序无关；
// 合并结果为负则整次提交失败，世界保持提交前状态。
func Example_sameItemMultiDelta() {
	// 1) 减少排在前：wood -2 再 +5。若逐条落数，中途会出现 -1 的负数量；
	//    实际判断先把全部增减与原数量合并：1 - 2 + 5 = 4，合法（4 等于上限，
	//    允许）。不会存在一个可被读取的负数量世界。
	w1 := multiDeltaWorld()
	printContinueState("初始", w1.Snapshot())
	if _, err := w1.Apply(world.Commit{
		Moves: []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{
			{Character: "hero", Item: "wood", Delta: -2},
			{Character: "hero", Item: "wood", Delta: 5},
		},
		Time: 2, // 目标绝对时间片，不是增加的步数
	}); err != nil {
		log.Fatal(err)
	}
	printContinueState("减少在前(-2,+5)", w1.Snapshot())

	// 2) 相同初始状态，交换两条增减的次序：合并结果同为 1 + 5 - 2 = 4，
	//    成功结果完全相同。
	w2 := multiDeltaWorld()
	if _, err := w2.Apply(world.Commit{
		Moves: []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{
			{Character: "hero", Item: "wood", Delta: 5},
			{Character: "hero", Item: "wood", Delta: -2},
		},
		Time: 2,
	}); err != nil {
		log.Fatal(err)
	}
	printContinueState("增加在前(+5,-2)", w2.Snapshot())

	// 3) 相同初始状态，只提交减少两件（移动与时间目标同上）：1 - 2 = -1，
	//    最终数量为负，整次提交失败并返回 *world.RuleError；位置、数量与
	//    时间片都保持提交前状态。这也正是把 -2/+5 拆成两次 Apply 时第一步
	//    的结果：第一步失败后，事后的 +5 是另一次提交，无法把已失败的
	//    第一步补成一次成功提交。
	w3 := multiDeltaWorld()
	_, err := w3.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "wood", Delta: -2}},
		Time:        2,
	})
	var re *world.RuleError
	if !errors.As(err, &re) {
		log.Fatalf("应返回 *world.RuleError，得到 %v", err)
	}
	fmt.Printf("只减两件: 错误类型=%T\n", err)
	fmt.Println("只减两件:", err)
	printContinueState("失败后世界", w3.Snapshot())

	// 4) 引用规则不允许的物品或不存在的角色，即使正负变化互相抵消，
	//    整次提交仍然失败。
	w4 := multiDeltaWorld()
	if _, err := w4.Apply(world.Commit{
		ItemChanges: []world.ItemChange{
			{Character: "hero", Item: "ore", Delta: 3},
			{Character: "hero", Item: "ore", Delta: -3},
		},
	}); !errors.As(err, &re) {
		log.Fatalf("应返回 *world.RuleError，得到 %v", err)
	} else {
		fmt.Println("抵消的非法物品:", err)
	}
	if _, err := w4.Apply(world.Commit{
		ItemChanges: []world.ItemChange{
			{Character: "ghost", Item: "wood", Delta: 2},
			{Character: "ghost", Item: "wood", Delta: -2},
		},
	}); !errors.As(err, &re) {
		log.Fatalf("应返回 *world.RuleError，得到 %v", err)
	} else {
		fmt.Println("抵消的未知角色:", err)
	}

	// Output:
	// 初始: time=0 hero@village wood=1
	// 减少在前(-2,+5): time=2 hero@forest wood=4
	// 增加在前(+5,-2): time=2 hero@forest wood=4
	// 只减两件: 错误类型=*world.RuleError
	// 只减两件: world: 角色 "hero" 的物品 "wood" 数量不能为负: -1
	// 失败后世界: time=0 hero@village wood=1
	// 抵消的非法物品: world: 角色 "hero" 的物品变化引用了规则不允许的物品: "ore"
	// 抵消的未知角色: world: 物品变化引用了不存在的角色: "ghost"
}
