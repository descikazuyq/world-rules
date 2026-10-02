package world

import (
	"math"
	"reflect"
	"testing"
)

func baseGenerateRequest() GenerateRequest {
	return GenerateRequest{
		Seed:         42,
		Locations:    []string{"hall", "yard", "cave", "dock"},
		Required:     []Edge{{From: "hall", To: "yard"}},
		Banned:       []Edge{{From: "cave", To: "dock"}},
		RoadCount:    4,
		RulesVersion: "v1",
		ItemKinds:    []string{"gold", "key"},
		CarryLimits:  map[string]int{"hero": 5},
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	}
}

func generateWorld(t *testing.T, req GenerateRequest) *World {
	t.Helper()
	w, err := GenerateWorld(req)
	if err != nil {
		t.Fatalf("GenerateWorld: %v", err)
	}
	return w
}

// edgeSet 把道路列表转成规范集合，便于比较。
func edgeSet(edges []Edge) map[Edge]struct{} {
	set := make(map[Edge]struct{}, len(edges))
	for _, e := range edges {
		set[canonicalEdge(e)] = struct{}{}
	}
	return set
}

// assertMapShape 检查生成地图的通用性质：道路数量、无重复、规范方向、
// 包含必有道路、避开禁用道路、全图连通。
func assertMapShape(t *testing.T, st State, req GenerateRequest) {
	t.Helper()
	edges := st.Rules.Edges
	if len(edges) != req.RoadCount {
		t.Fatalf("道路数量应为 %d，得到 %d: %v", req.RoadCount, len(edges), edges)
	}
	set := make(map[Edge]struct{}, len(edges))
	for _, e := range edges {
		if e.From >= e.To {
			t.Fatalf("道路方向不规范: %+v", e)
		}
		if _, dup := set[e]; dup {
			t.Fatalf("道路重复: %+v", e)
		}
		set[e] = struct{}{}
	}
	for _, r := range req.Required {
		if _, ok := set[canonicalEdge(r)]; !ok {
			t.Fatalf("必有道路 %+v 不在地图中: %v", r, edges)
		}
	}
	for _, b := range req.Banned {
		if _, ok := set[canonicalEdge(b)]; ok {
			t.Fatalf("禁用道路 %+v 出现在地图中: %v", b, edges)
		}
	}
	// 连通性：从任一地点出发应能到达全部地点。
	adj := make(map[string][]string)
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}
	seen := map[string]struct{}{st.Rules.Locations[0]: {}}
	queue := []string{st.Rules.Locations[0]}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range adj[cur] {
			if _, ok := seen[nb]; !ok {
				seen[nb] = struct{}{}
				queue = append(queue, nb)
			}
		}
	}
	if len(seen) != len(st.Rules.Locations) {
		t.Fatalf("地图不连通: 从 %q 只能到达 %d/%d 个地点",
			st.Rules.Locations[0], len(seen), len(st.Rules.Locations))
	}
}

func TestGenerateBasicProperties(t *testing.T) {
	req := baseGenerateRequest()
	st := generateWorld(t, req).Snapshot()
	if st.Time != 0 {
		t.Fatalf("时间片应为 0，得到 %d", st.Time)
	}
	if st.Seed != req.Seed {
		t.Fatalf("种子应原样保留为 %d，得到 %d", req.Seed, st.Seed)
	}
	if st.Rules.Version != "v1" {
		t.Fatalf("规则版本不符: %q", st.Rules.Version)
	}
	if !reflect.DeepEqual(st.Rules.ItemKinds, req.ItemKinds) {
		t.Fatalf("物品种类不符: %v", st.Rules.ItemKinds)
	}
	if !reflect.DeepEqual(st.Rules.CarryLimits, req.CarryLimits) {
		t.Fatalf("携带上限不符: %v", st.Rules.CarryLimits)
	}
	if !reflect.DeepEqual(st.Characters, req.Characters) {
		t.Fatalf("角色不符: %+v", st.Characters)
	}
	assertMapShape(t, st, req)
}

func TestGenerateDeterministic(t *testing.T) {
	req := baseGenerateRequest()
	first := generateWorld(t, req).Snapshot()

	// 重复调用结果一致。
	again := generateWorld(t, req).Snapshot()
	if !reflect.DeepEqual(first.Rules, again.Rules) {
		t.Fatalf("重复生成结果不同:\n%v\n%v", first.Rules.Edges, again.Rules.Edges)
	}

	// 先生成其他世界不影响结果。
	other := req
	other.Seed = 999
	_ = generateWorld(t, other)
	third := generateWorld(t, req).Snapshot()
	if !reflect.DeepEqual(first.Rules, third.Rules) {
		t.Fatal("生成其他世界后，同种子同约束的结果发生变化")
	}

	// 输入次序、重复道路与正反写法不影响结果。
	shuffled := req
	shuffled.Locations = []string{"dock", "cave", "yard", "hall"}
	shuffled.Required = []Edge{{From: "yard", To: "hall"}, {From: "hall", To: "yard"}}
	shuffled.Banned = []Edge{{From: "dock", To: "cave"}, {From: "cave", To: "dock"}, {From: "cave", To: "dock"}}
	fourth := generateWorld(t, shuffled).Snapshot()
	if !reflect.DeepEqual(first.Rules.Locations, fourth.Rules.Locations) {
		t.Fatalf("地点输入次序影响结果: %v vs %v", first.Rules.Locations, fourth.Rules.Locations)
	}
	if !reflect.DeepEqual(first.Rules.Edges, fourth.Rules.Edges) {
		t.Fatalf("道路输入次序/重复/正反影响结果: %v vs %v", first.Rules.Edges, fourth.Rules.Edges)
	}
}

func TestGenerateSeedExtremes(t *testing.T) {
	seeds := []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 42, -42}
	for _, seed := range seeds {
		req := baseGenerateRequest()
		req.Seed = seed
		st := generateWorld(t, req).Snapshot()
		if st.Seed != seed {
			t.Fatalf("种子 %d 未原样保留，得到 %d", seed, st.Seed)
		}
		assertMapShape(t, st, req)
	}
}

func TestGenerateDifferentSeedsDifferentMaps(t *testing.T) {
	seen := make(map[string]struct{})
	for seed := int64(0); seed < 20; seed++ {
		req := GenerateRequest{
			Seed:         seed,
			Locations:    []string{"a", "b", "c", "d", "e", "f"},
			RoadCount:    8,
			RulesVersion: "v1",
		}
		st := generateWorld(t, req).Snapshot()
		key := ""
		for _, e := range st.Rules.Edges {
			key += e.From + "-" + e.To + ";"
		}
		seen[key] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatal("20 个不同种子产生了完全相同的地图")
	}
}

func TestGenerateNonMapFieldsDoNotAffectMap(t *testing.T) {
	req := baseGenerateRequest()
	base := generateWorld(t, req).Snapshot()

	variant := req
	variant.RulesVersion = "v9"
	variant.ItemKinds = []string{"rock"}
	variant.CarryLimits = map[string]int{"hero": 99}
	variant.Characters = []Character{{ID: "hero", Location: "yard"}}
	st := generateWorld(t, variant).Snapshot()
	if !reflect.DeepEqual(base.Rules.Locations, st.Rules.Locations) ||
		!reflect.DeepEqual(base.Rules.Edges, st.Rules.Edges) {
		t.Fatal("规则版本、角色、物品种类或携带上限影响了地图生成")
	}
}

func TestGenerateRequiredCycle(t *testing.T) {
	req := GenerateRequest{
		Seed:      7,
		Locations: []string{"a", "b", "c", "d"},
		Required: []Edge{
			{From: "a", To: "b"},
			{From: "b", To: "c"},
			{From: "c", To: "a"},
		},
		RoadCount:    4,
		RulesVersion: "v1",
	}
	st := generateWorld(t, req).Snapshot()
	assertMapShape(t, st, req)
}

func TestGenerateSingleLocation(t *testing.T) {
	req := GenerateRequest{
		Seed:         3,
		Locations:    []string{"only"},
		RoadCount:    0,
		RulesVersion: "v1",
	}
	st := generateWorld(t, req).Snapshot()
	if len(st.Rules.Edges) != 0 {
		t.Fatalf("单地点不应有道路: %v", st.Rules.Edges)
	}

	req.RoadCount = 1
	if _, err := GenerateWorld(req); err == nil {
		t.Fatal("单地点要求一条道路应失败")
	} else if _, ok := err.(*RuleError); !ok {
		t.Fatalf("期望 *RuleError，得到 %T", err)
	}
}

func TestGenerateErrors(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*GenerateRequest)
	}{
		{"没有地点", func(r *GenerateRequest) { r.Locations = nil }},
		{"地点过多", func(r *GenerateRequest) {
			locs := make([]string, 257)
			for i := range locs {
				locs[i] = string(rune('a'+i%26)) + string(rune('a'+i/26))
			}
			r.Locations = locs
		}},
		{"地点标识为空", func(r *GenerateRequest) { r.Locations[0] = "" }},
		{"地点重复", func(r *GenerateRequest) { r.Locations[1] = r.Locations[0] }},
		{"必有道路引用未知地点", func(r *GenerateRequest) {
			r.Required = []Edge{{From: "hall", To: "void"}}
		}},
		{"禁用道路引用未知地点", func(r *GenerateRequest) {
			r.Banned = []Edge{{From: "void", To: "yard"}}
		}},
		{"必有道路连接自身", func(r *GenerateRequest) {
			r.Required = []Edge{{From: "hall", To: "hall"}}
		}},
		{"禁用道路连接自身", func(r *GenerateRequest) {
			r.Banned = []Edge{{From: "cave", To: "cave"}}
		}},
		{"同一道路必有且禁用", func(r *GenerateRequest) {
			r.Banned = []Edge{{From: "yard", To: "hall"}}
		}},
		{"目标数量为负", func(r *GenerateRequest) { r.RoadCount = -1 }},
		{"目标小于必有道路数", func(r *GenerateRequest) {
			r.Required = []Edge{{From: "hall", To: "yard"}, {From: "cave", To: "dock"}}
			r.RoadCount = 1
		}},
		{"目标少于连通所需", func(r *GenerateRequest) { r.RoadCount = 2 }},
		{"目标超过可用道路数", func(r *GenerateRequest) {
			r.Banned = nil
			r.RoadCount = 7 // 4 个地点最多 6 条
		}},
		{"禁用道路使地图无法连通", func(r *GenerateRequest) {
			r.Required = nil
			r.Banned = []Edge{
				{From: "hall", To: "yard"},
				{From: "hall", To: "cave"},
				{From: "hall", To: "dock"},
			}
		}},
		{"必有成环抬高连通下限", func(r *GenerateRequest) {
			// 必有道路在 hall/yard/cave 上成环，连通 dock 至少还需 1 条，
			// 因此最少需要 4 条；目标 3 条不可行。
			r.Required = []Edge{
				{From: "hall", To: "yard"},
				{From: "yard", To: "cave"},
				{From: "cave", To: "hall"},
			}
			r.RoadCount = 3
		}},
		{"规则版本为空", func(r *GenerateRequest) { r.RulesVersion = "" }},
		{"角色在不存在的地点", func(r *GenerateRequest) {
			r.Characters = []Character{{ID: "hero", Location: "void"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := baseGenerateRequest()
			tc.mod(&req)
			if _, err := GenerateWorld(req); err == nil {
				t.Fatal("期望生成失败，但成功了")
			} else if _, ok := err.(*RuleError); !ok {
				t.Fatalf("期望 *RuleError，得到 %T: %v", err, err)
			}
		})
	}
}

func TestGenerateEverySeedSucceedsWhenSolvable(t *testing.T) {
	// 约束有解时，每个种子都应成功且满足全部约束。
	for seed := int64(-50); seed < 50; seed++ {
		req := baseGenerateRequest()
		req.Seed = seed
		st, err := GenerateWorld(req)
		if err != nil {
			t.Fatalf("种子 %d 生成失败: %v", seed, err)
		}
		assertMapShape(t, st.Snapshot(), req)
	}
}

func TestGenerateInputIsolation(t *testing.T) {
	req := baseGenerateRequest()
	w := generateWorld(t, req)
	before := w.Snapshot()

	// 生成后修改请求内容不得影响已生成的世界。
	req.Locations[0] = "mutated"
	req.Required[0] = Edge{From: "x", To: "y"}
	req.Banned[0] = Edge{From: "x", To: "y"}
	req.ItemKinds[0] = "mutated"
	req.CarryLimits["hero"] = -100
	req.Characters[0].Location = "mutated"
	req.Characters[0].Items[0].Count = 99
	after := w.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("修改请求输入影响到了已生成的世界")
	}

	// 修改快照不得影响世界；另一次生成不得影响此前结果。
	snap := w.Snapshot()
	snap.Rules.Edges[0] = Edge{From: "a", To: "b"}
	other := baseGenerateRequest()
	other.Seed = 7
	_ = generateWorld(t, other)
	if !reflect.DeepEqual(before, w.Snapshot()) {
		t.Fatal("修改快照或另一次生成影响到了此前的世界")
	}
}

func TestGenerateFailureProducesNoWorld(t *testing.T) {
	req := baseGenerateRequest()
	req.RoadCount = -5
	w, err := GenerateWorld(req)
	if err == nil {
		t.Fatal("期望失败")
	}
	if w != nil {
		t.Fatal("失败时不应产生可用世界")
	}
}

func TestGeneratedWorldApplyAndArchive(t *testing.T) {
	req := baseGenerateRequest()
	w := generateWorld(t, req)
	st := w.Snapshot()

	// 沿生成的道路移动。
	e := st.Rules.Edges[0]
	hero := st.Characters[0]
	from, to := e.From, e.To
	if hero.Location == to {
		from, to = to, from
	}
	// 先把 hero 放到道路一端（直接重建世界，避免多步移动）。
	req.Characters = []Character{{ID: "hero", Location: from, Items: []CharacterItem{{Item: "gold", Count: 1}}}}
	w = generateWorld(t, req)
	if _, err := w.Apply(Commit{Moves: []Move{{Character: "hero", To: to}}, Time: 1}); err != nil {
		t.Fatalf("沿生成道路移动失败: %v", err)
	}

	// 存档后重新打开，地图来自记录而不是重新生成。
	dir := t.TempDir()
	a, err := Create(dir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	info, err := a.Save("slot", w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	a2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rec, err := a2.Record("slot", info.ID, []string{"v1"})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if !reflect.DeepEqual(st.Rules.Locations, rec.State.Rules.Locations) ||
		!reflect.DeepEqual(w.Snapshot().Rules.Edges, rec.State.Rules.Edges) {
		t.Fatal("重新打开后地图与记录不符")
	}
	// 读档重建的世界仍可按这些道路移动（沿同一条道路返回）。
	w2, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatalf("WorldFromState: %v", err)
	}
	if _, err := w2.Apply(Commit{Moves: []Move{{Character: "hero", To: from}}, Time: 2}); err != nil {
		t.Fatalf("读档后沿道路移动失败: %v", err)
	}
}

func TestNewWorldUnaffectedByGenerate(t *testing.T) {
	// 旧入口不施加生成地图的限制：地点数量、道路连通性等仍按原规则。
	w, err := NewWorld(InitialData{
		Seed: 1,
		Rules: Rules{
			Version:   "v1",
			Locations: []string{"a", "b", "c"},
			Edges:     []Edge{{From: "a", To: "b"}}, // c 不连通也允许
		},
	})
	if err != nil {
		t.Fatalf("NewWorld 行为不应改变: %v", err)
	}
	if got := len(w.Snapshot().Rules.Edges); got != 1 {
		t.Fatalf("NewWorld 道路数量不符: %d", got)
	}
}
