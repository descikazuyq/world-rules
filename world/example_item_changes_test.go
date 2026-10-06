package world_test

import (
	"errors"
	"fmt"
	"log"
	"math"

	"github.com/descikazuyq/world-rules/world"
)

// woodWorld 建立文档示例共用的初始世界：时间片 0，hero 在 village、持有
// 1 件 wood；village 与 forest 之间有一条无向道路，规则只允许 wood，
// hero 的携带总量上限由 limit 给出。
func woodWorld(limit int) *world.World {
	w, err := world.NewWorld(world.InitialData{
		Seed: 20240802,
		Rules: world.Rules{
			Version:     "v1",
			Locations:   []string{"village", "forest"},
			Edges:       []world.Edge{{From: "village", To: "forest"}},
			ItemKinds:   []string{"wood"},
			CarryLimits: map[string]int{"hero": limit},
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

// printWoodState 以固定格式打印时间片、角色位置与 wood 数量。
func printWoodState(tag string, w *world.World) {
	s := w.Snapshot()
	c := s.Characters[0]
	fmt.Printf("%s: time=%d %s@%s wood=%d\n", tag, s.Time, c.ID, c.Location, c.Items[0].Count)
}

// printRuleError 打印失败提交的错误全文，并明确它是否为 *world.RuleError。
func printRuleError(tag string, err error) {
	var re *world.RuleError
	fmt.Printf("%s: %v 类型=*world.RuleError:%t\n", tag, err, errors.As(err, &re))
}

// Example_applyMergedItemChanges 演示一次提交中对同一种物品多次增减时，
// 最终数量由“原有数量与本次全部增减合并”决定，与条目次序无关；失败提交
// 返回 *world.RuleError 且世界保持提交前状态。
func Example_applyMergedItemChanges() {
	// 一、同一次提交先减 2 再增 5，同时沿 village-forest 移动、时间片 0->2。
	// 最终数量按 1 - 2 + 5 = 4 判断；-2 虽然排在前面，中途的 -1 只存在于
	// 合并计算中，世界上不会出现可被读取的负数量。上限 4：最终 4 恰好等于
	// 上限，允许。
	w := woodWorld(4)
	printWoodState("提交前", w)
	if _, err := w.Apply(world.Commit{
		Moves: []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{
			{Character: "hero", Item: "wood", Delta: -2},
			{Character: "hero", Item: "wood", Delta: 5},
		},
		Time: 2, // 目标绝对时间片：0 -> 2
	}); err != nil {
		log.Fatal(err)
	}
	printWoodState("先减后增", w)

	// Commit.Time 是目标绝对时间片：当前已是 2，提交目标 1 属于时间倒退，
	// 即使没有移动或物品变化也返回规则错误。
	_, err := w.Apply(world.Commit{Time: 1})
	printRuleError("时间倒退", err)

	// 二、从相同初始状态交换两条增减的次序：判断依据是原有数量与本次全部
	// 增减合并后的结果，与条目次序无关，成功结果完全相同。
	w2 := woodWorld(4)
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
	printWoodState("先增后减", w2)

	// 三、从相同初始状态只提交减少 2（移动与时间目标仍合法）：最终数量
	// 1 - 2 = -1，整次提交失败，移动与时间推进一起回滚，世界保持提交前
	// 状态。
	w3 := woodWorld(4)
	_, err = w3.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "wood", Delta: -2}},
		Time:        2,
	})
	printRuleError("只减两件", err)
	printWoodState("失败后", w3)

	// 四、把 -2 与 +5 拆成两次 Apply（这里上限放到 6，以便看清第二次提交的
	// 落点）：第一步就因最终数量为负而失败，移动与时间一起回滚，世界保持
	// 原状；之后的 +5 是另一次独立提交（1 -> 6），它不能把第一次变成一次
	// 成功提交，也拼不出原先那次“到 forest、净 +3 到 4 件”的结果。
	w4 := woodWorld(6)
	_, err = w4.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "wood", Delta: -2}},
		Time:        2,
	})
	printRuleError("拆开第一步", err)
	printWoodState("第一步失败后", w4)
	if _, err := w4.Apply(world.Commit{
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "wood", Delta: 5}},
		Time:        2,
	}); err != nil {
		log.Fatal(err)
	}
	printWoodState("另一次提交+5", w4)

	// 五、数量范围：合并用精确整数完成，中途暂时超出 int 范围不失败：
	// 1 + MaxInt - (MaxInt-3) = 4，最终仍是 int 可表示的非负整数即成功。
	w5 := woodWorld(4)
	if _, err := w5.Apply(world.Commit{
		ItemChanges: []world.ItemChange{
			{Character: "hero", Item: "wood", Delta: math.MaxInt},
			{Character: "hero", Item: "wood", Delta: -(math.MaxInt - 3)},
		},
		Time: 1,
	}); err != nil {
		log.Fatal(err)
	}
	printWoodState("中途超出int", w5)

	// 最终数量超出 int（1 + MaxInt）则拒绝，世界保持提交前状态。
	w6 := woodWorld(4)
	_, err = w6.Apply(world.Commit{
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "wood", Delta: math.MaxInt}},
		Time:        1,
	})
	printRuleError("最终超出int", err)
	printWoodState("溢出失败后", w6)

	// 六、携带上限按本次全部变化完成后的真实总量判断：前面的提交最终 4 件、
	// 上限 4（等于上限）允许；同样的提交把上限改成 3，真实总量 4 大于上限，
	// 整次提交失败。
	w7 := woodWorld(3)
	_, err = w7.Apply(world.Commit{
		Moves: []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{
			{Character: "hero", Item: "wood", Delta: -2},
			{Character: "hero", Item: "wood", Delta: 5},
		},
		Time: 2,
	})
	printRuleError("大于上限", err)
	printWoodState("超限失败后", w7)

	// 七、引用不存在的角色、或规则不允许的物品，即使正负变化互相抵消为 0，
	// 整次提交仍失败。
	w8 := woodWorld(4)
	_, err = w8.Apply(world.Commit{
		ItemChanges: []world.ItemChange{
			{Character: "ghost", Item: "wood", Delta: 5},
			{Character: "ghost", Item: "wood", Delta: -5},
		},
		Time: 1,
	})
	printRuleError("未知角色", err)
	_, err = w8.Apply(world.Commit{
		ItemChanges: []world.ItemChange{
			{Character: "hero", Item: "gem", Delta: 5},
			{Character: "hero", Item: "gem", Delta: -5},
		},
		Time: 1,
	})
	printRuleError("不允许的物品", err)
	printWoodState("抵消失败后", w8)

	// Output:
	// 提交前: time=0 hero@village wood=1
	// 先减后增: time=2 hero@forest wood=4
	// 时间倒退: world: 时间不能倒退: 当前 2, 提交目标 1 类型=*world.RuleError:true
	// 先增后减: time=2 hero@forest wood=4
	// 只减两件: world: 角色 "hero" 的物品 "wood" 数量不能为负: -1 类型=*world.RuleError:true
	// 失败后: time=0 hero@village wood=1
	// 拆开第一步: world: 角色 "hero" 的物品 "wood" 数量不能为负: -1 类型=*world.RuleError:true
	// 第一步失败后: time=0 hero@village wood=1
	// 另一次提交+5: time=2 hero@village wood=6
	// 中途超出int: time=1 hero@village wood=4
	// 最终超出int: world: 角色 "hero" 的物品 "wood" 数量超出整数范围: 9223372036854775808 类型=*world.RuleError:true
	// 溢出失败后: time=0 hero@village wood=1
	// 大于上限: world: 角色 "hero" 携带总量 4 超过上限 3 类型=*world.RuleError:true
	// 超限失败后: time=0 hero@village wood=1
	// 未知角色: world: 物品变化引用了不存在的角色: "ghost" 类型=*world.RuleError:true
	// 不允许的物品: world: 角色 "hero" 的物品变化引用了规则不允许的物品: "gem" 类型=*world.RuleError:true
	// 抵消失败后: time=0 hero@village wood=1
}
