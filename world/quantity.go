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
