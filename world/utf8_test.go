package world

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// invalidBytes 是两个不同的非法 UTF-8 字节串：它们在内存中互不相等，
// 但经 encoding/json 保存都会被改写成同一个替换字符“�”。这正是必须在
// 接纳世界数据时拒绝、而不能等读档才暴露问题的根因。
var (
	invalidA = "a\xff\xfe"
	invalidB = "a\xfe\xff"
)

func assertInvalidRuleError(t *testing.T, err error, category string) {
	t.Helper()
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("应返回 *RuleError，得到 %T: %v", err, err)
	}
	if !strings.Contains(re.Reason, "UTF-8") {
		t.Fatalf("错误应说明是 UTF-8 合法性问题: %v", re)
	}
	if category != "" && !strings.Contains(re.Reason, category) {
		t.Fatalf("错误应指出字段类别 %q: %v", category, re)
	}
}

// TestNewWorldRejectsInvalidUTF8 规则与角色状态中的每一类文本标识出现
// 非法 UTF-8 字节时，NewWorld 都必须返回 *RuleError 且不产生世界，包括
// 暂时无人使用的地点、物品种类和没有现存角色的携带上限键。
func TestNewWorldRejectsInvalidUTF8(t *testing.T) {
	cases := []struct {
		name     string
		category string
		mod      func(*InitialData)
	}{
		{"规则版本", "规则版本", func(d *InitialData) { d.Rules.Version = invalidA }},
		{"无人使用的地点", "地点", func(d *InitialData) {
			d.Rules.Locations = append(d.Rules.Locations, invalidA)
		}},
		{"道路起点", "连通关系", func(d *InitialData) {
			d.Rules.Edges[0].From = invalidA
		}},
		{"道路终点", "连通关系", func(d *InitialData) {
			d.Rules.Edges[0].To = invalidA
		}},
		{"无人使用的物品种类", "物品种类", func(d *InitialData) {
			d.Rules.ItemKinds = append(d.Rules.ItemKinds, invalidA)
		}},
		{"没有现存角色的携带上限键", "携带上限", func(d *InitialData) {
			d.Rules.CarryLimits[invalidA] = 1
		}},
		{"角色标识", "角色", func(d *InitialData) { d.Characters[0].ID = invalidA }},
		{"角色所在地点", "地点", func(d *InitialData) { d.Characters[0].Location = invalidA }},
		{"角色所持物品名称", "物品", func(d *InitialData) {
			d.Characters[0].Items[0].Item = invalidA
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := InitialData{
				Seed:       1,
				Rules:      baseRules(),
				Characters: []Character{{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}}},
			}
			tc.mod(&data)
			w, err := NewWorld(data)
			if w != nil {
				t.Fatalf("非法输入不应产生可用世界，得到 %+v", w)
			}
			assertInvalidRuleError(t, err, tc.category)
		})
	}
}

// TestNewWorldRejectsDistinctInvalidByteNames 两个原本不同的无效字节串
// 作为不同地点本可通过旧校验，但保存成 JSON 后会重名并使新记录无法读取。
// 修复后建立世界时就必须拒绝。
func TestNewWorldRejectsDistinctInvalidByteNames(t *testing.T) {
	if invalidA == invalidB {
		t.Fatal("测试前提错误：两个无效字节串应当不同")
	}
	_, err := NewWorld(InitialData{
		Seed:  1,
		Rules: Rules{Version: "v1", Locations: []string{invalidA, invalidB}},
	})
	assertInvalidRuleError(t, err, "地点")
}

// TestNewWorldInvalidUTF8DoesNotMutateInput 拒绝操作不能修改调用方传入的
// 规则或状态：错误返回后入参中的非法字节必须原样保留。
func TestNewWorldInvalidUTF8DoesNotMutateInput(t *testing.T) {
	data := InitialData{
		Seed:       1,
		Rules:      baseRules(),
		Characters: []Character{{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}}},
	}
	data.Rules.CarryLimits[invalidA] = 3
	if _, err := NewWorld(data); err == nil {
		t.Fatal("非法携带上限键应被拒绝")
	}
	got, ok := data.Rules.CarryLimits[invalidA]
	if !ok || got != 3 {
		t.Fatalf("入参被修改: %+v", data.Rules.CarryLimits)
	}
	if utf8.ValidString(invalidA) {
		t.Fatal("测试键本身被改写")
	}

	// 非法版本同样原样保留。
	data2 := InitialData{Rules: Rules{Version: invalidA, Locations: []string{"hall"}}}
	if _, err := NewWorld(data2); err == nil {
		t.Fatal("非法版本应被拒绝")
	}
	if data2.Rules.Version != invalidA {
		t.Fatalf("入参版本被改写: %q", data2.Rules.Version)
	}
}

// TestWorldFromStateRejectsInvalidUTF8 从完整状态重建世界遇到任何无效
// 文本都返回 *RuleError，不返回可用世界。
func TestWorldFromStateRejectsInvalidUTF8(t *testing.T) {
	st := State{
		Seed:       3,
		Rules:      baseRules(),
		Time:       2,
		Characters: []Character{{ID: "hero", Location: "hall"}},
	}
	st.Rules.ItemKinds = append(st.Rules.ItemKinds, invalidB)
	w, err := WorldFromState(st)
	if w != nil {
		t.Fatalf("非法状态不应重建出世界: %+v", w)
	}
	assertInvalidRuleError(t, err, "物品种类")

	st = State{
		Seed:       3,
		Rules:      baseRules(),
		Time:       2,
		Characters: []Character{{ID: "hero", Location: "hall"}, {ID: invalidB, Location: "yard"}},
	}
	if _, err := WorldFromState(st); err == nil {
		t.Fatal("非法角色标识应被拒绝")
	} else {
		assertInvalidRuleError(t, err, "角色")
	}
}

// TestGenerateWorldRejectsInvalidUTF8 按种子生成世界时，地图输入（地点、
// 必有/禁用道路端点）以及规则版本、物品种类、携带上限键和角色中的非法
// UTF-8 都必须返回 *RuleError，不产生可用世界。
func TestGenerateWorldRejectsInvalidUTF8(t *testing.T) {
	validChars := []Character{{ID: "hero", Location: "hall"}}

	good := func() GenerateRequest {
		return GenerateRequest{
			Seed:         7,
			Locations:    []string{"hall", "yard"},
			Required:     []Edge{{From: "hall", To: "yard"}},
			RoadCount:    1,
			RulesVersion: "v1",
			ItemKinds:    []string{"gold"},
			Characters:   validChars,
		}
	}

	t.Run("地点", func(t *testing.T) {
		req := good()
		req.Locations = append(req.Locations, invalidA)
		w, err := GenerateWorld(req)
		if w != nil {
			t.Fatalf("非法地点不应产生世界: %+v", w)
		}
		assertInvalidRuleError(t, err, "地点")
	})
	t.Run("必有道路端点", func(t *testing.T) {
		req := good()
		req.Required = []Edge{{From: "hall", To: invalidA}}
		if _, err := GenerateWorld(req); err == nil {
			t.Fatal("非法必有道路端点应被拒绝")
		} else {
			assertInvalidRuleError(t, err, "必有道路")
		}
	})
	t.Run("禁用道路端点", func(t *testing.T) {
		req := good()
		req.Banned = []Edge{{From: invalidA, To: "yard"}}
		if _, err := GenerateWorld(req); err == nil {
			t.Fatal("非法禁用道路端点应被拒绝")
		} else {
			assertInvalidRuleError(t, err, "禁用道路")
		}
	})
	t.Run("规则版本", func(t *testing.T) {
		req := good()
		req.RulesVersion = invalidA
		if _, err := GenerateWorld(req); err == nil {
			t.Fatal("非法规则版本应被拒绝")
		} else {
			assertInvalidRuleError(t, err, "规则版本")
		}
	})
	t.Run("无人使用的物品种类", func(t *testing.T) {
		req := good()
		req.ItemKinds = []string{"gold", invalidA}
		if _, err := GenerateWorld(req); err == nil {
			t.Fatal("非法物品种类应被拒绝")
		} else {
			assertInvalidRuleError(t, err, "物品种类")
		}
	})
	t.Run("无现存角色的携带上限键", func(t *testing.T) {
		req := good()
		req.CarryLimits = map[string]int{invalidA: 4}
		if _, err := GenerateWorld(req); err == nil {
			t.Fatal("非法携带上限键应被拒绝")
		} else {
			assertInvalidRuleError(t, err, "携带上限")
		}
	})
	t.Run("角色标识", func(t *testing.T) {
		req := good()
		req.Characters = []Character{{ID: invalidA, Location: "hall"}}
		if _, err := GenerateWorld(req); err == nil {
			t.Fatal("非法角色标识应被拒绝")
		} else {
			assertInvalidRuleError(t, err, "角色")
		}
	})
}

// unicodeRules 用中文、表情符号和真实存在的替换字符“�”命名全部文本
// 标识，包括没有现存角色的携带上限键。它们都是合法 UTF-8，应按普通
// 标识接受，仍需满足非空、唯一、引用等原有条件。
func unicodeRules() Rules {
	return Rules{
		Version:   "规则一😀/v�",
		Locations: []string{"大厅", "院子", "洞穴"},
		Edges: []Edge{
			{From: "大厅", To: "院子"},
			{From: "院子", To: "洞穴"},
		},
		ItemKinds:   []string{"金币", "钥匙🗝", "替换符�"},
		CarryLimits: map[string]int{"英雄": 5, "法师🧙": 2, "幽灵👻": 3},
	}
}

func unicodeState() State {
	return State{
		Seed:  2026,
		Rules: unicodeRules(),
		Time:  4,
		Characters: []Character{
			{ID: "英雄", Location: "大厅", Items: []CharacterItem{
				{Item: "金币", Count: 1},
				{Item: "钥匙🗝", Count: 1},
			}},
			{ID: "法师🧙", Location: "院子", Items: []CharacterItem{
				{Item: "金币", Count: 2},
			}},
			{ID: "替�身", Location: "洞穴", Items: []CharacterItem{
				{Item: "替换符�", Count: 0},
			}},
		},
	}
}

// TestValidUnicodeAcceptedAndRoundTrips 合法 UTF-8（中文、表情符号、真实
// 的“�”字符）作为各类标识都被接受；世界保存再读取后，文本内容、角色和
// 物品排列与保存前完全一致。
func TestValidUnicodeAcceptedAndRoundTrips(t *testing.T) {
	original := unicodeState()
	w, err := WorldFromState(original)
	if err != nil {
		t.Fatalf("含合法 Unicode 标识的状态应被接受: %v", err)
	}
	before := w.Snapshot()
	if !reflect.DeepEqual(before, original) {
		t.Fatalf("建立后状态与输入不一致:\n got %+v\nwant %+v", before, original)
	}

	a, _ := newTestArchive(t)
	info, err := a.Save("槽一", w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	rec, err := a.Latest("槽一", []string{"规则一😀/v�"})
	if err != nil {
		t.Fatalf("合法 Unicode 世界保存后应可读: %v", err)
	}
	if !reflect.DeepEqual(rec.State, original) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec.State, original)
	}

	// 用读回的完整状态重建世界，再覆盖保存一次，排列与内容仍须一致。
	w2, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatalf("读回状态重建世界失败: %v", err)
	}
	if _, err := w2.Apply(Commit{
		Moves: []Move{{Character: "英雄", To: "院子"}},
		Time:  5,
	}); err != nil {
		t.Fatalf("合法 Unicode 道路上的移动应成功: %v", err)
	}
	info2, err := a.Replace("槽一", w2, info.ID)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	rec2, err := a.Record("槽一", info2.ID, []string{"规则一😀/v�"})
	if err != nil {
		t.Fatalf("覆盖后记录应可读: %v", err)
	}
	want := original
	want.Time = 5
	want.Characters[0].Location = "院子"
	if !reflect.DeepEqual(rec2.State, want) {
		t.Fatalf("覆盖读回状态与预期不一致:\n got %+v\nwant %+v", rec2.State, want)
	}
}

// TestUpgradeInvalidUTF8TargetRejected 升级检查与正式提交遇到含非法文本的
// 目标规则都返回 *RuleError；槽当前记录与历史保持原样，调用方传入的目标
// 规则也不被修改。
func TestUpgradeInvalidUTF8TargetRejected(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	before, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}

	target := v2Rules()
	target.Version = invalidA

	t.Run("检查拒绝", func(t *testing.T) {
		check, err := a.CheckUpgrade("s", []string{"v1"}, target)
		if check.Blockers != nil || check.RecordID != "" {
			t.Fatalf("非法目标规则不应返回检查结果: %+v", check)
		}
		assertInvalidRuleError(t, err, "规则版本")
	})
	t.Run("正式提交拒绝", func(t *testing.T) {
		res, err := a.Upgrade("s", []string{"v1"}, target, info.ID)
		if res.Record.ID != "" {
			t.Fatalf("非法目标规则不应产生新记录: %+v", res)
		}
		assertInvalidRuleError(t, err, "规则版本")
	})

	// 目标规则各类字段都要在接纳前检查（这里测一个无人使用的地点）。
	target2 := v2Rules()
	target2.Locations = append(target2.Locations, invalidB)
	if _, err := a.CheckUpgrade("s", []string{"v1"}, target2); err == nil {
		t.Fatal("含非法地点的目标规则应被拒绝")
	} else {
		assertInvalidRuleError(t, err, "地点")
	}
	if _, err := a.Upgrade("s", []string{"v1"}, target2, info.ID); err == nil {
		t.Fatal("含非法地点的目标规则升级应被拒绝")
	} else {
		assertInvalidRuleError(t, err, "地点")
	}

	// 槽当前记录与历史保持原样。
	after, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("拒绝后槽当前记录应仍可读: %v", err)
	}
	if after.ID != info.ID || !reflect.DeepEqual(after.State, before.State) {
		t.Fatalf("拒绝操作改动了槽当前记录:\n got %+v\nwant %+v", after, before)
	}
	hist, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].ID != info.ID {
		t.Fatalf("拒绝操作改动了历史: %+v", hist)
	}
	// 调用方传入的规则没有被改写。
	if target.Version != invalidA {
		t.Fatalf("目标版本入参被改写: %q", target.Version)
	}
	for _, l := range target2.Locations {
		if l == invalidB {
			return
		}
	}
	t.Fatal("目标地点入参被改写")
}

// TestMigrationInvalidUTF8TargetRejected 显式迁移的预览与正式提交沿用
// 相同的规则合法性条件：目标规则含非法文本时两者都返回 *RuleError，槽
// 保持原样；转换对应关系中的非法名称同样被拒绝。
func TestMigrationInvalidUTF8TargetRejected(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	before, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}

	target := v2Rules()
	target.Version = invalidA

	t.Run("预览拒绝", func(t *testing.T) {
		preview, err := a.PreviewMigration("s", []string{"v1"}, target, nil, nil)
		if preview.RecordID != "" {
			t.Fatalf("非法目标规则不应返回预览: %+v", preview)
		}
		assertInvalidRuleError(t, err, "规则版本")
	})
	t.Run("正式提交拒绝", func(t *testing.T) {
		res, err := a.Migrate("s", []string{"v1"}, target, nil, nil, info.ID)
		if res.Record.ID != "" {
			t.Fatalf("非法目标规则不应产生新记录: %+v", res)
		}
		assertInvalidRuleError(t, err, "规则版本")
	})

	// 目标规则合法，但转换对应关系含非法 UTF-8 名称：预览与提交都拒绝。
	goodTarget := v2Rules()
	badMappings := []NameMapping{{From: "hall", To: invalidA}}
	if _, err := a.PreviewMigration("s", []string{"v1"}, goodTarget, badMappings, nil); err == nil {
		t.Fatal("含非法目标名称的转换关系应被拒绝")
	} else {
		assertInvalidRuleError(t, err, "转换关系")
	}
	if _, err := a.Migrate("s", []string{"v1"}, goodTarget, badMappings, nil, info.ID); err == nil {
		t.Fatal("含非法目标名称的迁移提交应被拒绝")
	} else {
		assertInvalidRuleError(t, err, "转换关系")
	}

	// 槽当前记录与历史保持原样，入参未被改写。
	after, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("拒绝后槽当前记录应仍可读: %v", err)
	}
	if after.ID != info.ID || !reflect.DeepEqual(after.State, before.State) {
		t.Fatalf("拒绝操作改动了槽当前记录:\n got %+v\nwant %+v", after, before)
	}
	if hist, err := a.History("s"); err != nil || len(hist) != 1 || hist[0].ID != info.ID {
		t.Fatalf("拒绝操作改动了历史: %v %+v", err, hist)
	}
	if target.Version != invalidA || badMappings[0].To != invalidA {
		t.Fatal("调用方入参被改写")
	}
}
