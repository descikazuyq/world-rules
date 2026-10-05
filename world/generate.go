package world

import (
	"sort"
	"unicode/utf8"
)

// maxGenerateLocations 是生成地图允许的最大地点数量。
const maxGenerateLocations = 256

// GenerateRequest 是按种子生成地图并建立世界的请求。
//
// Seed 是整数地图种子，支持完整的 int64 范围（含零和负数）。Locations
// 是地图地点；Required 是必有道路，Banned 是禁用道路，RoadCount 是
// 生成后不同道路的总数。道路无向：同一道路重复出现或反向列出只算一条。
// RulesVersion、ItemKinds、CarryLimits 与 Characters 与 NewWorld 的
// 对应输入含义相同，它们不参与地图生成，改变它们不影响生成的地图。
type GenerateRequest struct {
	// Seed 是整数地图种子。
	Seed int64
	// Locations 是地图地点，1 至 256 个，标识非空且不重复。
	Locations []string
	// Required 是必须包含的道路，可以成环。
	Required []Edge
	// Banned 是必须避开的道路。
	Banned []Edge
	// RoadCount 是生成后不同道路的总数。
	RoadCount int
	// RulesVersion 是规则版本。
	RulesVersion string
	// ItemKinds 是允许出现的物品种类。
	ItemKinds []string
	// CarryLimits 给出每个角色可携带物品的总量上限。
	CarryLimits map[string]int
	// Characters 是初始角色集合。
	Characters []Character
}

// GenerateWorld 按种子生成地图并建立世界。生成的地图是无向道路图：
// 包含全部必有道路、避开全部禁用道路、任意两个地点之间都能沿道路到达，
// 且不同道路的数量严格等于请求的目标数量；同一道路只保留一条，输出中
// 道路方向与排列是规范的（端点按字典序、道路按端点排序）。
//
// 同一种子和同一组地图约束生成的地点与道路内容、方向表示和排列完全
// 一致，与调用先后、是否生成过其他世界及进程是否重启无关；地点与两类
// 道路的输入次序、重复道路或正反写法不影响结果。存在多种合法地图时，
// 不同种子可以产生不同道路组合。约束不合法（地点数量越界、标识为空或
// 重复、道路引用未知地点或连接自身、同一道路同时必有和禁用、目标数量
// 为负、小于必有道路数、少于连通全图所需数量、超过可用道路数，或禁用
// 道路使地图无法连通）时返回 *RuleError，不会产生可用世界；约束有解时
// 每个种子都能成功生成。
//
// 生成不修改调用方输入；规则版本、角色、物品种类与携带上限按输入保留，
// 时间片从零开始。除此之外与 NewWorld 行为一致。
func GenerateWorld(req GenerateRequest) (*World, error) {
	locs, edges, err := generateMap(req.Seed, req.Locations, req.Required, req.Banned, req.RoadCount)
	if err != nil {
		return nil, err
	}
	return NewWorld(InitialData{
		Seed: req.Seed,
		Rules: Rules{
			Version:     req.RulesVersion,
			Locations:   locs,
			Edges:       edges,
			ItemKinds:   req.ItemKinds,
			CarryLimits: req.CarryLimits,
		},
		Characters: req.Characters,
	})
}

// canonicalEdge 把一条无向道路规范为端点字典序较小的在前。
func canonicalEdge(e Edge) Edge {
	if e.To < e.From {
		return Edge{From: e.To, To: e.From}
	}
	return e
}

// generateMap 按种子与地图约束生成规范的地点列表和道路列表。
// 输入切片不会被修改。
func generateMap(seed int64, locations []string, required, banned []Edge, roadCount int) ([]string, []Edge, error) {
	n := len(locations)
	if n < 1 || n > maxGenerateLocations {
		return nil, nil, ruleErrorf("地点数量必须在 1 到 %d 之间: %d", maxGenerateLocations, n)
	}
	locs := append([]string(nil), locations...)
	sort.Strings(locs)
	locSet := make(map[string]struct{}, n)
	for _, l := range locs {
		if !utf8.ValidString(l) {
			return nil, nil, ruleErrorf("地点标识不是合法 UTF-8: %q", l)
		}
		if l == "" {
			return nil, nil, ruleErrorf("地点标识不能为空")
		}
		if _, ok := locSet[l]; ok {
			return nil, nil, ruleErrorf("地点标识重复: %q", l)
		}
		locSet[l] = struct{}{}
	}

	// 必有与禁用道路各自去重（反向列出算同一条），并校验端点。
	reqSet := make(map[Edge]struct{}, len(required))
	for _, e := range required {
		if err := checkMapEdge(e, locSet, "必有道路"); err != nil {
			return nil, nil, err
		}
		reqSet[canonicalEdge(e)] = struct{}{}
	}
	banSet := make(map[Edge]struct{}, len(banned))
	for _, e := range banned {
		if err := checkMapEdge(e, locSet, "禁用道路"); err != nil {
			return nil, nil, err
		}
		banSet[canonicalEdge(e)] = struct{}{}
	}
	for e := range reqSet {
		if _, ok := banSet[e]; ok {
			return nil, nil, ruleErrorf("道路 %q-%q 不能同时必有和禁用", e.From, e.To)
		}
	}

	if roadCount < 0 {
		return nil, nil, ruleErrorf("目标道路数量不能为负: %d", roadCount)
	}
	if roadCount < len(reqSet) {
		return nil, nil, ruleErrorf("目标道路数量 %d 小于必有道路数量 %d", roadCount, len(reqSet))
	}

	// 候选道路：全部地点两两组合中未被禁用的道路，按规范次序排列。
	candidates := make([]Edge, 0, n*(n-1)/2)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			e := Edge{From: locs[i], To: locs[j]}
			if _, ok := banSet[e]; !ok {
				candidates = append(candidates, e)
			}
		}
	}
	if roadCount > len(candidates) {
		return nil, nil, ruleErrorf("目标道路数量 %d 超过可用道路数量 %d", roadCount, len(candidates))
	}

	index := make(map[string]int, n)
	for i, l := range locs {
		index[l] = i
	}

	// 必有道路先并入并查集；连通全图最少还需要 (分量数-1) 条道路。
	uf := newUnionFind(n)
	components := n
	for e := range reqSet {
		if uf.union(index[e.From], index[e.To]) {
			components--
		}
	}
	if minNeeded := len(reqSet) + components - 1; roadCount < minNeeded {
		return nil, nil, ruleErrorf("目标道路数量 %d 少于连通全图所需数量 %d", roadCount, minNeeded)
	}

	// 禁用道路不得使地图无法连通：全部候选道路必须能连成一片。
	{
		full := newUnionFind(n)
		c := n
		for _, e := range candidates {
			if full.union(index[e.From], index[e.To]) {
				c--
			}
		}
		if c > 1 {
			return nil, nil, ruleErrorf("禁用道路使地图无法连通")
		}
	}

	// 待选道路（候选中除去必有）按规范次序洗牌，洗牌只取决于种子与
	// 约束内容，因此结果与输入次序、重复及正反写法无关。
	pool := make([]Edge, 0, len(candidates)-len(reqSet))
	for _, e := range candidates {
		if _, ok := reqSet[e]; !ok {
			pool = append(pool, e)
		}
	}
	rnd := mapRand{state: uint64(seed)}
	for i := len(pool) - 1; i > 0; i-- {
		j := int(rnd.next() % uint64(i+1))
		pool[i], pool[j] = pool[j], pool[i]
	}

	// 先沿洗牌次序连通全图，连通后继续按次序补足目标数量。连通阶段
	// 因端点已属同一分量而跳过的道路记入 skipped：它们仍是合法道路，
	// 目标数量接近可用道路总数时需要靠它们补足，不能像洗牌耗尽一样
	// 误判为无解（种子只决定选哪张合法地图，不决定请求能否成功）。
	chosen := make([]Edge, 0, roadCount)
	for e := range reqSet {
		chosen = append(chosen, e)
	}
	skipped := make([]Edge, 0)
	for _, e := range pool {
		if len(chosen) == roadCount {
			break
		}
		if components > 1 {
			if uf.union(index[e.From], index[e.To]) {
				components--
				chosen = append(chosen, e)
			} else {
				skipped = append(skipped, e)
			}
		} else {
			chosen = append(chosen, e)
		}
	}
	if len(chosen) < roadCount {
		// 走到这里说明连通全图之前 pool 已耗尽（仅在洗牌次序使关键
		// 连通边排得靠后、而目标数量又需要几乎全部道路时发生）。补做
		// 一轮 Kruskal：按洗牌次序加入能合并分量的跳过道路；连通后
		// 剩余名额按洗牌次序由跳过道路补足。可行性检查已保证这些道路
		// 足以连通全图，且跳过道路总数足以补足目标数量。
		extraNeeded := roadCount - len(chosen)
		for _, e := range skipped {
			if extraNeeded == 0 {
				break
			}
			if components > 1 {
				if uf.union(index[e.From], index[e.To]) {
					components--
					chosen = append(chosen, e)
					extraNeeded--
				}
			} else {
				chosen = append(chosen, e)
				extraNeeded--
			}
		}
	}
	if len(chosen) != roadCount || components > 1 {
		// 前面的可行性检查已保证有解时不会走到这里。
		return nil, nil, ruleErrorf("无法生成 %d 条道路的连通地图", roadCount)
	}
	sort.Slice(chosen, func(i, j int) bool {
		if chosen[i].From != chosen[j].From {
			return chosen[i].From < chosen[j].From
		}
		return chosen[i].To < chosen[j].To
	})
	return locs, chosen, nil
}

// checkMapEdge 校验一条必有或禁用道路的端点：必须是合法 UTF-8、引用已有
// 地点，且不能连接同一地点。
func checkMapEdge(e Edge, locSet map[string]struct{}, what string) error {
	if !utf8.ValidString(e.From) {
		return ruleErrorf("%s起点不是合法 UTF-8: %q", what, e.From)
	}
	if !utf8.ValidString(e.To) {
		return ruleErrorf("%s终点不是合法 UTF-8: %q", what, e.To)
	}
	if _, ok := locSet[e.From]; !ok {
		return ruleErrorf("%s引用了不存在的地点: %q", what, e.From)
	}
	if _, ok := locSet[e.To]; !ok {
		return ruleErrorf("%s引用了不存在的地点: %q", what, e.To)
	}
	if e.From == e.To {
		return ruleErrorf("%s不能连接同一地点: %q", what, e.From)
	}
	return nil
}

// unionFind 是生成地图时用于连通性判断的并查集。
type unionFind struct {
	parent []int
}

func newUnionFind(n int) *unionFind {
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	return &unionFind{parent: parent}
}

func (u *unionFind) find(x int) int {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]]
		x = u.parent[x]
	}
	return x
}

// union 合并两个元素所在集合，原本不在同一集合时返回 true。
func (u *unionFind) union(a, b int) bool {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return false
	}
	u.parent[ra] = rb
	return true
}

// mapRand 是地图生成使用的确定性伪随机源（splitmix64），只由种子决定，
// 不依赖任何全局状态，因此同一种子在任何进程中都得到同一序列。
type mapRand struct {
	state uint64
}

func (r *mapRand) next() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}
