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

// carryTotal 精确求和同一角色全部物品数量的真实总量：使用不回绕的
// 精确整数运算，不截断数量、不丢弃条目、不改变排列。不同角色的总量
// 分别计算，互不合并。
func carryTotal(items []CharacterItem) *big.Int {
	total := new(big.Int)
	for _, it := range items {
		total.Add(total, bigInt(it.Count))
	}
	return total
}

// checkCarryLimit 按真实总量校验单个角色的携带上限：总量等于上限合法，
// 大于上限返回说明角色、真实总量与上限的规则错误。未为该角色设置上限时
// 不检查总量（其总量允许超过 int 最大值，也不受其他角色的上限约束）；
// 上限为零表示只能携带总量为零的物品，不视为未设置。
func checkCarryLimit(charID string, items []CharacterItem, limits map[string]int) error {
	limit, ok := limits[charID]
	if !ok {
		return nil
	}
	total := carryTotal(items)
	if total.Cmp(bigInt(limit)) > 0 {
		return ruleErrorf("角色 %q 携带总量 %s 超过上限 %d", charID, total.String(), limit)
	}
	return nil
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
