package world

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// bad 是一段含无效 UTF-8 字节的文本标识。
const bad = "loc\xff\xfe"

// bad2 是与 bad 字节不同、但保存为 JSON 后同样被改写成替换字符的标识。
const bad2 = "loc\xff"

// badRulesCases 列出规则中各类文本标识含无效 UTF-8 字节的情形，
// 每个情形都说明有问题的字段类别。
func badRulesCases() []struct {
	name string
	mod  func(*Rules)
	want string
} {
	return []struct {
		name string
		mod  func(*Rules)
		want string
	}{
		{"规则版本", func(r *Rules) { r.Version = "v\xff1" }, "规则版本"},
		{"地点标识", func(r *Rules) { r.Locations[1] = bad }, "地点"},
		{"无人使用的地点", func(r *Rules) { r.Locations = append(r.Locations, bad) }, "地点"},
		{"连通关系起点", func(r *Rules) { r.Edges[0].From = bad }, "连通关系"},
		{"连通关系终点", func(r *Rules) { r.Edges[1].To = bad }, "连通关系"},
		{"物品种类", func(r *Rules) { r.ItemKinds[0] = bad }, "物品种类"},
		{"无人使用的物品种类", func(r *Rules) { r.ItemKinds = append(r.ItemKinds, bad) }, "物品种类"},
		{"携带上限角色键", func(r *Rules) { r.CarryLimits[bad] = 3 }, "携带上限"},
		{"无对应角色的上限键", func(r *Rules) {
			r.CarryLimits = map[string]int{bad: 3}
		}, "携带上限"},
	}
}

func badCharCases() []struct {
	name string
	mod  func(*Character)
	want string
} {
	return []struct {
		name string
		mod  func(*Character)
		want string
	}{
		{"角色标识", func(c *Character) { c.ID = bad }, "角色"},
		{"所在地点", func(c *Character) { c.Location = bad }, "地点"},
		{"物品名称", func(c *Character) { c.Items = []CharacterItem{{Item: bad, Count: 1}} }, "物品"},
	}
}

func TestNewWorldRejectsInvalidUTF8Rules(t *testing.T) {
	for _, tc := range badRulesCases() {
		t.Run(tc.name, func(t *testing.T) {
			data := InitialData{
				Seed:       1,
				Rules:      baseRules(),
				Characters: []Character{{ID: "hero", Location: "hall"}},
			}
			tc.mod(&data.Rules)
			w, err := NewWorld(data)
			if err == nil {
				t.Fatal("含无效 UTF-8 的规则应当被拒绝，但建立了世界")
			}
			re, ok := err.(*RuleError)
			if !ok {
				t.Fatalf("期望 *RuleError，得到 %T: %v", err, err)
			}
			if w != nil {
				t.Fatal("拒绝时不应返回可用世界")
			}
			if !strings.Contains(re.Reason, tc.want) {
				t.Fatalf("错误应说明字段类别 %q，得到: %s", tc.want, re.Reason)
			}
		})
	}
}

func TestNewWorldRejectsInvalidUTF8Characters(t *testing.T) {
	for _, tc := range badCharCases() {
		t.Run(tc.name, func(t *testing.T) {
			data := InitialData{
				Seed:       1,
				Rules:      baseRules(),
				Characters: []Character{{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}}},
			}
			tc.mod(&data.Characters[0])
			if _, err := NewWorld(data); err == nil {
				t.Fatal("含无效 UTF-8 的角色状态应当被拒绝，但建立了世界")
			} else if re, ok := err.(*RuleError); !ok {
				t.Fatalf("期望 *RuleError，得到 %T: %v", err, err)
			} else if !strings.Contains(re.Reason, tc.want) {
				t.Fatalf("错误应说明字段类别 %q，得到: %s", tc.want, re.Reason)
			}
		})
	}
}

// TestInvalidUTF8DistinctNamesRejected 两个原本不同的无效字节串在保存为
// JSON 后都会变成替换字符而重名；它们必须在接纳时就被拒绝。
func TestInvalidUTF8DistinctNamesRejected(t *testing.T) {
	if bad == bad2 {
		t.Fatal("测试前提：两个标识字节不同")
	}
	data := InitialData{
		Seed: 1,
		Rules: Rules{
			Version:   "v1",
			Locations: []string{bad, bad2},
		},
	}
	if _, err := NewWorld(data); err == nil {
		t.Fatal("两个保存后会重名的无效标识应当被拒绝")
	} else if _, ok := err.(*RuleError); !ok {
		t.Fatalf("期望 *RuleError，得到 %T: %v", err, err)
	}
}

func TestGenerateWorldRejectsInvalidUTF8(t *testing.T) {
	base := GenerateRequest{
		Seed:         7,
		Locations:    []string{"a", "b", "c"},
		RoadCount:    2,
		RulesVersion: "v1",
		ItemKinds:    []string{"gold"},
		Characters:   []Character{{ID: "hero", Location: "a"}},
	}
	cases := []struct {
		name string
		mod  func(*GenerateRequest)
	}{
		{"地点", func(r *GenerateRequest) { r.Locations[1] = bad }},
		{"规则版本", func(r *GenerateRequest) { r.RulesVersion = "v\xff" }},
		{"物品种类", func(r *GenerateRequest) { r.ItemKinds = []string{bad} }},
		{"携带上限角色键", func(r *GenerateRequest) { r.CarryLimits = map[string]int{bad: 1} }},
		{"角色标识", func(r *GenerateRequest) { r.Characters[0].ID = bad }},
		{"所在地点", func(r *GenerateRequest) { r.Characters[0].Location = bad }},
		{"物品名称", func(r *GenerateRequest) {
			r.Characters[0].Items = []CharacterItem{{Item: bad, Count: 1}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			req.Locations = append([]string(nil), base.Locations...)
			req.ItemKinds = append([]string(nil), base.ItemKinds...)
			req.Characters = cloneCharacters(base.Characters)
			tc.mod(&req)
			w, err := GenerateWorld(req)
			if err == nil {
				t.Fatal("含无效 UTF-8 的生成请求应当被拒绝，但建立了世界")
			}
			if _, ok := err.(*RuleError); !ok {
				t.Fatalf("期望 *RuleError，得到 %T: %v", err, err)
			}
			if w != nil {
				t.Fatal("拒绝时不应返回可用世界")
			}
		})
	}
}

func TestWorldFromStateRejectsInvalidUTF8(t *testing.T) {
	st := State{
		Seed:  1,
		Rules: baseRules(),
		Time:  3,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	}
	cases := []struct {
		name string
		mod  func(*State)
	}{
		{"规则版本", func(s *State) { s.Rules.Version = bad }},
		{"地点", func(s *State) { s.Rules.Locations[2] = bad }},
		{"携带上限角色键", func(s *State) { s.Rules.CarryLimits[bad] = 2 }},
		{"角色标识", func(s *State) { s.Characters[0].ID = bad }},
		{"所在地点", func(s *State) { s.Characters[0].Location = bad }},
		{"物品名称", func(s *State) { s.Characters[0].Items[0].Item = bad }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := cloneState(st)
			tc.mod(&s)
			if _, err := WorldFromState(s); err == nil {
				t.Fatal("含无效 UTF-8 的状态应当被拒绝，但重建了世界")
			} else if _, ok := err.(*RuleError); !ok {
				t.Fatalf("期望 *RuleError，得到 %T: %v", err, err)
			}
		})
	}
}

// TestRejectionDoesNotMutateInput 拒绝操作不能修改调用方传入的规则或状态。
func TestRejectionDoesNotMutateInput(t *testing.T) {
	data := InitialData{
		Seed: 1,
		Rules: Rules{
			Version:     "v1",
			Locations:   []string{"hall", bad},
			Edges:       []Edge{{From: "hall", To: "hall"}},
			ItemKinds:   []string{"gold"},
			CarryLimits: map[string]int{"hero": 5},
		},
		Characters: []Character{{ID: "hero", Location: "hall"}},
	}
	snapshot := InitialData{
		Seed:       data.Seed,
		Rules:      cloneRules(data.Rules),
		Characters: cloneCharacters(data.Characters),
	}
	if _, err := NewWorld(data); err == nil {
		t.Fatal("前提：该输入应被拒绝")
	}
	if !reflect.DeepEqual(data.Rules, snapshot.Rules) {
		t.Fatalf("拒绝后规则被修改: %+v", data.Rules)
	}
	if !reflect.DeepEqual(data.Characters, snapshot.Characters) {
		t.Fatalf("拒绝后角色状态被修改: %+v", data.Characters)
	}
}

// TestValidUTF8Accepted 合法 UTF-8 中真实存在的替换字符、中文与表情符号
// 都按普通标识接受，保存再读取后内容一致。
func TestValidUTF8Accepted(t *testing.T) {
	a, _ := newTestArchive(t)
	data := InitialData{
		Seed: 9,
		Rules: Rules{
			Version:   "版本�v1",
			Locations: []string{"大厅", "�", "🌋火山"},
			Edges: []Edge{
				{From: "大厅", To: "�"},
				{From: "�", To: "🌋火山"},
			},
			ItemKinds:   []string{"金币", "🗝"},
			CarryLimits: map[string]int{"勇者": 10},
		},
		Characters: []Character{
			{ID: "勇者", Location: "大厅", Items: []CharacterItem{
				{Item: "金币", Count: 2},
				{Item: "🗝", Count: 1},
			}},
			{ID: "mage�", Location: "🌋火山"},
		},
	}
	w, err := NewWorld(data)
	if err != nil {
		t.Fatalf("合法 UTF-8 标识应当被接受: %v", err)
	}
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	rec, err := a.Latest("s", []string{data.Rules.Version})
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rec.ID != info.ID {
		t.Fatalf("记录标识不一致: %s != %s", rec.ID, info.ID)
	}
	if !reflect.DeepEqual(rec.State.Rules, w.Snapshot().Rules) {
		t.Fatalf("保存再读取后规则不一致:\n保存前 %+v\n读取后 %+v", w.Snapshot().Rules, rec.State.Rules)
	}
	if !reflect.DeepEqual(rec.State.Characters, w.Snapshot().Characters) {
		t.Fatalf("保存再读取后角色不一致:\n保存前 %+v\n读取后 %+v", w.Snapshot().Characters, rec.State.Characters)
	}
}

// TestUpgradeRejectsInvalidUTF8Target 目标规则含无效文本时，升级的检查与
// 正式提交都返回 *RuleError，槽当前记录与历史保持原样。
func TestUpgradeRejectsInvalidUTF8Target(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")

	target := v2Rules()
	target.Locations = append(target.Locations, bad)

	if _, err := a.CheckUpgrade("s", []string{"v1"}, target); err == nil {
		t.Fatal("CheckUpgrade 应当拒绝含无效 UTF-8 的目标规则")
	} else if _, ok := err.(*RuleError); !ok {
		t.Fatalf("CheckUpgrade 期望 *RuleError，得到 %T: %v", err, err)
	}
	if _, err := a.Upgrade("s", []string{"v1"}, target, info.ID); err == nil {
		t.Fatal("Upgrade 应当拒绝含无效 UTF-8 的目标规则")
	} else if _, ok := err.(*RuleError); !ok {
		t.Fatalf("Upgrade 期望 *RuleError，得到 %T: %v", err, err)
	}

	// 槽当前记录与历史保持原样。
	latest, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.ID != info.ID {
		t.Fatalf("被拒绝的升级不应改变槽当前记录: %s != %s", latest.ID, info.ID)
	}
	history, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 || history[0].ID != info.ID {
		t.Fatalf("被拒绝的升级不应改变历史: %+v", history)
	}
}

// TestMigrateRejectsInvalidUTF8Target 目标规则含无效文本时，迁移的预览与
// 正式提交都返回 *RuleError，槽当前记录与历史保持原样。
func TestMigrateRejectsInvalidUTF8Target(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")

	target := migrateTarget()
	target.ItemKinds = append(target.ItemKinds, bad)

	if _, err := a.PreviewMigration("s", []string{"v1"}, target, locationMappings(), itemMappings()); err == nil {
		t.Fatal("PreviewMigration 应当拒绝含无效 UTF-8 的目标规则")
	} else if _, ok := err.(*RuleError); !ok {
		t.Fatalf("PreviewMigration 期望 *RuleError，得到 %T: %v", err, err)
	}
	if _, err := a.Migrate("s", []string{"v1"}, target, locationMappings(), itemMappings(), info.ID); err == nil {
		t.Fatal("Migrate 应当拒绝含无效 UTF-8 的目标规则")
	} else if _, ok := err.(*RuleError); !ok {
		t.Fatalf("Migrate 期望 *RuleError，得到 %T: %v", err, err)
	}

	latest, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.ID != info.ID {
		t.Fatalf("被拒绝的迁移不应改变槽当前记录: %s != %s", latest.ID, info.ID)
	}
	history, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 || history[0].ID != info.ID {
		t.Fatalf("被拒绝的迁移不应改变历史: %+v", history)
	}
}

// TestExistingArchiveStillReadable 已有可读存档继续按原有校验与版本接受
// 规则使用：合法世界保存再读取后文本内容、角色和物品排列与保存前一致。
func TestExistingArchiveStillReadable(t *testing.T) {
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	if _, err := a.Save("s", w); err != nil {
		t.Fatalf("Save: %v", err)
	}
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("RecoverLatest: %v", err)
	}
	if !reflect.DeepEqual(rec.State, w.Snapshot()) {
		t.Fatalf("恢复的状态与保存前不一致:\n保存前 %+v\n恢复后 %+v", w.Snapshot(), rec.State)
	}
	var re *RuleError
	if err := func() error {
		_, err := WorldFromState(rec.State)
		return err
	}(); errors.As(err, &re) {
		t.Fatalf("从存档重建世界不应报规则错误: %v", err)
	}
}
