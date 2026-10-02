package world

import (
	"math"
	"math/big"
)

// bigIntTotal 返回若干 int 的精确总和（任意精度，不回绕）。
//
// 数量与增量在接口层仍使用 Go 的 int；内部求和改用任意精度整数，
// 避免接近 int 边界时加法回绕把违规状态误判为合法。
func bigIntTotal(vals []int) *big.Int {
	total := new(big.Int)
	for _, v := range vals {
		total.Add(total, big.NewInt(int64(v)))
	}
	return total
}

// fitsInInt 判断 x 是否能被 Go 的 int 精确表示。
//
// int 在 64 位平台上是 int64、32 位平台上是 int32，因此不能只判断
// IsInt64，还要与平台的 math.MinInt、math.MaxInt 比较。
func fitsInInt(x *big.Int) bool {
	if !x.IsInt64() {
		return false
	}
	v := x.Int64()
	return v >= math.MinInt && v <= math.MaxInt
}
