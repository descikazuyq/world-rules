package world

import (
	"math"
	"math/big"
)

// maxIntBig 是 int 能表示的最大值，用于精确整数运算中的范围判断。
var maxIntBig = big.NewInt(math.MaxInt)

// bigInt 把 int 数量或增量提升为精确整数，参与不回绕的加减运算。
func bigInt(v int) *big.Int {
	return big.NewInt(int64(v))
}

// finalCount 校验合并后的精确数量是 int 能表示的非负整数，并返回其
// int 值；为负或超出 int 范围时返回说明角色、物品及原因的规则错误。
func finalCount(charID, item string, final *big.Int) (int, error) {
	if final.Sign() < 0 {
		return 0, ruleErrorf("角色 %q 的物品 %q 数量不能为负: %s", charID, item, final.String())
	}
	if final.Cmp(maxIntBig) > 0 {
		return 0, ruleErrorf("角色 %q 的物品 %q 数量超出整数范围: %s", charID, item, final.String())
	}
	return int(final.Int64()), nil
}

// carryTotal 返回同一角色携带物品的真实总量：其全部物品数量的精确和。
//
// 只相加传入的同一角色物品，不同角色必须分别调用、绝不合并。求和用精确
// 整数完成、不回绕，因此真实总量允许超过 int 最大值；是否超限交由上限
// 判断另行决定。
func carryTotal(items []CharacterItem) *big.Int {
	total := new(big.Int)
	for _, it := range items {
		total.Add(total, bigInt(it.Count))
	}
	return total
}

// totalExceedsLimit 是携带总量与上限的唯一比较口径：真实总量等于上限
// 合法，大于上限才拒绝。total 为精确真实总量（见 carryTotal），limit 是
// 已确认存在的非负上限；调用方需先区分“上限为 0”与“未设置上限”。
func totalExceedsLimit(total *big.Int, limit int) bool {
	return total.Cmp(bigInt(limit)) > 0
}

// carryLimitError 按角色携带上限规则校验一个角色的真实总量，超限时返回
// 说明角色、完整十进制真实总量与上限的规则错误。
//
// 上限语义集中在此：limits 中没有该角色的键表示不设上限，该角色不受任何
// 总量约束（即使各物品数量相加超过 int 最大值也合法），不受其他角色上限
// 影响；键存在且值为 0 仍是真实上限，表示只能携带总量为 0 的物品，不能
// 当作未设置。建立/重建世界、提交物品变化与存档校验共用本判断。
func carryLimitError(charID string, items []CharacterItem, limits map[string]int) error {
	limit, hasLimit := limits[charID]
	if !hasLimit {
		return nil
	}
	total := carryTotal(items)
	if totalExceedsLimit(total, limit) {
		return ruleErrorf("角色 %q 携带总量 %s 超过上限 %d", charID, total.String(), limit)
	}
	return nil
}
