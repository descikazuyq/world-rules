package world

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"reflect"
	"testing"
)

// 本文件的测试覆盖跨进程生成一致性：每个用例都通过独立的子进程执行
// 完整的 GenerateWorld 调用（完整请求、建立新世界），再比较各进程实际
// 生成的地图结果。子进程由 TestGenerateWorldHelperProcess 实现，只在
// 设置 GO_WORLD_GENERATE_HELPER=1 时真正工作，否则直接返回。

// helperProcessEnv 是启用子进程生成模式的环境变量。
const helperProcessEnv = "GO_WORLD_GENERATE_HELPER"

// TestGenerateWorldHelperProcess 是跨进程生成测试的子进程入口。它从标准
// 输入读取一个 JSON 编码的 GenerateRequest，调用 GenerateWorld 建立新
// 世界，并把生成世界的快照以 JSON 写到标准输出。普通测试运行时（未设置
// 环境变量）它立即返回，不产生任何影响。
func TestGenerateWorldHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取请求失败: %v\n", err)
		os.Exit(1)
	}
	var req GenerateRequest
	if err := json.Unmarshal(data, &req); err != nil {
		fmt.Fprintf(os.Stderr, "解析请求失败: %v\n", err)
		os.Exit(1)
	}
	w, err := GenerateWorld(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GenerateWorld 失败: %v\n", err)
		os.Exit(1)
	}
	out, err := json.Marshal(w.Snapshot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "编码结果失败: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintf(os.Stderr, "写出结果失败: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// generateWorldInSeparateProcess 在一个全新的独立进程中按给定请求调用
// GenerateWorld 并返回生成世界的快照。每次调用都启动新进程，进程内只
// 处理这一个请求，因此比较结果反映的是真正的跨进程生成行为。
func generateWorldInSeparateProcess(t *testing.T, req GenerateRequest) State {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("编码请求失败: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestGenerateWorldHelperProcess$")
	cmd.Env = append(os.Environ(), helperProcessEnv+"=1")
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("子进程生成失败: %v\nstderr: %s", err, stderr.String())
	}
	var st State
	if err := json.Unmarshal(stdout.Bytes(), &st); err != nil {
		t.Fatalf("解析子进程结果失败: %v\n输出: %s", err, stdout.String())
	}
	return st
}

// assertSameMap 断言两个状态的地点列表与道路列表完全一致，包括列表
// 顺序与道路端点的方向表示。
func assertSameMap(t *testing.T, what string, a, b State) {
	t.Helper()
	if !reflect.DeepEqual(a.Rules.Locations, b.Rules.Locations) {
		t.Fatalf("%s: 地点列表不一致:\n%v\n%v", what, a.Rules.Locations, b.Rules.Locations)
	}
	if !reflect.DeepEqual(a.Rules.Edges, b.Rules.Edges) {
		t.Fatalf("%s: 道路列表不一致:\n%v\n%v", what, a.Rules.Edges, b.Rules.Edges)
	}
}

// multiSolutionGenerateRequest 返回一个存在多种合法道路组合的生成请求：
// 6 个地点、1 条必有道路、2 条禁用道路、目标 8 条道路，候选道路 13 条
// 中选 7 条补足，合法组合很多。
func multiSolutionGenerateRequest() GenerateRequest {
	return GenerateRequest{
		Seed:         42,
		Locations:    []string{"hall", "yard", "cave", "dock", "mill", "gate"},
		Required:     []Edge{{From: "hall", To: "yard"}},
		Banned:       []Edge{{From: "cave", To: "dock"}, {From: "mill", To: "gate"}},
		RoadCount:    8,
		RulesVersion: "v1",
		ItemKinds:    []string{"gold", "key"},
		CarryLimits:  map[string]int{"hero": 5},
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	}
}

// assertMultipleLegalMaps 确认给定的地图约束确实存在多种合法结果
// （不同种子产生不同地图），保证一致性比较覆盖的是有选择空间的地图。
func assertMultipleLegalMaps(t *testing.T, req GenerateRequest) {
	t.Helper()
	seen := make(map[string]struct{})
	for seed := int64(0); seed < 10; seed++ {
		r := req
		r.Seed = seed
		st := generateWorld(t, r).Snapshot()
		key, err := json.Marshal(st.Rules.Edges)
		if err != nil {
			t.Fatalf("编码道路失败: %v", err)
		}
		seen[string(key)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatal("该地图约束下不同种子产生了完全相同的地图，无法覆盖多种合法组合")
	}
}

func TestGenerateCrossProcessDeterministic(t *testing.T) {
	req := multiSolutionGenerateRequest()
	assertMultipleLegalMaps(t, req)

	// 两个互不知情的独立进程分别按同一完整请求生成。
	first := generateWorldInSeparateProcess(t, req)
	second := generateWorldInSeparateProcess(t, req)
	assertSameMap(t, "两个独立进程", first, second)

	// 每个进程生成的地图都必须满足原有约束。
	assertMapShape(t, first, req)
	assertMapShape(t, second, req)

	// 跨进程结果与本进程内生成结果也一致。
	inProcess := generateWorld(t, req).Snapshot()
	assertSameMap(t, "子进程与本进程", first, inProcess)
}

func TestGenerateCrossProcessEquivalentConstraints(t *testing.T) {
	req := multiSolutionGenerateRequest()
	base := generateWorldInSeparateProcess(t, req)

	// 等价的地图约束：调换地点与道路输入顺序、重复列出同一道路、
	// 把道路两端反写。每个变体都在独立进程中生成，结果必须一致。
	variants := map[string]GenerateRequest{
		"调换输入顺序": func() GenerateRequest {
			r := req
			r.Locations = []string{"gate", "mill", "dock", "cave", "yard", "hall"}
			r.Required = []Edge{{From: "yard", To: "hall"}}
			r.Banned = []Edge{{From: "gate", To: "mill"}, {From: "dock", To: "cave"}}
			return r
		}(),
		"重复列出道路": func() GenerateRequest {
			r := req
			r.Required = []Edge{{From: "hall", To: "yard"}, {From: "hall", To: "yard"}}
			r.Banned = []Edge{
				{From: "cave", To: "dock"}, {From: "mill", To: "gate"},
				{From: "cave", To: "dock"},
			}
			return r
		}(),
		"道路两端反写": func() GenerateRequest {
			r := req
			r.Required = []Edge{{From: "yard", To: "hall"}}
			r.Banned = []Edge{{From: "dock", To: "cave"}, {From: "gate", To: "mill"}}
			return r
		}(),
	}
	for name, variant := range variants {
		st := generateWorldInSeparateProcess(t, variant)
		assertSameMap(t, "等价约束变体「"+name+"」", base, st)
		assertMapShape(t, st, req)
	}
}

func TestGenerateCrossProcessRequiredCycle(t *testing.T) {
	// 必有道路在 a/b/c 之间成环，d/e/f 仍需连入；目标 7 条道路留下
	// 多种合法组合。
	req := GenerateRequest{
		Seed:      7,
		Locations: []string{"a", "b", "c", "d", "e", "f"},
		Required: []Edge{
			{From: "a", To: "b"},
			{From: "b", To: "c"},
			{From: "c", To: "a"},
		},
		RoadCount:    7,
		RulesVersion: "v1",
	}
	assertMultipleLegalMaps(t, req)

	first := generateWorldInSeparateProcess(t, req)
	second := generateWorldInSeparateProcess(t, req)
	assertSameMap(t, "必有成环约束的两个独立进程", first, second)
	assertMapShape(t, first, req)
	assertMapShape(t, second, req)
}

func TestGenerateCrossProcessSeedExtremes(t *testing.T) {
	seeds := []int64{0, -1, math.MinInt64, math.MaxInt64, 42}
	for _, seed := range seeds {
		req := multiSolutionGenerateRequest()
		req.Seed = seed
		first := generateWorldInSeparateProcess(t, req)
		second := generateWorldInSeparateProcess(t, req)
		if first.Seed != seed || second.Seed != seed {
			t.Fatalf("种子 %d 未原样保留: %d / %d", seed, first.Seed, second.Seed)
		}
		assertSameMap(t, "种子 "+fmt.Sprint(seed)+" 的两个独立进程", first, second)
		assertMapShape(t, first, req)
		assertMapShape(t, second, req)
	}
}

func TestGenerateCrossProcessNonMapFields(t *testing.T) {
	// 地图约束相同，但规则版本、角色、物品种类与携带上限不同的两个
	// 请求，分别在独立进程中生成：地图必须一致，非地图内容各自保留。
	reqA := multiSolutionGenerateRequest()
	reqB := multiSolutionGenerateRequest()
	reqB.RulesVersion = "v9"
	reqB.ItemKinds = []string{"rock"}
	reqB.CarryLimits = map[string]int{"hero": 99}
	reqB.Characters = []Character{{ID: "hero", Location: "yard"}}

	stA := generateWorldInSeparateProcess(t, reqA)
	stB := generateWorldInSeparateProcess(t, reqB)
	assertSameMap(t, "非地图内容不同的两个进程", stA, stB)

	if stA.Time != 0 || stB.Time != 0 {
		t.Fatalf("时间片应从零开始: %d / %d", stA.Time, stB.Time)
	}
	if stA.Rules.Version != "v1" || stB.Rules.Version != "v9" {
		t.Fatalf("规则版本未按各自请求保留: %q / %q", stA.Rules.Version, stB.Rules.Version)
	}
	if !reflect.DeepEqual(stA.Rules.ItemKinds, reqA.ItemKinds) ||
		!reflect.DeepEqual(stB.Rules.ItemKinds, reqB.ItemKinds) {
		t.Fatalf("物品种类未按各自请求保留: %v / %v", stA.Rules.ItemKinds, stB.Rules.ItemKinds)
	}
	if !reflect.DeepEqual(stA.Rules.CarryLimits, reqA.CarryLimits) ||
		!reflect.DeepEqual(stB.Rules.CarryLimits, reqB.CarryLimits) {
		t.Fatalf("携带上限未按各自请求保留: %v / %v", stA.Rules.CarryLimits, stB.Rules.CarryLimits)
	}
	if !reflect.DeepEqual(stA.Characters, reqA.Characters) ||
		!reflect.DeepEqual(stB.Characters, reqB.Characters) {
		t.Fatalf("角色未按各自请求保留: %+v / %+v", stA.Characters, stB.Characters)
	}
	assertMapShape(t, stA, reqA)
	assertMapShape(t, stB, reqB)
}
