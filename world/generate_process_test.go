package world

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// 本文件用相互独立的操作系统进程回归“相同种子 + 相同地图约束 ⇒ 完全
// 一致的地图”的承诺：父进程构造完整的 GenerateRequest，把每个请求分别
// 交给一个全新进程，由后者实际调用 GenerateWorld 建立新世界，并把这次
// 实际生成的结果序列化回传；父进程随后在各次实际结果之间逐字段比较
// （地点/道路列表的内容、顺序与道路端点的方向表示），并对每个进程的
// 结果独立校验全部生成约束。

// processGenerateResult 是子进程对一次实际生成的完整报告。失败时
// RuleError 置位、State 为空；成功时 State 是新建立世界的真实快照。
type processGenerateResult struct {
	OK        bool   `json:"ok"`
	RuleError string `json:"rule_error,omitempty"`
	OtherErr  string `json:"other_err,omitempty"`
	State     *State `json:"state,omitempty"`
}

// helperGenerateRequest 只用于在独立进程间经 JSON 文件传递完整生成
// 请求；字段与 GenerateRequest 一一对应，避免给公开类型增加 JSON 标签
// 或改变公开行为。
type helperGenerateRequest struct {
	Seed         int64          `json:"seed"`
	Locations    []string       `json:"locations"`
	Required     []Edge         `json:"required"`
	Banned       []Edge         `json:"banned"`
	RoadCount    int            `json:"road_count"`
	RulesVersion string         `json:"rules_version"`
	ItemKinds    []string       `json:"item_kinds"`
	CarryLimits  map[string]int `json:"carry_limits"`
	Characters   []Character    `json:"characters"`
}

func (r helperGenerateRequest) toRequest() GenerateRequest {
	req := GenerateRequest{
		Seed:         r.Seed,
		Locations:    append([]string(nil), r.Locations...),
		Required:     append([]Edge(nil), r.Required...),
		Banned:       append([]Edge(nil), r.Banned...),
		RoadCount:    r.RoadCount,
		RulesVersion: r.RulesVersion,
		ItemKinds:    append([]string(nil), r.ItemKinds...),
		Characters:   append([]Character(nil), r.Characters...),
	}
	if r.CarryLimits != nil {
		req.CarryLimits = make(map[string]int, len(r.CarryLimits))
		for k, v := range r.CarryLimits {
			req.CarryLimits[k] = v
		}
	}
	return req
}

func toHelperRequest(req GenerateRequest) helperGenerateRequest {
	h := helperGenerateRequest{
		Seed:         req.Seed,
		Locations:    append([]string(nil), req.Locations...),
		Required:     append([]Edge(nil), req.Required...),
		Banned:       append([]Edge(nil), req.Banned...),
		RoadCount:    req.RoadCount,
		RulesVersion: req.RulesVersion,
		ItemKinds:    append([]string(nil), req.ItemKinds...),
		Characters:   append([]Character(nil), req.Characters...),
	}
	if req.CarryLimits != nil {
		h.CarryLimits = make(map[string]int, len(req.CarryLimits))
		for k, v := range req.CarryLimits {
			h.CarryLimits[k] = v
		}
	}
	return h
}

// runGenerateInFreshProcess 在一个全新操作系统进程中实际调用
// GenerateWorld 并返回这次生成的真实结果（由 JSON 反序列化而成，
// 与父进程内存无共享）。每次都传入完整请求、在新进程中建立新世界。
func runGenerateInFreshProcess(t *testing.T, req GenerateRequest) processGenerateResult {
	t.Helper()
	dir := t.TempDir()
	reqPath := filepath.Join(dir, "request.json")
	resPath := filepath.Join(dir, "result.json")
	payload, err := json.Marshal(toHelperRequest(req))
	if err != nil {
		t.Fatalf("序列化生成请求失败: %v", err)
	}
	if err := os.WriteFile(reqPath, payload, 0o600); err != nil {
		t.Fatalf("写入请求文件失败: %v", err)
	}

	c := exec.Command(os.Args[0], "-test.run=TestMain")
	c.Env = append(os.Environ(),
		"WORLD_TEST_HELPER=generate_world",
		"WORLD_TEST_REQUEST="+reqPath,
		"WORLD_TEST_RESULT="+resPath,
	)
	var stderr strings.Builder
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		t.Fatalf("生成子进程退出异常: %v\nstderr:\n%s", err, stderr.String())
	}
	raw, err := os.ReadFile(resPath)
	if err != nil {
		t.Fatalf("读取生成子进程结果失败: %v\nstderr:\n%s", err, stderr.String())
	}
	var out processGenerateResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("解析生成子进程结果失败: %v\nstderr:\n%s", err, stderr.String())
	}
	if !out.OK && out.OtherErr != "" {
		t.Fatalf("生成子进程报告非约定错误: %s", out.OtherErr)
	}
	return out
}

// helperGenerateWorld 在子进程中执行：读取完整请求 -> 实际生成并建立
// 新世界 -> 把本次实际生成结果写出。沿用公开行为与错误约定：约束不
// 合法时必须是 *RuleError，且不产生世界。
func helperGenerateWorld() {
	reqPath := os.Getenv("WORLD_TEST_REQUEST")
	resPath := os.Getenv("WORLD_TEST_RESULT")
	raw, err := os.ReadFile(reqPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read request:", err)
		os.Exit(1)
	}
	var in helperGenerateRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		fmt.Fprintln(os.Stderr, "decode request:", err)
		os.Exit(1)
	}
	req := in.toRequest()
	res := processGenerateResult{}
	w, err := GenerateWorld(req)
	if err != nil {
		if _, ok := err.(*RuleError); ok {
			res.RuleError = err.Error()
		} else {
			res.OtherErr = err.Error()
		}
		helperWriteResult(resPath, res)
		return
	}
	st := w.Snapshot()
	res.OK = true
	res.State = &st
	helperWriteResult(resPath, res)
}

func helperWriteResult(path string, res processGenerateResult) {
	payload, err := json.Marshal(res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal result:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write result:", err)
		os.Exit(1)
	}
}

// assertGeneratedConstraints 在一份实际生成结果上校验原有全部地图约束：
// 必有道路全部保留（重复/反写只算一条）、禁用道路全部避开、不同道路
// 数量恰好等于目标值、全图连通，以及规范的方向与排列、种子保留。
func assertGeneratedConstraints(t *testing.T, res processGenerateResult, req GenerateRequest) {
	t.Helper()
	if !res.OK {
		t.Fatalf("本应成功的生成在子进程中失败: %s", res.RuleError)
	}
	st := res.State
	if st == nil {
		t.Fatal("子进程成功但未返回世界状态")
	}
	assertMapShape(t, *st, req)

	if st.Seed != req.Seed {
		t.Fatalf("种子应原样保留为 %d，得到 %d", req.Seed, st.Seed)
	}
	// 道路端点必须按名称顺序表示。
	for _, e := range st.Rules.Edges {
		if e.From >= e.To {
			t.Fatalf("道路端点未按名称顺序表示: %+v", e)
		}
	}
	// 整个道路列表必须按端点名称排序。
	for i := 1; i < len(st.Rules.Edges); i++ {
		a, b := st.Rules.Edges[i-1], st.Rules.Edges[i]
		if a.From > b.From || (a.From == b.From && a.To >= b.To) {
			t.Fatalf("道路列表未按端点名称排序: %+v 排在 %+v 之前", a, b)
		}
	}
	// 输出地点必须按名称排序。
	for i := 1; i < len(st.Rules.Locations); i++ {
		if st.Rules.Locations[i-1] >= st.Rules.Locations[i] {
			t.Fatalf("地点列表未按名称排序: %v", st.Rules.Locations)
		}
	}
}

// assertSameMapAcrossResults 在多个进程的实际结果之间逐字段比较地图：
// 地点列表（含顺序）与道路列表（含顺序和端点方向表示）必须完全相同，
// 不能只比较道路数量。
func assertSameMapAcrossResults(t *testing.T, tag string, results []processGenerateResult) {
	t.Helper()
	base := results[0].State
	for i := 1; i < len(results); i++ {
		got := results[i].State
		if !reflect.DeepEqual(base.Rules.Locations, got.Rules.Locations) {
			t.Fatalf("[%s] 跨进程地点列表不一致（含顺序）:\n%v\n%v",
				tag, base.Rules.Locations, got.Rules.Locations)
		}
		if !reflect.DeepEqual(base.Rules.Edges, got.Rules.Edges) {
			t.Fatalf("[%s] 跨进程道路列表不一致（含顺序与端点方向）:\n%v\n%v",
				tag, base.Rules.Edges, got.Rules.Edges)
		}
	}
}

// equivalentRequestPermutations 返回两份语义等价但写法不同的完整请求：
// 调换地点输入顺序，必有/禁用道路整体反写并倒序，再重复列出同一道路
// （正向和反向）。道路仍按无向关系处理，这些变化不得改变生成结果。
// 返回的请求与入参不共享切片与映射。
func equivalentRequestPermutations(req GenerateRequest) []GenerateRequest {
	shuffledLocs := append([]string(nil), req.Locations...)
	reverseStrings(shuffledLocs)

	shuffledReqEdges := make([]Edge, 0, len(req.Required)*2+1)
	for _, e := range req.Required {
		shuffledReqEdges = append(shuffledReqEdges, Edge{From: e.To, To: e.From})
	}
	for i := len(shuffledReqEdges) - 1; i >= 0; i-- {
		shuffledReqEdges = append(shuffledReqEdges, shuffledReqEdges[i])
	}
	if len(req.Required) > 0 {
		// 再把第一条正向重复一次，令同一条道路在输入中出现三次。
		shuffledReqEdges = append(shuffledReqEdges, req.Required[0])
	}

	shuffledBanEdges := make([]Edge, 0, len(req.Banned)*2+1)
	for _, e := range req.Banned {
		shuffledBanEdges = append(shuffledBanEdges, Edge{From: e.To, To: e.From})
	}
	for i := len(shuffledBanEdges) - 1; i >= 0; i-- {
		shuffledBanEdges = append(shuffledBanEdges, shuffledBanEdges[i])
	}
	if len(req.Banned) > 0 {
		first := req.Banned[0]
		shuffledBanEdges = append(shuffledBanEdges, Edge{From: first.To, To: first.From})
	}

	alt := req
	alt.Locations = shuffledLocs
	alt.Required = shuffledReqEdges
	alt.Banned = shuffledBanEdges
	alt.ItemKinds = append([]string(nil), req.ItemKinds...)
	alt.Characters = cloneCharacters(req.Characters)
	if req.CarryLimits != nil {
		alt.CarryLimits = make(map[string]int, len(req.CarryLimits))
		for k, v := range req.CarryLimits {
			alt.CarryLimits[k] = v
		}
	}
	return []GenerateRequest{cloneGenerateRequest(req), alt}
}

// cloneGenerateRequest 返回请求的深拷贝，保证各次进程调用之间以及与
// 调用方输入之间不共享底层数据。
func cloneGenerateRequest(req GenerateRequest) GenerateRequest {
	cp := req
	cp.Locations = append([]string(nil), req.Locations...)
	cp.Required = append([]Edge(nil), req.Required...)
	cp.Banned = append([]Edge(nil), req.Banned...)
	cp.ItemKinds = append([]string(nil), req.ItemKinds...)
	cp.Characters = cloneCharacters(req.Characters)
	if req.CarryLimits != nil {
		cp.CarryLimits = make(map[string]int, len(req.CarryLimits))
		for k, v := range req.CarryLimits {
			cp.CarryLimits[k] = v
		}
	}
	return cp
}

func reverseStrings(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// TestGenerateDeterministicAcrossProcesses 是核心回归：同一个完整请求
// 的等价写法在多个全新进程中分别生成、建立新世界，实际结果的地图
// （地点与道路的内容、顺序、方向）必须逐字段一致，且每个进程的结果
// 都满足全部约束。场景覆盖存在多种合法道路组合的地图，以及必有道路
// 已在部分地点间成环、仍需连接其他地点的情况。
func TestGenerateDeterministicAcrossProcesses(t *testing.T) {
	if os.Getenv("WORLD_TEST_HELPER") != "" {
		t.Skip("子进程模式")
	}

	cases := []struct {
		name string
		req  GenerateRequest
	}{
		{
			name: "基础场景",
			req:  baseGenerateRequest(),
		},
		{
			// 存在多种合法道路组合：6 个地点、无必有无禁用、目标 8 条，
			// 合法的连通 8 边图非常多，种子真正参与选择。
			name: "多种合法组合",
			req: GenerateRequest{
				Seed:         4242,
				Locations:    []string{"a", "b", "c", "d", "e", "f"},
				RoadCount:    8,
				RulesVersion: "v1",
				ItemKinds:    []string{"gold"},
			},
		},
		{
			// 必有道路已在 a/b/c 之间成环，仍需生成道路连接 d/e，
			// 且 d-e 被禁用；目标 7 条留有多种合法组合。
			name: "必有成环且需连接其余地点",
			req: GenerateRequest{
				Seed:      7,
				Locations: []string{"a", "b", "c", "d", "e"},
				Required: []Edge{
					{From: "a", To: "b"},
					{From: "b", To: "c"},
					{From: "c", To: "a"},
				},
				Banned:       []Edge{{From: "d", To: "e"}},
				RoadCount:    7,
				RulesVersion: "v1",
			},
		},
		{
			// 更大的图、更多可选组合、成环的必有路加禁用边，目标数量居中。
			name: "大图成环加禁用",
			req: GenerateRequest{
				Seed:      -77,
				Locations: []string{"n0", "n1", "n2", "n3", "n4", "n5", "n6", "n7"},
				Required: []Edge{
					{From: "n0", To: "n1"},
					{From: "n1", To: "n2"},
					{From: "n2", To: "n0"},
				},
				Banned: []Edge{
					{From: "n3", To: "n7"},
					{From: "n4", To: "n6"},
				},
				RoadCount:    12,
				RulesVersion: "v1",
			},
		},
		{
			// 单地点无边：退化为空道路列表，仍跨进程一致。
			name: "单地点",
			req: GenerateRequest{
				Seed:         3,
				Locations:    []string{"only"},
				RoadCount:    0,
				RulesVersion: "v1",
			},
		},
		{
			// 接近满图：必有成环 + 接近全部可用道路，触发跳过道路补足逻辑。
			name: "成环且接近满图",
			req: GenerateRequest{
				Seed:      123,
				Locations: []string{"a", "b", "c", "d", "e"},
				Required: []Edge{
					{From: "a", To: "b"},
					{From: "b", To: "c"},
					{From: "c", To: "a"},
				},
				RoadCount:    9,
				RulesVersion: "v1",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// 同一完整请求与其等价写法，分别在两个互相独立、且与父进程
			// 也独立的进程中生成。
			reqs := equivalentRequestPermutations(tc.req)
			results := make([]processGenerateResult, len(reqs))
			for i, r := range reqs {
				results[i] = runGenerateInFreshProcess(t, r)
			}

			// 每个进程得到的地图都必须独立满足原有全部约束。
			for i := range results {
				assertGeneratedConstraints(t, results[i], reqs[i])
			}
			// 约束等价的请求（调换顺序、重复、反写）跨进程结果完全一致，
			// 比较覆盖列表顺序与道路端点的方向表示。
			assertSameMapAcrossResults(t, tc.name, results)
		})
	}
}

// TestGenerateSeedExtremesAcrossProcesses 回归零、负数及 int64 最小/最大
// 值种子：各自原样保存在生成的世界中，且同一极端种子的等价请求跨进程
// 生成结果一致。
func TestGenerateSeedExtremesAcrossProcesses(t *testing.T) {
	if os.Getenv("WORLD_TEST_HELPER") != "" {
		t.Skip("子进程模式")
	}
	seeds := []int64{0, -1, -42, math.MinInt64, math.MaxInt64}
	for _, seed := range seeds {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			req := baseGenerateRequest()
			req.Seed = seed
			reqs := equivalentRequestPermutations(req)
			results := make([]processGenerateResult, len(reqs))
			for i, r := range reqs {
				results[i] = runGenerateInFreshProcess(t, r)
			}
			for i := range results {
				if !results[i].OK {
					t.Fatalf("种子 %d 生成失败: %s", seed, results[i].RuleError)
				}
				if results[i].State.Seed != seed {
					t.Fatalf("种子应原样保存为 %d，得到 %d", seed, results[i].State.Seed)
				}
				assertGeneratedConstraints(t, results[i], reqs[i])
			}
			assertSameMapAcrossResults(t, fmt.Sprintf("seed=%d", seed), results)
		})
	}
}

// TestGenerateManySeedsCrossProcess 对一批种子逐一在独立进程中实际生成，
// 并与父进程内直接生成的真实结果对照，覆盖“多种合法组合”的地图随种子
// 确定且可跨进程复现的性质。
func TestGenerateManySeedsCrossProcess(t *testing.T) {
	if os.Getenv("WORLD_TEST_HELPER") != "" {
		t.Skip("子进程模式")
	}
	req := GenerateRequest{
		Locations:    []string{"a", "b", "c", "d", "e", "f"},
		RoadCount:    8,
		RulesVersion: "v1",
		ItemKinds:    []string{"gold"},
	}
	for _, seed := range []int64{-300, -1, 0, 1, 2, 99, math.MaxInt64 - 1} {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := req
			r.Seed = seed
			inProc := generateWorld(t, r).Snapshot()
			out := runGenerateInFreshProcess(t, r)
			if !out.OK {
				t.Fatalf("种子 %d 在子进程中生成失败: %s", seed, out.RuleError)
			}
			assertGeneratedConstraints(t, out, r)
			if !reflect.DeepEqual(inProc.Rules.Locations, out.State.Rules.Locations) ||
				!reflect.DeepEqual(inProc.Rules.Edges, out.State.Rules.Edges) {
				t.Fatalf("种子 %d 跨进程地图与进程内不一致:\n%v %v\n%v %v",
					seed,
					inProc.Rules.Locations, inProc.Rules.Edges,
					out.State.Rules.Locations, out.State.Rules.Edges)
			}
		})
	}
}

// TestGenerateDifferentSeedsCanYieldDifferentMapsAcrossProcesses 跨进程
// 验证种子确实参与选路：不同种子实际产生的地图集合中至少出现两种不同
// 的完整道路组合（以有序地点与道路列表作为指纹，而非只看数量）。
func TestGenerateDifferentSeedsCanYieldDifferentMapsAcrossProcesses(t *testing.T) {
	if os.Getenv("WORLD_TEST_HELPER") != "" {
		t.Skip("子进程模式")
	}
	req := GenerateRequest{
		Locations:    []string{"a", "b", "c", "d", "e", "f"},
		RoadCount:    8,
		RulesVersion: "v1",
		ItemKinds:    []string{"gold"},
	}
	seen := map[string]struct{}{}
	for seed := int64(0); seed < 12; seed++ {
		r := req
		r.Seed = seed
		out := runGenerateInFreshProcess(t, r)
		if !out.OK {
			t.Fatalf("种子 %d 生成失败: %s", seed, out.RuleError)
		}
		assertGeneratedConstraints(t, out, r)
		fp := fmt.Sprintf("%v|%v", out.State.Rules.Locations, out.State.Rules.Edges)
		seen[fp] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatal("跨进程生成的多个种子未产生至少两种不同的完整地图")
	}
}

// TestGenerateNonMapFieldsProcessIsolation 区分地图与其他世界数据：
// 规则版本、角色初始位置、物品种类或携带上限发生合法变化时，地图仍只由
// 种子和地图约束决定；新世界中的这些非地图内容按各自请求保留，时间片
// 从零开始。
func TestGenerateNonMapFieldsProcessIsolation(t *testing.T) {
	if os.Getenv("WORLD_TEST_HELPER") != "" {
		t.Skip("子进程模式")
	}
	base := baseGenerateRequest()

	variants := []GenerateRequest{
		base,
		func() GenerateRequest {
			v := cloneGenerateRequest(base)
			v.RulesVersion = "v9"
			return v
		}(),
		func() GenerateRequest {
			v := cloneGenerateRequest(base)
			v.ItemKinds = []string{"gold", "key", "rock"}
			return v
		}(),
		func() GenerateRequest {
			v := cloneGenerateRequest(base)
			v.CarryLimits = map[string]int{"hero": 99}
			return v
		}(),
		func() GenerateRequest {
			v := cloneGenerateRequest(base)
			v.Characters = []Character{
				{ID: "hero", Location: "yard", Items: []CharacterItem{{Item: "gold", Count: 2}}},
			}
			return v
		}(),
	}

	baseRes := runGenerateInFreshProcess(t, variants[0])
	if !baseRes.OK {
		t.Fatalf("基准生成失败: %s", baseRes.RuleError)
	}
	if baseRes.State.Time != 0 {
		t.Fatal("基准世界时间片应为 0")
	}
	for i := 1; i < len(variants); i++ {
		i := i
		t.Run(fmt.Sprintf("变体%d", i), func(t *testing.T) {
			res := runGenerateInFreshProcess(t, variants[i])
			if !res.OK {
				t.Fatalf("变体 %d 生成失败: %s", i, res.RuleError)
			}
			assertGeneratedConstraints(t, res, variants[i])
			// 地图只由种子和地图约束决定。
			if !reflect.DeepEqual(baseRes.State.Rules.Locations, res.State.Rules.Locations) ||
				!reflect.DeepEqual(baseRes.State.Rules.Edges, res.State.Rules.Edges) {
				t.Fatalf("变体 %d 的非地图字段变化影响了地图", i)
			}
			// 非地图内容按各自请求保留。
			st := res.State
			if st.Rules.Version != variants[i].RulesVersion {
				t.Fatalf("变体 %d 规则版本未保留: %q", i, st.Rules.Version)
			}
			if !reflect.DeepEqual(st.Rules.ItemKinds, variants[i].ItemKinds) {
				t.Fatalf("变体 %d 物品种类未保留: %v", i, st.Rules.ItemKinds)
			}
			if !reflect.DeepEqual(st.Rules.CarryLimits, variants[i].CarryLimits) {
				t.Fatalf("变体 %d 携带上限未保留: %v", i, st.Rules.CarryLimits)
			}
			if !reflect.DeepEqual(st.Characters, variants[i].Characters) {
				t.Fatalf("变体 %d 角色（含初始位置）未按请求保留: %+v vs %+v",
					i, st.Characters, variants[i].Characters)
			}
			if st.Time != 0 {
				t.Fatalf("变体 %d 时间片应从零开始，得到 %d", i, st.Time)
			}
		})
	}
}

// TestGenerateInvalidConstraintAcrossProcesses 沿用现有错误约定：跨进程
// 实际生成时，不合法的地图约束同样得到 *RuleError 且不产生世界，与进程
// 内行为一致——保障覆盖错误路径而不是只覆盖成功路径。
func TestGenerateInvalidConstraintAcrossProcesses(t *testing.T) {
	if os.Getenv("WORLD_TEST_HELPER") != "" {
		t.Skip("子进程模式")
	}
	invalid := []GenerateRequest{
		func() GenerateRequest {
			r := baseGenerateRequest()
			r.RoadCount = -1
			return r
		}(),
		func() GenerateRequest {
			r := baseGenerateRequest()
			r.Required = []Edge{{From: "hall", To: "void"}}
			return r
		}(),
		func() GenerateRequest {
			r := baseGenerateRequest()
			r.Locations = nil
			return r
		}(),
	}
	for i, req := range invalid {
		i, req := i, req
		t.Run(fmt.Sprintf("非法约束%d", i), func(t *testing.T) {
			out := runGenerateInFreshProcess(t, req)
			if out.OK {
				t.Fatalf("非法约束 %d 应失败，但子进程返回了世界", i)
			}
			if out.RuleError == "" {
				t.Fatalf("非法约束 %d 应返回 *RuleError，得到: %+v", i, out)
			}
			if out.State != nil {
				t.Fatalf("非法约束 %d 不应产生世界", i)
			}
			// 与进程内约定一致：进程内也必须是 *RuleError 且无世界。
			w, err := GenerateWorld(req)
			if err == nil {
				t.Fatalf("非法约束 %d 进程内应失败", i)
			}
			if w != nil {
				t.Fatalf("非法约束 %d 进程内也不应产生世界", i)
			}
			if _, ok := err.(*RuleError); !ok {
				t.Fatalf("非法约束 %d 进程内应返回 *RuleError，得到 %T", i, err)
			}
		})
	}
}
