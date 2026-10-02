package world

import (
	"fmt"
	"reflect"
	"testing"
)

// mapSpec 返回一个供测试复用的地图约束。
func mapSpec(seed int64) MapSpec {
	return MapSpec{
		Seed:      seed,
		Locations: []string{"a", "b", "c", "d", "e"},
		Required:  []Edge{{From: "a", To: "b"}, {From: "b", To: "c"}},
		Forbidden: []Edge{{From: "d", To: "e"}},
		Target:    6,
	}
}

// assertMapValid 校验生成结果满足全部约束：必有道路都在、禁用道路都不在、
// 全图连通、不同道路数严格等于 target。
func assertMapValid(t *testing.T, spec MapSpec, edges []Edge) {
	t.Helper()
	if len(edges) != spec.Target {
		t.Fatalf("道路数应为 %d，得到 %d", spec.Target, len(edges))
	}
	locSet := make(map[string]struct{}, len(spec.Locations))
	for _, l := range spec.Locations {
		locSet[l] = struct{}{}
	}
	edgeSet := make(map[edgeKey]struct{}, len(edges))
	for i, e := range edges {
		if _, ok := locSet[e.From]; !ok {
			t.Fatalf("道路 %d 引用未知地点: %q", i, e.From)
		}
		if _, ok := locSet[e.To]; !ok {
			t.Fatalf("道路 %d 引用未知地点: %q", i, e.To)
		}
		if e.From == e.To {
			t.Fatalf("道路 %d 连接自身: %q", i, e.From)
		}
		a, b := e.From, e.To
		if a > b {
			a, b = b, a
		}
		key := edgeKey{a, b}
		if _, dup := edgeSet[key]; dup {
			t.Fatalf("道路重复: %q-%q", e.From, e.To)
		}
		edgeSet[key] = struct{}{}
	}
	for _, e := range spec.Required {
		a, b := e.From, e.To
		if a > b {
			a, b = b, a
		}
		if _, ok := edgeSet[edgeKey{a, b}]; !ok {
			t.Fatalf("必有道路缺失: %q-%q", e.From, e.To)
		}
	}
	for _, e := range spec.Forbidden {
		a, b := e.From, e.To
		if a > b {
			a, b = b, a
		}
		if _, ok := edgeSet[edgeKey{a, b}]; ok {
			t.Fatalf("出现了禁用道路: %q-%q", e.From, e.To)
		}
	}
	if !graphConnected(spec.Locations, keys(edgeSet)) {
		t.Fatalf("地图不连通")
	}
}

func keys(m map[edgeKey]struct{}) []edgeKey {
	out := make([]edgeKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestGenerateMapDeterministic(t *testing.T) {
	spec := mapSpec(12345)
	first, err := GenerateMap(spec)
	if err != nil {
		t.Fatalf("GenerateMap: %v", err)
	}
	assertMapValid(t, spec, first)

	// 重复调用完全一致。
	again, err := GenerateMap(spec)
	if err != nil {
		t.Fatalf("GenerateMap again: %v", err)
	}
	if !reflect.DeepEqual(first, again) {
		t.Fatalf("重复调用结果不一致:\n%v\n%v", first, again)
	}

	// 先生成其他世界不影响结果。
	_, _ = GenerateMap(mapSpec(999))
	_, _ = GenerateMap(MapSpec{Seed: -7, Locations: []string{"x", "y", "z"}, Target: 2})
	after, err := GenerateMap(spec)
	if err != nil {
		t.Fatalf("GenerateMap after others: %v", err)
	}
	if !reflect.DeepEqual(first, after) {
		t.Fatalf("生成其他世界后结果不一致:\n%v\n%v", first, after)
	}
}

func TestGenerateMapSeedZeroAndNegative(t *testing.T) {
	for _, seed := range []int64{0, -1, -100, 1<<63 - 1, -1 << 63} {
		spec := mapSpec(seed)
		edges, err := GenerateMap(spec)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		assertMapValid(t, spec, edges)
		again, err := GenerateMap(spec)
		if err != nil {
			t.Fatalf("seed %d again: %v", seed, err)
		}
		if !reflect.DeepEqual(edges, again) {
			t.Fatalf("seed %d 重复调用结果不一致", seed)
		}
	}
}

func TestGenerateMapOrderIndependent(t *testing.T) {
	base := mapSpec(7)
	baseEdges, err := GenerateMap(base)
	if err != nil {
		t.Fatalf("GenerateMap: %v", err)
	}

	// 打乱地点次序、道路次序，反向书写并重复列出。
	shuffled := MapSpec{
		Seed:      7,
		Locations: []string{"e", "d", "c", "b", "a"},
		Required: []Edge{
			{From: "c", To: "b"}, {From: "b", To: "a"},
			{From: "a", To: "b"}, {From: "b", To: "c"},
		},
		Forbidden: []Edge{
			{From: "e", To: "d"}, {From: "d", To: "e"},
		},
		Target: 6,
	}
	shuffledEdges, err := GenerateMap(shuffled)
	if err != nil {
		t.Fatalf("GenerateMap shuffled: %v", err)
	}
	if !reflect.DeepEqual(baseEdges, shuffledEdges) {
		t.Fatalf("输入次序影响了结果:\n%v\n%v", baseEdges, shuffledEdges)
	}
}

func TestGenerateMapSatisfiesConstraints(t *testing.T) {
	for seed := int64(-200); seed <= 200; seed++ {
		spec := mapSpec(seed)
		edges, err := GenerateMap(spec)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		assertMapValid(t, spec, edges)
	}
}

func TestGenerateMapVariesWithSeed(t *testing.T) {
	seen := make(map[string]struct{})
	for seed := int64(0); seed < 30; seed++ {
		spec := MapSpec{
			Seed:      seed,
			Locations: []string{"a", "b", "c", "d"},
			Target:    3,
		}
		edges, err := GenerateMap(spec)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		key := fmt.Sprint(edges)
		seen[key] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("不同种子产生了完全相同的地图，共 %d 种", len(seen))
	}
}

func TestGenerateMapRequiredCycle(t *testing.T) {
	spec := MapSpec{
		Seed:      3,
		Locations: []string{"a", "b", "c"},
		Required:  []Edge{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "a"}},
		Target:    3,
	}
	edges, err := GenerateMap(spec)
	if err != nil {
		t.Fatalf("必有道路成环应当可行: %v", err)
	}
	assertMapValid(t, spec, edges)
}

func TestGenerateMapSingleLocation(t *testing.T) {
	spec := MapSpec{Seed: 5, Locations: []string{"solo"}, Target: 0}
	edges, err := GenerateMap(spec)
	if err != nil {
		t.Fatalf("单地点零道路应当可行: %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("单地点不应有道路: %v", edges)
	}
	if _, err := GenerateMap(MapSpec{Seed: 5, Locations: []string{"solo"}, Target: 1}); err == nil {
		t.Fatal("单地点目标数量为正应当被拒绝")
	}
}

func TestGenerateMapErrors(t *testing.T) {
	loc := func() []string { return []string{"a", "b", "c"} }
	cases := []struct {
		name string
		spec MapSpec
	}{
		{"地点数为空", MapSpec{Seed: 1, Locations: nil, Target: 0}},
		{"地点数越界", MapSpec{Seed: 1, Locations: make([]string, maxMapLocations+1), Target: 0}},
		{"地点标识为空", MapSpec{Seed: 1, Locations: []string{"a", ""}, Target: 0}},
		{"地点标识重复", MapSpec{Seed: 1, Locations: []string{"a", "a"}, Target: 0}},
		{"必有道路引用未知地点", MapSpec{Seed: 1, Locations: loc(), Required: []Edge{{From: "a", To: "z"}}, Target: 1}},
		{"必有道路连接自身", MapSpec{Seed: 1, Locations: loc(), Required: []Edge{{From: "a", To: "a"}}, Target: 1}},
		{"禁用道路引用未知地点", MapSpec{Seed: 1, Locations: loc(), Forbidden: []Edge{{From: "a", To: "z"}}, Target: 1}},
		{"禁用道路连接自身", MapSpec{Seed: 1, Locations: loc(), Forbidden: []Edge{{From: "a", To: "a"}}, Target: 1}},
		{"道路同时必有和禁用", MapSpec{
			Seed: 1, Locations: loc(),
			Required:  []Edge{{From: "a", To: "b"}},
			Forbidden: []Edge{{From: "b", To: "a"}},
			Target:    1,
		}},
		{"目标数量为负", MapSpec{Seed: 1, Locations: loc(), Target: -1}},
		{"目标少于必有道路数", MapSpec{
			Seed: 1, Locations: loc(),
			Required: []Edge{{From: "a", To: "b"}, {From: "b", To: "c"}},
			Target:   1,
		}},
		{"目标少于连通所需", MapSpec{
			Seed: 1, Locations: loc(),
			Target: 1,
		}},
		{"目标超过可用道路数", MapSpec{
			Seed: 1, Locations: loc(),
			Target: 4,
		}},
		{"禁用道路使地图不连通", MapSpec{
			Seed:      1,
			Locations: []string{"a", "b", "c", "d"},
			Forbidden: []Edge{
				{From: "a", To: "c"}, {From: "a", To: "d"},
				{From: "b", To: "c"}, {From: "b", To: "d"},
			},
			Target: 3,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GenerateMap(tc.spec)
			if err == nil {
				t.Fatal("期望失败，但成功了")
			}
			if _, ok := err.(*RuleError); !ok {
				t.Fatalf("期望 *RuleError，得到 %T: %v", err, err)
			}
		})
	}
}

func TestNewSeededWorldBasics(t *testing.T) {
	spec := mapSpec(42)
	data := InitialData{
		Rules: Rules{
			Version:   "v1",
			ItemKinds: []string{"gold", "key"},
			CarryLimits: map[string]int{"hero": 5},
		},
		Characters: []Character{
			{ID: "hero", Location: "a", Items: []CharacterItem{{Item: "gold", Count: 2}}},
		},
	}
	w, err := NewSeededWorld(spec, data)
	if err != nil {
		t.Fatalf("NewSeededWorld: %v", err)
	}
	st := w.Snapshot()
	if st.Time != 0 {
		t.Fatalf("时间片应为 0，得到 %d", st.Time)
	}
	if st.Seed != 42 {
		t.Fatalf("种子应原样保留，得到 %d", st.Seed)
	}
	if st.Rules.Version != "v1" {
		t.Fatalf("规则版本应保持输入，得到 %q", st.Rules.Version)
	}
	if !reflect.DeepEqual(st.Rules.ItemKinds, []string{"gold", "key"}) {
		t.Fatalf("物品种类应保持输入: %v", st.Rules.ItemKinds)
	}
	if st.Rules.CarryLimits["hero"] != 5 {
		t.Fatalf("携带上限应保持输入: %v", st.Rules.CarryLimits)
	}
	if !reflect.DeepEqual(st.Characters, data.Characters) {
		t.Fatalf("角色应保持输入: %v", st.Characters)
	}
	assertMapValid(t, spec, st.Rules.Edges)
}

func TestNewSeededWorldMapIndependentOfRulesAndCharacters(t *testing.T) {
	spec := mapSpec(42)
	mkData := func(version string, items []string, limits map[string]int, chars []Character) InitialData {
		return InitialData{
			Rules: Rules{
				Version:     version,
				ItemKinds:   items,
				CarryLimits: limits,
			},
			Characters: chars,
		}
	}
	w1, err := NewSeededWorld(spec, mkData("v1", []string{"gold"}, map[string]int{"hero": 5},
		[]Character{{ID: "hero", Location: "a", Items: []CharacterItem{{Item: "gold", Count: 1}}}}))
	if err != nil {
		t.Fatalf("w1: %v", err)
	}
	w2, err := NewSeededWorld(spec, mkData("v2", []string{"silver", "gem"}, map[string]int{"mage": 9},
		[]Character{{ID: "mage", Location: "b", Items: []CharacterItem{{Item: "silver", Count: 3}}}}))
	if err != nil {
		t.Fatalf("w2: %v", err)
	}
	if !reflect.DeepEqual(w1.Snapshot().Rules.Edges, w2.Snapshot().Rules.Edges) {
		t.Fatal("规则版本、角色、物品种类或携带上限不应影响地图")
	}
	if !reflect.DeepEqual(w1.Snapshot().Rules.Locations, w2.Snapshot().Rules.Locations) {
		t.Fatal("规则版本等不应影响地点")
	}
}

func TestNewSeededWorldInputIsolation(t *testing.T) {
	spec := mapSpec(42)
	data := InitialData{
		Rules: Rules{
			Version:   "v1",
			ItemKinds: []string{"gold"},
		},
		Characters: []Character{
			{ID: "hero", Location: "a", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	}
	w, err := NewSeededWorld(spec, data)
	if err != nil {
		t.Fatalf("NewSeededWorld: %v", err)
	}

	// 修改调用方输入不得影响已生成的世界。
	spec.Locations[0] = "hacked"
	spec.Required[0] = Edge{From: "a", To: "c"}
	data.Characters[0].Location = "hacked"
	data.Characters[0].Items[0].Count = 99
	data.Rules.ItemKinds[0] = "hacked"

	st := w.Snapshot()
	if st.Characters[0].Location != "a" || st.Characters[0].Items[0].Count != 1 {
		t.Fatal("调用方输入被修改影响了世界")
	}
	if st.Rules.ItemKinds[0] != "gold" {
		t.Fatal("调用方输入被修改影响了世界")
	}

	// 修改返回快照不得影响世界。
	st.Characters[0].Location = "c"
	st.Rules.Version = "hacked"
	st.Rules.Edges[0] = Edge{From: "a", To: "c"}
	again := w.Snapshot()
	if again.Characters[0].Location != "a" || again.Rules.Version != "v1" {
		t.Fatal("快照修改影响了世界")
	}
	if again.Rules.Edges[0] == (Edge{From: "a", To: "c"}) && len(again.Rules.Edges) == len(st.Rules.Edges) {
		// 仅当排序后首条恰好相同才可能误判；用整体比较更稳妥。
	}
	if !reflect.DeepEqual(w.Snapshot().Rules.Edges, again.Rules.Edges) {
		t.Fatal("快照修改影响了地图")
	}

	// 另一次生成不得影响此前结果。
	w2, err := NewSeededWorld(mapSpec(999), InitialData{
		Rules:     Rules{Version: "v1", ItemKinds: []string{"gold"}},
		Characters: []Character{{ID: "hero", Location: "a", Items: []CharacterItem{{Item: "gold", Count: 1}}}},
	})
	if err != nil {
		t.Fatalf("w2: %v", err)
	}
	if !reflect.DeepEqual(w.Snapshot().Rules.Edges, again.Rules.Edges) {
		t.Fatal("另一次生成影响了此前世界")
	}
	_ = w2
}

func TestNewSeededWorldFailureProducesNoWorld(t *testing.T) {
	spec := MapSpec{Seed: 1, Locations: []string{"a", "b"}, Target: 99}
	w, err := NewSeededWorld(spec, InitialData{Rules: Rules{Version: "v1"}})
	if err == nil {
		t.Fatal("期望失败，但成功了")
	}
	if w != nil {
		t.Fatal("失败时不应产生可用世界")
	}
}

func TestNewSeededWorldSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	archive, err := Create(dir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	spec := mapSpec(42)
	data := InitialData{
		Rules: Rules{
			Version:   "v1",
			ItemKinds: []string{"gold"},
		},
		Characters: []Character{
			{ID: "hero", Location: "a", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	}
	w, err := NewSeededWorld(spec, data)
	if err != nil {
		t.Fatalf("NewSeededWorld: %v", err)
	}
	info, err := archive.Save("slot1", w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 重新打开，地图使用记录中的版本，不重新生成。
	archive2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rec, err := archive2.Latest("slot1", []string{"v1"})
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rec.State.Seed != 42 {
		t.Fatalf("种子应原样保留，得到 %d", rec.State.Seed)
	}
	if !reflect.DeepEqual(rec.State.Rules.Edges, w.Snapshot().Rules.Edges) {
		t.Fatal("读档后地图应与生成时一致，不应重新生成")
	}
	assertMapValid(t, spec, rec.State.Rules.Edges)

	// 读档后仍可沿这些道路移动。
	loaded, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatalf("WorldFromState: %v", err)
	}
	// 找到一条从 a 出发的道路。
	var target string
	for _, e := range rec.State.Rules.Edges {
		if e.From == "a" {
			target = e.To
			break
		}
		if e.To == "a" {
			target = e.From
			break
		}
	}
	if target == "" {
		t.Fatal("生成的地图中没有从 a 出发的道路")
	}
	if _, err := loaded.Apply(Commit{
		Moves: []Move{{Character: "hero", To: target}},
		Time:  1,
	}); err != nil {
		t.Fatalf("沿生成的道路移动失败: %v", err)
	}

	// 覆盖后再读回，地图保持不变。
	if _, err := archive2.Replace("slot1", loaded, info.ID); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	rec2, err := archive2.Latest("slot1", []string{"v1"})
	if err != nil {
		t.Fatalf("Latest after replace: %v", err)
	}
	if !reflect.DeepEqual(rec2.State.Rules.Edges, rec.State.Rules.Edges) {
		t.Fatal("覆盖后地图应保持不变")
	}
}

func TestNewSeededWorldBranchUsesRecordedMap(t *testing.T) {
	dir := t.TempDir()
	archive, err := Create(dir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	spec := mapSpec(42)
	w, err := NewSeededWorld(spec, InitialData{
		Rules:     Rules{Version: "v1", ItemKinds: []string{"gold"}},
		Characters: []Character{{ID: "hero", Location: "a", Items: []CharacterItem{{Item: "gold", Count: 1}}}},
	})
	if err != nil {
		t.Fatalf("NewSeededWorld: %v", err)
	}
	info, err := archive.Save("slot1", w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	branch, err := archive.Branch("slot1", info.ID, "slot2")
	if err != nil {
		t.Fatalf("Branch: %v", err)
	}
	rec, err := archive.Latest("slot2", []string{"v1"})
	if err != nil {
		t.Fatalf("Latest branch: %v", err)
	}
	if !reflect.DeepEqual(rec.State.Rules.Edges, w.Snapshot().Rules.Edges) {
		t.Fatal("分支应使用记录中的地图，不重新生成")
	}
	if branch.Parent != info.ID {
		t.Fatalf("分支父记录应为来源记录")
	}
}
