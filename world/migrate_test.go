package world

import (
	"errors"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
)

// renamedV2Rules 是把 hall 改名为 plaza、gold 改名为 coin 的 v2 规则。
func renamedV2Rules() Rules {
	return Rules{
		Version:   "v2",
		Locations: []string{"plaza", "yard", "cave", "tower"},
		Edges: []Edge{
			{From: "plaza", To: "yard"},
			{From: "yard", To: "cave"},
			{From: "plaza", To: "tower"},
		},
		ItemKinds:   []string{"coin", "key", "gem"},
		CarryLimits: map[string]int{"hero": 10, "mage": 5},
	}
}

func renameMapping() NameMapping {
	return NameMapping{
		Locations: []NameRename{{From: "hall", To: "plaza"}},
		Items:     []NameRename{{From: "gold", To: "coin"}},
	}
}

func TestPreviewMigrationSuccess(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")

	pv, err := a.PreviewMigration("s", []string{"v1"}, renamedV2Rules(), renameMapping())
	if err != nil {
		t.Fatalf("PreviewMigration: %v", err)
	}
	if pv.RecordID != info.ID {
		t.Fatalf("来源标识应为 %s，得到 %s", info.ID, pv.RecordID)
	}
	if pv.OldVersion != "v1" || pv.TargetVersion != "v2" {
		t.Fatalf("版本信息错误: %+v", pv)
	}
	if !pv.Committable {
		t.Fatalf("改名后应可提交，得到阻碍: %+v", pv.Blockers)
	}
	hero := pv.State.Characters[0]
	if hero.Location != "plaza" {
		t.Fatalf("hero 应改到 plaza，得到 %q", hero.Location)
	}
	if hero.Items[0].Item != "coin" || hero.Items[0].Count != 1 {
		t.Fatalf("gold 应改名为 coin 且数量不变，得到 %+v", hero.Items)
	}
	// 未指定的名称保持原样。
	mage := pv.State.Characters[1]
	if mage.Location != "yard" {
		t.Fatalf("mage 地点应保持 yard，得到 %q", mage.Location)
	}
	// 种子、时间片、角色标识与排列保持不变。
	if pv.State.Seed != 42 || pv.State.Time != 0 {
		t.Fatalf("种子/时间片应不变: seed=%d time=%d", pv.State.Seed, pv.State.Time)
	}
	if pv.State.Rules.Version != "v2" {
		t.Fatalf("转换后状态应带目标规则，得到 %q", pv.State.Rules.Version)
	}
	if ids := []string{pv.State.Characters[0].ID, pv.State.Characters[1].ID}; ids[0] != "hero" || ids[1] != "mage" {
		t.Fatalf("角色标识/排列被改变: %v", ids)
	}
}

func TestPreviewMigrationDoesNotMutate(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	ptrBefore, err := a.readLatestLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	histBefore, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	pv, err := a.PreviewMigration("s", []string{"v1"}, renamedV2Rules(), renameMapping())
	if err != nil {
		t.Fatal(err)
	}
	// 篡改返回状态，不应影响存档或之后的正式迁移。
	pv.State.Characters[0].Location = "HACKED"
	pv.State.Rules.Version = "HACKED"
	pv.Committable = false

	ptrAfter, err := a.readLatestLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	if ptrAfter != ptrBefore {
		t.Fatal("预览改变了槽指针")
	}
	histAfter, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(histAfter) != len(histBefore) {
		t.Fatal("预览改变了历史")
	}
	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != info.ID || rec.State.Rules.Version != "v1" {
		t.Fatal("预览影响了来源记录")
	}
}

func TestMigrateSuccess(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	// 推进时间，验证时间片保持。
	if _, err := w.Apply(Commit{Time: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Replace("s", w, info.ID); err != nil {
		t.Fatal(err)
	}
	latest, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}

	target := renamedV2Rules()
	mapping := renameMapping()
	pv, err := a.PreviewMigration("s", []string{"v1"}, target, mapping)
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Migrate("s", []string{"v1"}, target, mapping, pv.RecordID)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !res.Preview.Committable {
		t.Fatalf("可提交迁移不应有阻碍: %+v", res.Preview.Blockers)
	}
	if res.Record.ID == "" || res.Record.ID == latest.ID {
		t.Fatal("迁移应产生新记录标识")
	}
	if res.Record.Parent != latest.ID {
		t.Fatalf("新记录父应为 %s，得到 %s", latest.ID, res.Record.Parent)
	}
	if res.Record.Version != "v2" {
		t.Fatalf("新记录版本应为 v2，得到 %s", res.Record.Version)
	}

	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v1", "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != res.Record.ID {
		t.Fatal("槽指针应指向迁移后的记录")
	}
	if rec.State.Rules.Version != "v2" || len(rec.State.Rules.Locations) != 4 {
		t.Fatalf("新记录应保存完整目标规则: %+v", rec.State.Rules)
	}
	if rec.State.Seed != 42 || rec.State.Time != 3 {
		t.Fatalf("种子/时间片应保持: seed=%d time=%d", rec.State.Seed, rec.State.Time)
	}
	hero := rec.State.Characters[0]
	if hero.Location != "plaza" || hero.Items[0].Item != "coin" || hero.Items[0].Count != 1 {
		t.Fatalf("角色转换结果错误: %+v", hero)
	}
	// 旧记录保留原状态与旧规则。
	old, err := a2.Record("s", latest.ID, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if old.State.Rules.Version != "v1" || old.State.Characters[0].Location != "hall" {
		t.Fatalf("旧记录应保留原状态: %+v", old.State)
	}
	// 新记录加入本槽保存次序最前。
	hist, err := a2.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 || hist[0].ID != res.Record.ID {
		t.Fatalf("历史次序错误: %+v", hist)
	}
}

func TestMigrateRecomputesFromRecord(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	pv, err := a.PreviewMigration("s", []string{"v1"}, renamedV2Rules(), renameMapping())
	if err != nil {
		t.Fatal(err)
	}
	// 调用方篡改预览状态后再正式迁移；保存内容必须来自重新读取与计算。
	pv.State.Characters[0].Location = "HACKED"
	pv.State.Characters[0].Items[0].Count = 999
	res, err := a.Migrate("s", []string{"v1"}, renamedV2Rules(), renameMapping(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a.Latest("s", []string{"v2"})
	if err != nil {
		t.Fatal(err)
	}
	_ = res
	if rec.State.Characters[0].Location != "plaza" || rec.State.Characters[0].Items[0].Count != 1 {
		t.Fatalf("正式迁移采信了被篡改的预览状态: %+v", rec.State.Characters[0])
	}
}

func TestMigrateMergeItemKinds(t *testing.T) {
	a, _ := newTestArchive(t)
	rules := Rules{
		Version:     "v1",
		Locations:   []string{"hall"},
		ItemKinds:   []string{"gold", "key", "gem"},
		CarryLimits: map[string]int{"hero": 10},
	}
	w, err := NewWorld(InitialData{
		Seed: 1, Rules: rules,
		Characters: []Character{{
			ID:       "hero",
			Location: "hall",
			// gold 与 key 都转换成 gem，数量合并，位置取首次出现（0）。
			Items: []CharacterItem{{Item: "gold", Count: 2}, {Item: "key", Count: 3}, {Item: "gem", Count: 4}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	target := Rules{
		Version:     "v2",
		Locations:   []string{"hall"},
		ItemKinds:   []string{"gem"},
		CarryLimits: map[string]int{"hero": 10},
	}
	mapping := NameMapping{Items: []NameRename{
		{From: "gold", To: "gem"},
		{From: "key", To: "gem"},
	}}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if !pv.Committable {
		t.Fatalf("合并后总量 9 未超上限，阻碍: %+v", pv.Blockers)
	}
	items := pv.State.Characters[0].Items
	if len(items) != 1 || items[0].Item != "gem" || items[0].Count != 9 {
		t.Fatalf("gold/key/gem 应合并成一个 gem=9 条目，得到 %+v", items)
	}
	if _, err := a.Migrate("s", []string{"v1"}, target, mapping, info.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateMergeWithUnmappedKindAndZero(t *testing.T) {
	a, _ := newTestArchive(t)
	rules := Rules{
		Version:     "v1",
		Locations:   []string{"hall"},
		ItemKinds:   []string{"gold", "gem"},
		CarryLimits: map[string]int{"hero": 10},
	}
	w, err := NewWorld(InitialData{
		Seed: 1, Rules: rules,
		Characters: []Character{{
			ID:       "hero",
			Location: "hall",
			// gold 改名成已有的 gem；gem 数量为零仍保留并参与合并与种类检查。
			Items: []CharacterItem{{Item: "gold", Count: 2}, {Item: "gem", Count: 0}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	target := Rules{
		Version:     "v2",
		Locations:   []string{"hall"},
		ItemKinds:   []string{"gem"},
		CarryLimits: map[string]int{"hero": 10},
	}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target,
		NameMapping{Items: []NameRename{{From: "gold", To: "gem"}}})
	if err != nil {
		t.Fatal(err)
	}
	items := pv.State.Characters[0].Items
	if len(items) != 1 || items[0].Item != "gem" || items[0].Count != 2 {
		t.Fatalf("零数量条目应仍保留并合并，得到 %+v", items)
	}
}

func TestMigrateZeroCountStillChecked(t *testing.T) {
	a, _ := newTestArchive(t)
	rules := Rules{
		Version:     "v1",
		Locations:   []string{"hall"},
		ItemKinds:   []string{"gold"},
		CarryLimits: map[string]int{"hero": 5},
	}
	w, err := NewWorld(InitialData{
		Seed: 1, Rules: rules,
		Characters: []Character{{
			ID: "hero", Location: "hall",
			Items: []CharacterItem{{Item: "gold", Count: 0}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	// 目标移除 gold 且不给对应关系：零数量仍构成物品阻碍。
	target := Rules{
		Version:     "v2",
		Locations:   []string{"hall"},
		ItemKinds:   nil,
		CarryLimits: map[string]int{"hero": 5},
	}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target, NameMapping{})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Committable {
		t.Fatal("零数量物品种类被移除仍应阻碍")
	}
	if len(pv.Blockers) != 1 || pv.Blockers[0].Kind != BlockerItem || pv.Blockers[0].Item != "gold" {
		t.Fatalf("应得到 hero/gold 物品阻碍，得到 %+v", pv.Blockers)
	}
}

func TestMigrateSwapNames(t *testing.T) {
	a, _ := newTestArchive(t)
	rules := Rules{
		Version:     "v1",
		Locations:   []string{"hall"},
		ItemKinds:   []string{"gold", "key"},
		CarryLimits: map[string]int{"hero": 10},
	}
	w, err := NewWorld(InitialData{
		Seed: 1, Rules: rules,
		Characters: []Character{{
			ID:       "hero",
			Location: "hall",
			Items:    []CharacterItem{{Item: "gold", Count: 2}, {Item: "key", Count: 3}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	target := Rules{
		Version:     "v2",
		Locations:   []string{"hall"},
		ItemKinds:   []string{"gold", "key"},
		CarryLimits: map[string]int{"hero": 10},
	}
	// 互换：gold->key、key->gold，只作用于原始名称一次。
	mapping := NameMapping{Items: []NameRename{{From: "gold", To: "key"}, {From: "key", To: "gold"}}}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target, mapping)
	if err != nil {
		t.Fatal(err)
	}
	items := pv.State.Characters[0].Items
	if len(items) != 2 ||
		items[0].Item != "key" || items[0].Count != 2 ||
		items[1].Item != "gold" || items[1].Count != 3 {
		t.Fatalf("互换名称应一次性替换且保持位置/数量，得到 %+v", items)
	}
}

func TestMigrateRenameNotTransitive(t *testing.T) {
	a, _ := newTestArchive(t)
	w, err := NewWorld(InitialData{
		Seed: 42,
		Rules: Rules{
			Version:   "v1",
			Locations: []string{"a", "b", "c"},
		},
		Characters: []Character{{ID: "hero", Location: "a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	target := Rules{Version: "v2", Locations: []string{"a", "b", "c"}}
	// a->b、b->c：持有 a 的角色应只变成 b，不连续替换到 c。
	mapping := NameMapping{Locations: []NameRename{{From: "a", To: "b"}, {From: "b", To: "c"}}}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if pv.State.Characters[0].Location != "b" {
		t.Fatalf("转换应只作用一次，期望 b，得到 %q", pv.State.Characters[0].Location)
	}
}

func TestMigrateLocationIgnoresOldEdges(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	// hall 直接改成 tower：v1 中没有 hall->tower 道路，地点转换也无需符合旧道路。
	target := renamedV2Rules()
	mapping := renameMapping() // hall -> plaza
	mapping.Locations = []NameRename{{From: "hall", To: "tower"}}
	if _, err := a.PreviewMigration("s", []string{"v1"}, target, mapping); err != nil {
		t.Fatalf("地点改名不应检查旧道路: %v", err)
	}
	if _, err := a.Migrate("s", []string{"v1"}, target, mapping, info.ID); err != nil {
		t.Fatalf("地点改名不应检查旧道路: %v", err)
	}
	rec, err := a.Latest("s", []string{"v2"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.State.Characters[0].Location != "tower" {
		t.Fatalf("hero 应在 tower，得到 %q", rec.State.Characters[0].Location)
	}
}

func TestPreviewMigrationBlockersAllAtOnce(t *testing.T) {
	a, _ := newTestArchive(t)
	rules := Rules{
		Version: "v1",
		// cave 无人使用；角色 hero 在 hall，rogue 在 cave。
		Locations: []string{"hall", "yard", "cave"},
		Edges:     []Edge{{From: "hall", To: "yard"}},
		ItemKinds: []string{"gold", "key"},
		// 不给角色设上限，便于构造数据。
	}
	w, err := NewWorld(InitialData{
		Seed: 1, Rules: rules,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: 3}, {Item: "key", Count: 2},
			}},
			{ID: "rogue", Location: "cave", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	// gold、key 都改成 coin（合并 5）；cave 不给对应且目标中不存在；
	// hero 上限降到 4，合并后 5 超限。rogue 的 gold->coin 合法。
	target := Rules{
		Version:     "v2",
		Locations:   []string{"plaza", "yard"},
		ItemKinds:   []string{"coin"},
		CarryLimits: map[string]int{"hero": 4},
	}
	mapping := NameMapping{
		Locations: []NameRename{{From: "hall", To: "plaza"}},
		Items:     []NameRename{{From: "gold", To: "coin"}, {From: "key", To: "coin"}},
	}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Committable {
		t.Fatal("应不可提交")
	}
	want := []Blocker{
		{Character: "hero", Kind: BlockerLimit, Total: 5, Limit: 4},
		{Character: "rogue", Kind: BlockerLocation, Location: "cave"},
	}
	if len(pv.Blockers) != len(want) {
		t.Fatalf("期望 %d 条阻碍，得到 %d: %+v", len(want), len(pv.Blockers), pv.Blockers)
	}
	for i := range want {
		if pv.Blockers[i] != want[i] {
			t.Fatalf("第 %d 条阻碍应为 %+v，得到 %+v", i, want[i], pv.Blockers[i])
		}
	}
	// 即使有阻碍，转换后的状态仍返回（hero 已在 plaza 且物品合并）。
	if pv.State.Characters[0].Location != "plaza" {
		t.Fatalf("阻碍预览也应给出转换后状态，得到 %+v", pv.State.Characters[0])
	}
	// 重复预览顺序一致。
	pv2, err := a.PreviewMigration("s", []string{"v1"}, target, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv2.Blockers) != len(pv.Blockers) {
		t.Fatal("重复预览阻碍数量不一致")
	}
	for i := range pv.Blockers {
		if pv.Blockers[i] != pv2.Blockers[i] {
			t.Fatalf("重复预览顺序不一致: %+v vs %+v", pv.Blockers, pv2.Blockers)
		}
	}
}

func TestMigrateBlockedNoWrite(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	// 目标删掉 yard（mage 所在），且不给对应关系。
	target := Rules{
		Version:     "v2",
		Locations:   []string{"plaza"},
		ItemKinds:   []string{"coin"},
		CarryLimits: map[string]int{"hero": 10, "mage": 5},
	}
	mapping := NameMapping{
		Locations: []NameRename{{From: "hall", To: "plaza"}},
		Items:     []NameRename{{From: "gold", To: "coin"}},
	}
	res, err := a.Migrate("s", []string{"v1"}, target, mapping, info.ID)
	if err == nil {
		t.Fatal("有阻碍时迁移应被拒绝")
	}
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("阻碍应返回 *RuleError，得到 %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "阻碍") {
		t.Fatalf("错误信息应提及阻碍: %v", err)
	}
	if res.Preview.Committable || len(res.Preview.Blockers) == 0 {
		t.Fatal("被拒绝的结果应带阻碍")
	}
	if res.Record.ID != "" {
		t.Fatal("被拒绝时不应返回新记录")
	}
	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != info.ID {
		t.Fatal("被拒绝的迁移改变了槽指针")
	}
	hist, err := a2.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 {
		t.Fatalf("被拒绝的迁移增加了历史: %+v", hist)
	}
}

func TestMigrateInvalidMapping(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	target := renamedV2Rules()

	cases := []struct {
		name    string
		mapping NameMapping
	}{
		{"地点来源不在旧规则", NameMapping{Locations: []NameRename{{From: "void", To: "plaza"}}}},
		{"地点目标不在新规则", NameMapping{Locations: []NameRename{{From: "hall", To: "void"}}}},
		{"地点目标为空", NameMapping{Locations: []NameRename{{From: "hall", To: ""}}}},
		{"物品来源不在旧规则", NameMapping{Items: []NameRename{{From: "rock", To: "coin"}}}},
		{"物品目标不在新规则", NameMapping{Items: []NameRename{{From: "gold", To: "rock"}}}},
		{"物品目标为空", NameMapping{Items: []NameRename{{From: "gold", To: ""}}}},
		{"地点来源重复", NameMapping{Locations: []NameRename{{From: "hall", To: "plaza"}, {From: "hall", To: "yard"}}}},
		{"物品来源重复", NameMapping{Items: []NameRename{{From: "gold", To: "coin"}, {From: "gold", To: "gem"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.PreviewMigration("s", []string{"v1"}, target, tc.mapping)
			var re *RuleError
			if !errors.As(err, &re) {
				t.Fatalf("应返回 *RuleError，得到 %T: %v", err, err)
			}
		})
	}
}

func TestMigrateMappingValidatedEvenWhenUnused(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	target := renamedV2Rules()
	// cave 在旧规则中存在但无人使用：作为来源仍合法。
	ok := NameMapping{Locations: []NameRename{{From: "cave", To: "tower"}}}
	if _, err := a.PreviewMigration("s", []string{"v1"}, target, ok); err != nil {
		t.Fatalf("无人使用的旧名称也应可作为来源: %v", err)
	}
	// tower 在新规则中无人使用：作为目标也合法；gold 类似。
	ok2 := NameMapping{
		Locations: []NameRename{{From: "yard", To: "tower"}},
		Items:     []NameRename{{From: "key", To: "gem"}},
	}
	if _, err := a.PreviewMigration("s", []string{"v1"}, target, ok2); err != nil {
		t.Fatalf("无人使用的新名称也应可作为目标: %v", err)
	}
}

func TestMigrateMergeOverflow(t *testing.T) {
	a, _ := newTestArchive(t)
	rules := Rules{
		Version:   "v1",
		Locations: []string{"hall"},
		ItemKinds: []string{"gold", "key"},
	}
	w, err := NewWorld(InitialData{
		Seed: 1, Rules: rules,
		Characters: []Character{{
			ID: "hero", Location: "hall",
			Items: []CharacterItem{{Item: "gold", Count: math.MaxInt}, {Item: "key", Count: 1}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	target := Rules{Version: "v2", Locations: []string{"hall"}, ItemKinds: []string{"gem"}}
	mapping := NameMapping{Items: []NameRename{{From: "gold", To: "gem"}, {From: "key", To: "gem"}}}
	if _, err := a.PreviewMigration("s", []string{"v1"}, target, mapping); err == nil {
		t.Fatal("合并数量超出整数范围应返回错误")
	} else if _, ok := err.(*RuleError); !ok {
		t.Fatalf("应返回 *RuleError，得到 %T: %v", err, err)
	}
}

func TestMigrateCarryOverflow(t *testing.T) {
	a, _ := newTestArchive(t)
	rules := Rules{
		Version:   "v1",
		Locations: []string{"hall"},
		ItemKinds: []string{"gold", "key"},
	}
	w, err := NewWorld(InitialData{
		Seed: 1, Rules: rules,
		Characters: []Character{{
			ID: "hero", Location: "hall",
			// 不改名，仅携带总量本身就超出整数范围（建立时无上限故允许）。
			Items: []CharacterItem{{Item: "gold", Count: math.MaxInt}, {Item: "key", Count: math.MaxInt}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	target := Rules{Version: "v2", Locations: []string{"hall"}, ItemKinds: []string{"gold", "key"}}
	if _, err := a.PreviewMigration("s", []string{"v1"}, target, NameMapping{}); err == nil {
		t.Fatal("携带总量超出整数范围应返回错误")
	} else if _, ok := err.(*RuleError); !ok {
		t.Fatalf("应返回 *RuleError，得到 %T: %v", err, err)
	}
}

func TestMigrateIllegalTargetAndSameVersion(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	bad := renamedV2Rules()
	bad.Version = ""
	if _, err := a.PreviewMigration("s", []string{"v1"}, bad, renameMapping()); err == nil {
		t.Fatal("非法目标规则应返回错误")
	} else if _, ok := err.(*RuleError); !ok {
		t.Fatalf("应返回 *RuleError，得到 %T", err)
	}
	same := renamedV2Rules()
	same.Version = "v1"
	if _, err := a.PreviewMigration("s", []string{"v1"}, same, renameMapping()); err == nil {
		t.Fatal("版本相同应返回错误")
	} else if _, ok := err.(*RuleError); !ok {
		t.Fatalf("应返回 *RuleError，得到 %T", err)
	}
}

func TestMigrateSourceErrors(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")

	// 槽不存在。
	if _, err := a.PreviewMigration("nope", []string{"v1"}, renamedV2Rules(), renameMapping()); err == nil {
		t.Fatal("槽不存在应返回错误")
	} else if _, ok := err.(*NotFoundError); !ok {
		t.Fatalf("应返回 *NotFoundError，得到 %T", err)
	}
	// 版本不被接受，不自动挑选历史记录。
	if _, err := a.PreviewMigration("s", []string{"v9"}, renamedV2Rules(), renameMapping()); err == nil {
		t.Fatal("版本不被接受应返回错误")
	} else if _, ok := err.(*VersionRejectedError); !ok {
		t.Fatalf("应返回 *VersionRejectedError，得到 %T", err)
	}
	// 来源损坏。
	path := recordPath(dir, info.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	i := strings.Index(s, `"checksum": "`)
	start := i + len(`"checksum": "`)
	s = s[:start] + strings.Repeat("f", 64) + s[start+64:]
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PreviewMigration("s", []string{"v1"}, renamedV2Rules(), renameMapping()); err == nil {
		t.Fatal("来源损坏应返回错误")
	} else if _, ok := err.(*CorruptError); !ok {
		t.Fatalf("应返回 *CorruptError，得到 %T", err)
	}
}

func TestMigrateEmptyAndStaleExpected(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	if _, err := a.Migrate("s", []string{"v1"}, renamedV2Rules(), renameMapping(), ""); err == nil {
		t.Fatal("空标识应冲突")
	} else if !isConflict(err) {
		t.Fatalf("应返回 ConflictError，得到 %T", err)
	}
	// 先用覆盖推进槽。
	latest, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	w := baseWorld(t)
	if _, err := w.Apply(Commit{Time: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Replace("s", w, latest.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Migrate("s", []string{"v1"}, renamedV2Rules(), renameMapping(), info.ID); err == nil {
		t.Fatal("过期标识应冲突")
	} else if !isConflict(err) {
		t.Fatalf("应返回 ConflictError，得到 %T", err)
	}
}

func TestMigrateConcurrentSameSource(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, conflicts := 0, 0
	errs := make([]error, 0)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ga, err := Open(dir)
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			_, merr := ga.Migrate("s", []string{"v1"}, renamedV2Rules(), renameMapping(), info.ID)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case merr == nil:
				wins++
			case isConflict(merr):
				conflicts++
			default:
				errs = append(errs, merr)
			}
		}()
	}
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("并发迁移出现非冲突错误: %v", errs)
	}
	if wins != 1 || conflicts != n-1 {
		t.Fatalf("期望恰好 1 成功 %d 冲突，得到 %d 成功 %d 冲突", n-1, wins, conflicts)
	}
	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v1", "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.State.Rules.Version != "v2" || rec.State.Characters[0].Location != "plaza" {
		t.Fatalf("胜出记录应完成转换: %+v", rec.State.Characters[0])
	}
}

func TestMigrateVersusOtherWritesConcurrent(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, conflicts := 0, 0
	errs := make([]error, 0)
	note := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			wins++
		case isConflict(err):
			conflicts++
		default:
			errs = append(errs, err)
		}
	}
	// 迁移、覆盖、升级、确认恢复各若干，全部基于同一来源。
	for i := 0; i < 4; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			ga, _ := Open(dir)
			_, err := ga.Migrate("s", []string{"v1"}, renamedV2Rules(), renameMapping(), info.ID)
			note(err)
		}()
		go func() {
			defer wg.Done()
			ga, _ := Open(dir)
			_, err := ga.Replace("s", baseWorld(t), info.ID)
			note(err)
		}()
		go func() {
			defer wg.Done()
			ga, _ := Open(dir)
			_, err := ga.Upgrade("s", []string{"v1"}, v2Rules(), info.ID)
			note(err)
		}()
		go func() {
			defer wg.Done()
			ga, _ := Open(dir)
			_, err := ga.ConfirmRecovery("s", info.ID, info.ID, []string{"v1"})
			note(err)
		}()
	}
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("竞争出现非冲突错误: %v", errs)
	}
	if wins != 1 || conflicts != 15 {
		t.Fatalf("期望恰好 1 成功 15 冲突，得到 %d 成功 %d 冲突", wins, conflicts)
	}
}

func TestMigrateRecoverAndBranchUnaffected(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	if _, err := a.Branch("s", info.ID, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Migrate("s", []string{"v1"}, renamedV2Rules(), renameMapping(), info.ID); err != nil {
		t.Fatal(err)
	}
	a2, _ := Open(dir)
	// 只接受旧版本的恢复读取仍能找到迁移前记录。
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("恢复读取应找到迁移前记录: %v", err)
	}
	if rec.ID != info.ID {
		t.Fatalf("应恢复到迁移前记录 %s，得到 %s", info.ID, rec.ID)
	}
	// 分支保留原状态。
	brec, err := a2.Latest("b", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if brec.State.Rules.Version != "v1" || brec.State.Characters[0].Location != "hall" {
		t.Fatalf("分支不应受迁移影响: %+v", brec.State)
	}
	bhist, err := a2.History("b")
	if err != nil {
		t.Fatal(err)
	}
	if len(bhist) != 1 {
		t.Fatalf("分支历史应仍只有一条，得到 %+v", bhist)
	}
}

func TestMigrateReopenContinueAndNoAutoConvert(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	if _, err := a.Migrate("s", []string{"v1"}, renamedV2Rules(), renameMapping(), info.ID); err != nil {
		t.Fatal(err)
	}
	a2, _ := Open(dir)
	// 普通读取不会自动转换：旧记录照旧按 v1 读。
	old, err := a2.Record("s", info.ID, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if old.State.Rules.Version != "v1" {
		t.Fatal("普通读取不应自动转换旧记录")
	}
	// 迁移后的记录可按新规则移动、改变物品并保存。
	rec, err := a2.Latest("s", []string{"v2"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatalf("从迁移后记录重建世界失败: %v", err)
	}
	// plaza -> tower 是 v2 新道路；coin 是 v2 物品名。
	if _, err := w.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "tower"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "coin", Delta: 1}},
		Time:        2,
	}); err != nil {
		t.Fatalf("迁移后应按新规则继续: %v", err)
	}
	saved, err := a2.Replace("s", w, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	a3, _ := Open(dir)
	latest, err := a3.Latest("s", []string{"v2"})
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != saved.ID || latest.State.Characters[0].Location != "tower" ||
		latest.State.Characters[0].Items[0].Count != 2 {
		t.Fatalf("迁移后保存结果错误: %+v", latest.State.Characters[0])
	}
}

func TestMigrateIdentityMapping(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	// 空对应关系 + 只改版本的规则：行为等同原样承接。
	target := v2Rules()
	res, err := a.Migrate("s", []string{"v1"}, target, NameMapping{}, info.ID)
	if err != nil {
		t.Fatalf("空对应关系的迁移应成功: %v", err)
	}
	rec, err := a.Latest("s", []string{"v2"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Record.Version != "v2" || rec.State.Characters[0].Location != "hall" {
		t.Fatalf("空对应关系应原样承接状态: %+v", rec.State.Characters[0])
	}
}
