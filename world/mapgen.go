package world

import (
	"math/bits"
	"sort"
)

// MapSpec 描述按种子生成地图时必须满足的全部约束。
//
// Locations 是允许出现的全部地点（生成结果按字典序排列）；Required 是必须
// 包含的必有道路（无向，重复出现或正反写法只算一条，且可以成环）；
// Forbidden 是必须避开的禁用道路；Target 是最终不同道路的严格总数。
type MapSpec struct {
	// Seed 是整数地图种子，支持完整 int64 范围（含零与负数）。
	Seed int64
	// Locations 是地点列表，数量必须在 1 到 256 之间，标识非空且不重复。
	Locations []string
	// Required 是必有道路，全部出现在生成结果中。
	Required []Edge
	// Forbidden 是禁用道路，全部不出现在生成结果中。
	Forbidden []Edge
	// Target 是目标道路总数，必须非负，且不超过可用道路数。
	Target int
}

// maxMapLocations 是地点数的上限。
const maxMapLocations = 256

// edgeKey 是无向道路的规范表示：a 与 b 按字典序排列，a < b。
type edgeKey struct {
	a string
	b string
}

// GenerateMap 按种子生成满足全部约束的无向道路集合。
//
// 生成结果只与种子和约束有关：同一种子与同一组约束在任何时候、任何进程中
// 都给出完全一致的道路（内容、方向表示与排列均一致）；地点与两类道路的
// 输入次序、重复道路或正反写法都不影响结果。不同种子在存在多种合法地图时
// 应能产生不同组合；只有一种合法地图时允许结果相同。
//
// 必有道路全部保留，禁用道路全部避开，任意两个地点相互连通，不同道路
// 总数严格等于 Target。约束无解时返回 *RuleError，且不产生任何道路。
func GenerateMap(spec MapSpec) ([]Edge, error) {
	locs, err := canonicalLocations(spec.Locations)
	if err != nil {
		return nil, err
	}
	required, err := canonicalEdges(spec.Required, locs, "必有道路")
	if err != nil {
		return nil, err
	}
	forbidden, err := canonicalEdges(spec.Forbidden, locs, "禁用道路")
	if err != nil {
		return nil, err
	}
	if spec.Target < 0 {
		return nil, ruleErrorf("目标道路数量不能为负: %d", spec.Target)
	}
	// 同一条道路不能既必有又禁用。
	for key := range required {
		if _, ok := forbidden[key]; ok {
			return nil, ruleErrorf("道路 %q 同时被指定为必有和禁用", key.a+"<->"+key.b)
		}
	}
	need := len(locs) - 1
	if spec.Target < len(required) {
		return nil, ruleErrorf("目标道路数量 %d 少于必有道路数 %d", spec.Target, len(required))
	}
	if spec.Target < need {
		return nil, ruleErrorf("目标道路数量 %d 少于连通全图所需的 %d 条", spec.Target, need)
	}
	// 可用道路 = 全部道路去掉禁用道路。
	available := availableEdges(locs, forbidden)
	if spec.Target > len(available) {
		return nil, ruleErrorf("目标道路数量 %d 超过可用道路数 %d", spec.Target, len(available))
	}
	// 禁用道路不得使地图无法连通。
	if !graphConnected(locs, available) {
		return nil, ruleErrorf("禁用道路使地点无法全部连通")
	}

	picked := pickEdges(spec.Seed, locs, required, available, spec.Target)
	out := make([]Edge, 0, len(picked))
	for key := range picked {
		out = append(out, Edge{From: key.a, To: key.b})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out, nil
}

// NewSeededWorld 按种子生成满足约束的地图并建立世界。
//
// 地图只由 spec 决定：同一种子与同一组约束在任何时候、任何进程中都给出
// 完全一致的地图；规则版本、物品种类、携带上限与初始角色只影响世界的
// 其余部分，不影响地图。spec 与 data 不会被修改。生成在任一约束无解时
// 返回 *RuleError，且不产生可用世界。
//
// 生成后时间片从零开始，种子原样保留；规则版本、物品种类、携带上限以及
// 角色位置、物品数量和排列保持输入内容，并继续满足现有规则校验。
// data.Rules.Locations 与 data.Rules.Edges 被忽略：地点与道路以 spec
// 为准。生成的世界可照常提交、保存、恢复与分支；地图随存档保存，读档后
// 直接使用记录中的地图，不重新生成。
func NewSeededWorld(spec MapSpec, data InitialData) (*World, error) {
	edges, err := GenerateMap(spec)
	if err != nil {
		return nil, err
	}
	rules := Rules{
		Version:     data.Rules.Version,
		Locations:   append([]string(nil), spec.Locations...),
		Edges:       edges,
		ItemKinds:   append([]string(nil), data.Rules.ItemKinds...),
		CarryLimits: cloneCarryLimits(data.Rules.CarryLimits),
	}
	st := State{
		Seed:       spec.Seed,
		Rules:      rules,
		Time:       0,
		Characters: cloneCharacters(data.Characters),
	}
	if err := validateInitialData(InitialData{
		Seed:       st.Seed,
		Rules:      rules,
		Characters: st.Characters,
	}); err != nil {
		return nil, err
	}
	return &World{state: st}, nil
}

// canonicalLocations 规范化地点列表：数量限制在 1 到 maxMapLocations，
// 标识非空且不重复，结果按字典序排列，使输入次序不影响输出。
func canonicalLocations(in []string) ([]string, error) {
	if len(in) < 1 || len(in) > maxMapLocations {
		return nil, ruleErrorf("地点数必须在 1 到 %d 之间，得到 %d", maxMapLocations, len(in))
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, l := range in {
		if l == "" {
			return nil, ruleErrorf("地点标识不能为空")
		}
		if _, ok := seen[l]; ok {
			return nil, ruleErrorf("地点标识重复: %q", l)
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	sort.Strings(out)
	return out, nil
}

// canonicalEdges 规范化一类道路：端点必须是已排序的地点、不得连接自身；
// 无向道路按字典序定向，重复或正反写法只算一条。
func canonicalEdges(in []Edge, locs []string, what string) (map[edgeKey]struct{}, error) {
	locSet := make(map[string]struct{}, len(locs))
	for _, l := range locs {
		locSet[l] = struct{}{}
	}
	out := make(map[edgeKey]struct{}, len(in))
	for i, e := range in {
		if _, ok := locSet[e.From]; !ok {
			return nil, ruleErrorf("%s %d 引用了不存在的地点: %q", what, i, e.From)
		}
		if _, ok := locSet[e.To]; !ok {
			return nil, ruleErrorf("%s %d 引用了不存在的地点: %q", what, i, e.To)
		}
		if e.From == e.To {
			return nil, ruleErrorf("%s %d 连接了地点自身: %q", what, i, e.From)
		}
		a, b := e.From, e.To
		if a > b {
			a, b = b, a
		}
		out[edgeKey{a, b}] = struct{}{}
	}
	return out, nil
}

// availableEdges 返回全部道路中去掉禁用道路后的可用道路集合，按字典序
// 排列。
func availableEdges(locs []string, forbidden map[edgeKey]struct{}) []edgeKey {
	out := make([]edgeKey, 0, len(locs)*(len(locs)-1)/2)
	for i := 0; i < len(locs); i++ {
		for j := i + 1; j < len(locs); j++ {
			key := edgeKey{locs[i], locs[j]}
			if _, ok := forbidden[key]; ok {
				continue
			}
			out = append(out, key)
		}
	}
	return out
}

// graphConnected 判断只使用 available 中的道路能否让全部地点相互连通。
func graphConnected(locs []string, available []edgeKey) bool {
	parent := make(map[string]string, len(locs))
	for _, l := range locs {
		parent[l] = l
	}
	var find func(string) string
	find = func(x string) string {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	components := len(locs)
	for _, e := range available {
		ra, rb := find(e.a), find(e.b)
		if ra != rb {
			parent[ra] = rb
			components--
			if components == 1 {
				return true
			}
		}
	}
	return components <= 1
}

// pickEdges 在满足约束的前提下选取恰好 target 条不同道路：先打乱可用
// 道路，保留全部必有道路，再沿打乱次序用并查集补足连通，最后补足到目标
// 数量。打乱由种子驱动，与输入次序无关；不同种子在存在多种合法地图时
// 应能产生不同组合。
func pickEdges(seed int64, locs []string, required map[edgeKey]struct{}, available []edgeKey, target int) map[edgeKey]struct{} {
	rng := newMapRNG(seed)
	pool := append([]edgeKey(nil), available...)
	for i := len(pool) - 1; i > 0; i-- {
		j := int(rng.uint64n(uint64(i + 1)))
		pool[i], pool[j] = pool[j], pool[i]
	}

	parent := make(map[string]string, len(locs))
	for _, l := range locs {
		parent[l] = l
	}
	var find func(string) string
	find = func(x string) string {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b string) bool {
		ra, rb := find(a), find(b)
		if ra == rb {
			return false
		}
		parent[ra] = rb
		return true
	}

	picked := make(map[edgeKey]struct{}, target)
	components := len(locs)
	for key := range required {
		picked[key] = struct{}{}
		if union(key.a, key.b) {
			components--
		}
	}
	// 先沿打乱次序连通全图。
	for _, key := range pool {
		if components == 1 {
			break
		}
		if _, ok := picked[key]; ok {
			continue
		}
		if union(key.a, key.b) {
			picked[key] = struct{}{}
			components--
		}
	}
	// 再补足到目标数量。
	for _, key := range pool {
		if len(picked) >= target {
			break
		}
		if _, ok := picked[key]; ok {
			continue
		}
		picked[key] = struct{}{}
	}
	return picked
}

// mapRNG 是种子驱动的确定性伪随机数生成器（splitmix64）。自包含实现，
// 不依赖 math/rand 的版本行为，因此同一种子在任何进程、任何 Go 版本下
// 都给出完全一致的序列。
type mapRNG struct {
	state uint64
}

func newMapRNG(seed int64) *mapRNG {
	return &mapRNG{state: uint64(seed)}
}

func (r *mapRNG) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// uint64n 返回 [0, n) 内的均匀随机数（n > 0），用 Lemire 方法取模并
// 拒绝极小概率的偏差。
func (r *mapRNG) uint64n(n uint64) uint64 {
	v := r.next()
	hi, lo := bits.Mul64(v, n)
	if lo < n {
		threshold := (^uint64(0) - n + 1) % n
		for lo < threshold {
			v = r.next()
			hi, lo = bits.Mul64(v, n)
		}
	}
	return hi
}

// cloneCarryLimits 深拷贝携带上限表。
func cloneCarryLimits(in map[string]int) map[string]int {
	if in == nil {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
