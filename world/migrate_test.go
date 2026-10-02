package world

import (
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
)

// migrateTarget 构造一个把 v1 地点/物品改名的目标规则：
// hall->great_hall、gold->coin，其余旧内容保留并提高上限。
func migrateTarget() Rules {
	return Rules{
		Version:   "v2",
		Locations: []string{"great_hall", "yard", "cave", "tower"},
		Edges: []Edge{
			{From: "great_hall", To: "yard"},
			{From: "yard", To: "cave"},
			{From: "great_hall", To: "tower"},
		},
		ItemKinds:   []string{"coin", "key", "gem"},
		CarryLimits: map[string]int{"hero": 10, "mage": 5},
	}
}

func locationMappings() []NameMapping {
	return []NameMapping{{From: "hall", To: "great_hall"}}
}

func itemMappings() []NameMapping {
	return []NameMapping{{From: "gold", To: "coin"}}
}

func TestPreviewMigrationRenameAndKeep(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")

	pv, err := a.PreviewMigration("s", []string{"v1"}, migrateTarget(), locationMappings(), itemMappings())
	if err != nil {
		t.Fatalf("PreviewMigration: %v", err)
	}
	if !pv.Committable {
		t.Fatalf("改名后应当可提交，得到阻碍: %+v", pv.Blockers)
	}
	if pv.RecordID == "" || pv.OldVersion != "v1" || pv.TargetVersion != "v2" {
		t.Fatalf("版本信息错误: %+v", pv)
	}
	if len(pv.Blockers) != 0 {
		t.Fatalf("不应有阻碍，得到 %+v", pv.Blockers)
	}
	// 规则已替换为目标规则。
	if pv.State.Rules.Version != "v2" || len(pv.State.Rules.Locations) != 4 {
		t.Fatalf("预览状态应保存目标规则: %+v", pv.State.Rules)
	}
	// 种子、时间片保持原样。
	if pv.State.Seed != 42 || pv.State.Time != 0 {
		t.Fatalf("种子/时间片未保持原样: seed=%d time=%d", pv.State.Seed, pv.State.Time)
	}
	// hero 地点与物品按对应关系转换。
	hero := pv.State.Characters[0]
	if hero.ID != "hero" || hero.Location != "great_hall" {
		t.Fatalf("hero 地点转换错误: %+v", hero)
	}
	if len(hero.Items) != 1 || hero.Items[0] != (CharacterItem{Item: "coin", Count: 1}) {
		t.Fatalf("hero 物品转换错误: %+v", hero.Items)
	}
	// mage 未指定名称，保持原样。
	mage := pv.State.Characters[1]
	if mage.ID != "mage" || mage.Location != "yard" || len(mage.Items) != 0 {
		t.Fatalf("mage 未指定名称应保持原样: %+v", mage)
	}
}

func TestPreviewMigrationMergeItems(t *testing.T) {
	a, _ := newTestArchive(t)
	// hero 持有 gold(2) 与 key(3)，都转换成 coin：合并为 coin(5)，
	// 按原列表中首次出现的位置（gold 的位置）保留一个条目。
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: baseRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: 2},
				{Item: "key", Count: 3},
			}},
		},
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
		ItemKinds:   []string{"coin"},
		CarryLimits: map[string]int{"hero": 10},
	}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target, nil,
		[]NameMapping{{From: "gold", To: "coin"}, {From: "key", To: "coin"}})
	if err != nil {
		t.Fatalf("PreviewMigration: %v", err)
	}
	if !pv.Committable {
		t.Fatalf("合并后应当可提交，得到阻碍: %+v", pv.Blockers)
	}
	items := pv.State.Characters[0].Items
	if len(items) != 1 {
		t.Fatalf("合并后应只有一个条目，得到 %+v", items)
	}
	if items[0] != (CharacterItem{Item: "coin", Count: 5}) {
		t.Fatalf("合并数量应为 coin(5)，得到 %+v", items[0])
	}
}

func TestPreviewMigrationMergeKeepsFirstPosition(t *testing.T) {
	a, _ := newTestArchive(t)
	// key 在 gold 之前：合并条目应保留在 key 的位置（索引 0）。
	oldRules := Rules{
		Version:   "v1",
		Locations: []string{"hall"},
		ItemKinds: []string{"gold", "key", "gem"},
	}
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: oldRules,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "key", Count: 3},
				{Item: "gold", Count: 2},
				{Item: "gem", Count: 1},
			}},
		},
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
		ItemKinds:   []string{"coin", "gem"},
		CarryLimits: map[string]int{"hero": 10},
	}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target, nil,
		[]NameMapping{{From: "gold", To: "coin"}, {From: "key", To: "coin"}})
	if err != nil {
		t.Fatalf("PreviewMigration: %v", err)
	}
	items := pv.State.Characters[0].Items
	if len(items) != 2 {
		t.Fatalf("合并后应有 2 个条目，得到 %+v", items)
	}
	// 首个位置是原列表中首次出现的 key -> coin(5)。
	if items[0] != (CharacterItem{Item: "coin", Count: 5}) {
		t.Fatalf("合并条目应在首次出现位置，得到 %+v", items[0])
	}
	if items[1] != (CharacterItem{Item: "gem", Count: 1}) {
		t.Fatalf("未指定的 gem 应保持原位，得到 %+v", items[1])
	}
}

func TestPreviewMigrationMergeWithExistingTarget(t *testing.T) {
	a, _ := newTestArchive(t)
	// gold -> coin，而角色本来就持有 coin：合并为 coin(5)。
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: baseRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: 2},
				{Item: "key", Count: 3},
			}},
		},
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
		ItemKinds:   []string{"coin"},
		CarryLimits: map[string]int{"hero": 10},
	}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target, nil,
		[]NameMapping{{From: "gold", To: "coin"}, {From: "key", To: "coin"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := pv.State.Characters[0].Items; len(got) != 1 || got[0] != (CharacterItem{Item: "coin", Count: 5}) {
		t.Fatalf("应合并为 coin(5)，得到 %+v", got)
	}
}

func TestPreviewMigrationSwapDeterministic(t *testing.T) {
	a, _ := newTestArchive(t)
	// 地点互换 A<->B：角色在 A 应转到 B，在 B 应转到 A，不沿对应关系连续替换。
	oldRules := Rules{
		Version:   "v1",
		Locations: []string{"A", "B"},
		ItemKinds: []string{"gold"},
	}
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: oldRules,
		Characters: []Character{
			{ID: "a", Location: "A"},
			{ID: "b", Location: "B"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	target := Rules{
		Version:   "v2",
		Locations: []string{"A", "B"},
		ItemKinds: []string{"gold"},
	}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target,
		[]NameMapping{{From: "A", To: "B"}, {From: "B", To: "A"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	locs := map[string]string{}
	for _, ch := range pv.State.Characters {
		locs[ch.ID] = ch.Location
	}
	if locs["a"] != "B" || locs["b"] != "A" {
		t.Fatalf("互换应有确定结果，得到 %+v", locs)
	}
}

func TestPreviewMigrationNoChaining(t *testing.T) {
	a, _ := newTestArchive(t)
	// A->B、B->C：在 A 的角色只转到 B（不连续替换到 C），在 B 的角色转到 C。
	oldRules := Rules{
		Version:   "v1",
		Locations: []string{"A", "B"},
		ItemKinds: []string{"gold"},
	}
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: oldRules,
		Characters: []Character{
			{ID: "a", Location: "A"},
			{ID: "b", Location: "B"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	target := Rules{
		Version:   "v2",
		Locations: []string{"B", "C"},
		ItemKinds: []string{"gold"},
	}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target,
		[]NameMapping{{From: "A", To: "B"}, {From: "B", To: "C"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	locs := map[string]string{}
	for _, ch := range pv.State.Characters {
		locs[ch.ID] = ch.Location
	}
	if locs["a"] != "B" || locs["b"] != "C" {
		t.Fatalf("转换不应连续替换，得到 %+v", locs)
	}
}

func TestPreviewMigrationZeroCountKeptAndChecked(t *testing.T) {
	a, _ := newTestArchive(t)
	// gold 数量为 0 且未指定转换，新规则移除 gold 种类：仍应算阻碍。
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: baseRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 0}}},
		},
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
		ItemKinds:   []string{},
		CarryLimits: map[string]int{"hero": 5},
	}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Committable {
		t.Fatal("数量为零的已列物品被移除种类仍应算阻碍")
	}
	found := false
	for _, b := range pv.Blockers {
		if b.Character == "hero" && b.Kind == BlockerItem && b.Item == "gold" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应包含 hero/gold 物品种类阻碍，得到 %+v", pv.Blockers)
	}

	// 数量为零且转换成 coin：条目保留为 coin(0)，不被丢弃。
	target2 := Rules{
		Version:     "v2",
		Locations:   []string{"hall"},
		ItemKinds:   []string{"coin"},
		CarryLimits: map[string]int{"hero": 5},
	}
	pv2, err := a.PreviewMigration("s", []string{"v1"}, target2, nil,
		[]NameMapping{{From: "gold", To: "coin"}})
	if err != nil {
		t.Fatal(err)
	}
	if !pv2.Committable {
		t.Fatalf("零数量转换后应可提交，得到阻碍: %+v", pv2.Blockers)
	}
	items := pv2.State.Characters[0].Items
	if len(items) != 1 || items[0] != (CharacterItem{Item: "coin", Count: 0}) {
		t.Fatalf("零数量条目应保留为 coin(0)，得到 %+v", items)
	}
}

func TestPreviewMigrationMappingErrors(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	target := migrateTarget()

	cases := []struct {
		name      string
		locations []NameMapping
		items     []NameMapping
	}{
		{"地点来源为空", []NameMapping{{From: "", To: "great_hall"}}, nil},
		{"地点目标为空", []NameMapping{{From: "hall", To: ""}}, nil},
		{"物品来源为空", nil, []NameMapping{{From: "", To: "coin"}}},
		{"物品目标为空", nil, []NameMapping{{From: "gold", To: ""}}},
		{"地点来源不存在于旧规则", []NameMapping{{From: "void", To: "great_hall"}}, nil},
		{"地点目标不存在于新规则", []NameMapping{{From: "hall", To: "void"}}, nil},
		{"物品来源不存在于旧规则", nil, []NameMapping{{From: "rock", To: "coin"}}},
		{"物品目标不存在于新规则", nil, []NameMapping{{From: "gold", To: "void"}}},
		{"地点来源重复指定", []NameMapping{{From: "hall", To: "great_hall"}, {From: "hall", To: "yard"}}, nil},
		{"物品来源重复指定", nil, []NameMapping{{From: "gold", To: "coin"}, {From: "gold", To: "key"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.PreviewMigration("s", []string{"v1"}, target, tc.locations, tc.items)
			var re *RuleError
			if !errors.As(err, &re) {
				t.Fatalf("应返回 *RuleError，得到 %T: %v", err, err)
			}
		})
	}
}

func TestPreviewMigrationMappingCheckedEvenIfUnused(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	target := migrateTarget()
	// 旧规则有 cave 地点，但没有角色在 cave；对应关系仍要检查。
	_, err := a.PreviewMigration("s", []string{"v1"}, target,
		[]NameMapping{{From: "cave", To: "void"}}, nil)
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("即使没有角色使用，未知目标仍应返回规则错误，得到 %T: %v", err, err)
	}
}

func TestPreviewMigrationIllegalTarget(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	target := migrateTarget()
	target.Version = ""
	_, err := a.PreviewMigration("s", []string{"v1"}, target, nil, nil)
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("目标规则非法应返回 *RuleError，得到 %T: %v", err, err)
	}
}

func TestPreviewMigrationSameVersion(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	target := migrateTarget()
	target.Version = "v1"
	_, err := a.PreviewMigration("s", []string{"v1"}, target, nil, nil)
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("版本相同应返回 *RuleError，得到 %T: %v", err, err)
	}
}

func TestPreviewMigrationBlockersAllListedAndSorted(t *testing.T) {
	a, _ := newTestArchive(t)
	// 两个角色：zara 与 anna。转换后 zara 缺地点、缺物品、超上限；
	// anna 也缺地点。一次列出全部阻碍并按角色、类别排序。
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: baseRules(),
		Characters: []Character{
			{ID: "zara", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 3}, {Item: "key", Count: 3}}},
			{ID: "anna", Location: "yard", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	// 目标：只保留 cave 地点与 coin 种类，把两人上限都降到 0。
	// 不提供对应关系，因此 hall/yard、gold/key 都无法转换。
	target := Rules{
		Version:     "v2",
		Locations:   []string{"cave"},
		ItemKinds:   []string{"coin"},
		CarryLimits: map[string]int{"zara": 0, "anna": 0},
	}
	pv, err := a.PreviewMigration("s", []string{"v1"}, target, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Committable {
		t.Fatal("存在阻碍时不应可提交")
	}
	// anna: location(yard)、item(gold)、limit(1>0)
	// zara: location(hall)、item(gold)、item(key)、limit(6>0)
	want := []Blocker{
		{Character: "anna", Kind: BlockerLocation, Location: "yard"},
		{Character: "anna", Kind: BlockerItem, Item: "gold"},
		{Character: "anna", Kind: BlockerLimit, Total: 1, Limit: 0},
		{Character: "zara", Kind: BlockerLocation, Location: "hall"},
		{Character: "zara", Kind: BlockerItem, Item: "gold"},
		{Character: "zara", Kind: BlockerItem, Item: "key"},
		{Character: "zara", Kind: BlockerLimit, Total: 6, Limit: 0},
	}
	if len(pv.Blockers) != len(want) {
		t.Fatalf("期望 %d 条阻碍，得到 %d: %+v", len(want), len(pv.Blockers), pv.Blockers)
	}
	for i := range want {
		if pv.Blockers[i] != want[i] {
			t.Fatalf("第 %d 条阻碍应为 %+v，得到 %+v", i, want[i], pv.Blockers[i])
		}
	}
	// 重复预览顺序一致。
	pv2, err := a.PreviewMigration("s", []string{"v1"}, target, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range pv.Blockers {
		if pv.Blockers[i] != pv2.Blockers[i] {
			t.Fatalf("重复预览输出顺序不一致: %+v vs %+v", pv.Blockers, pv2.Blockers)
		}
	}
}

func TestPreviewMigrationOverflow(t *testing.T) {
	a, _ := newTestArchive(t)
	// gold(MaxInt) 与 key(1) 合并为 coin：数量相加溢出 int，返回规则错误。
	w, err := NewWorld(InitialData{
		Seed: 1,
		Rules: Rules{
			Version:   "v1",
			Locations: []string{"hall"},
			ItemKinds: []string{"gold", "key"},
		},
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: math.MaxInt},
				{Item: "key", Count: 1},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	target := Rules{
		Version:   "v2",
		Locations: []string{"hall"},
		ItemKinds: []string{"coin"},
	}
	_, err = a.PreviewMigration("s", []string{"v1"}, target, nil,
		[]NameMapping{{From: "gold", To: "coin"}, {From: "key", To: "coin"}})
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("合并数量溢出应返回 *RuleError，得到 %T: %v", err, err)
	}

	// 不合并，仅携带总量溢出同样返回规则错误。
	target2 := Rules{
		Version:   "v2",
		Locations: []string{"hall"},
		ItemKinds: []string{"gold", "key"},
	}
	_, err = a.PreviewMigration("s", []string{"v1"}, target2, nil, nil)
	if !errors.As(err, &re) {
		t.Fatalf("携带总量溢出应返回 *RuleError，得到 %T: %v", err, err)
	}
}

func TestPreviewMigrationSourceErrors(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	target := migrateTarget()

	// 来源损坏。
	corruptChecksum(t, dir, info.ID)
	_, err := a.PreviewMigration("s", []string{"v1"}, target, locationMappings(), itemMappings())
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("来源损坏应返回 *CorruptError，得到 %T: %v", err, err)
	}

	// 来源缺失。
	deleteRecord(t, dir, info.ID)
	_, err = a.PreviewMigration("s", []string{"v1"}, target, locationMappings(), itemMappings())
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("来源缺失应返回 *NotFoundError，得到 %T: %v", err, err)
	}

	// 版本不被接受。
	a2, _ := Open(dir)
	saveBase(t, a2, "s2")
	_, err = a2.PreviewMigration("s2", []string{"v9"}, target, locationMappings(), itemMappings())
	var vre *VersionRejectedError
	if !errors.As(err, &vre) {
		t.Fatalf("来源版本不被接受应返回 *VersionRejectedError，得到 %T: %v", err, err)
	}

	// 槽不存在。
	_, err = a2.PreviewMigration("nope", []string{"v1"}, target, nil, nil)
	if !errors.As(err, &nf) {
		t.Fatalf("槽不存在应返回 *NotFoundError，得到 %T: %v", err, err)
	}
}

func TestPreviewMigrationReadOnly(t *testing.T) {
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
	target := migrateTarget()
	pv, err := a.PreviewMigration("s", []string{"v1"}, target, locationMappings(), itemMappings())
	if err != nil {
		t.Fatal(err)
	}
	// 改预览返回的状态不影响存档，也不影响再次预览。
	pv.State.Seed = 999
	pv.State.Characters[0].Location = "cave"
	pv.State.Rules.Version = "MUTATED"
	pv2, err := a.PreviewMigration("s", []string{"v1"}, target, locationMappings(), itemMappings())
	if err != nil {
		t.Fatal(err)
	}
	if pv2.State.Seed != 42 || pv2.State.Characters[0].Location != "great_hall" {
		t.Fatal("预览状态被调用方修改影响了再次预览")
	}
	// 槽指针与历史不变。
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
		t.Fatal("预览改变了历史长度")
	}
	// 重新打开后仍是旧记录。
	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != info.ID || rec.State.Rules.Version != "v1" {
		t.Fatal("预览改变了来源记录")
	}
}

func TestMigrateSuccess(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	// 推进时间并移动角色，验证迁移后这些状态保持原样。
	if _, err := w.Apply(Commit{Moves: []Move{{Character: "hero", To: "yard"}}, Time: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Replace("s", w, info.ID); err != nil {
		t.Fatal(err)
	}
	latest, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}

	target := migrateTarget()
	res, err := a.Migrate("s", []string{"v1"}, target, locationMappings(), itemMappings(), latest.ID)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !res.Preview.Committable {
		t.Fatalf("可提交的迁移不应有阻碍: %+v", res.Preview.Blockers)
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

	// 重新打开存档读取新记录。
	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v1", "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != res.Record.ID {
		t.Fatal("槽指针应指向迁移后的记录")
	}
	// 新记录保存目标规则。
	if rec.State.Rules.Version != "v2" || len(rec.State.Rules.Locations) != 4 {
		t.Fatalf("新记录应保存目标规则: %+v", rec.State.Rules)
	}
	// 种子、时间片保持原样。
	if rec.State.Seed != 42 || rec.State.Time != 3 {
		t.Fatalf("种子/时间片未保持原样: seed=%d time=%d", rec.State.Seed, rec.State.Time)
	}
	// hero 从 yard 转换：yard 未指定名称，保持 yard。
	if rec.State.Characters[0].Location != "yard" {
		t.Fatalf("hero 位置转换错误: %+v", rec.State.Characters[0])
	}
	// hero 持有的 gold 已在 v2 规则中 -> coin。
	if rec.State.Characters[0].Items[0].Item != "coin" || rec.State.Characters[0].Items[0].Count != 1 {
		t.Fatalf("hero 物品转换错误: %+v", rec.State.Characters[0].Items)
	}
	// 旧记录仍保留原规则。
	old, err := a2.Record("s", latest.ID, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if old.State.Rules.Version != "v1" {
		t.Fatal("旧记录的规则被替换")
	}
	// 历史增加一条。
	hist, err := a2.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 {
		t.Fatalf("迁移后历史应有 3 条，得到 %d", len(hist))
	}
}

func TestMigrateEmptySource(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	_, err := a.Migrate("s", []string{"v1"}, migrateTarget(), locationMappings(), itemMappings(), "")
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("来源标识为空应返回 ConflictError，得到 %T: %v", err, err)
	}
}

func TestMigrateStaleSource(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	// 先用普通覆盖推进槽。
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
	// 用过时的首条记录标识迁移。
	_, err = a.Migrate("s", []string{"v1"}, migrateTarget(), locationMappings(), itemMappings(), info.ID)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("来源标识过期应返回 ConflictError，得到 %T: %v", err, err)
	}
}

func TestMigrateBlockedNoWrite(t *testing.T) {
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
	// 目标规则删除 hall 地点与 gold 种类，且不提供对应关系：迁移被阻碍。
	target := Rules{
		Version:     "v2",
		Locations:   []string{"yard"},
		ItemKinds:   []string{"key"},
		CarryLimits: map[string]int{"hero": 0, "mage": 0},
	}
	res, err := a.Migrate("s", []string{"v1"}, target, nil, nil, info.ID)
	if err == nil {
		t.Fatal("有阻碍时迁移应被拒绝")
	}
	if res.Preview.Committable {
		t.Fatal("被拒绝的迁移结果应标记为不可提交")
	}
	if len(res.Preview.Blockers) == 0 {
		t.Fatal("被拒绝的迁移应返回阻碍信息")
	}
	// 槽指针与历史不变。
	ptrAfter, err := a.readLatestLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	if ptrAfter != ptrBefore {
		t.Fatal("被拒绝的迁移改变了槽指针")
	}
	histAfter, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(histAfter) != len(histBefore) {
		t.Fatal("被拒绝的迁移增加了历史记录")
	}
	// 重新打开后仍是旧记录。
	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != info.ID {
		t.Fatal("被拒绝的迁移后槽指针不应指向新记录")
	}
}

func TestMigrateRecomputesFromSource(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	latest, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	// 先预览，然后篡改预览返回的状态；正式迁移应重新读取和计算，
	// 不保存调用方改过的预览状态。
	pv, err := a.PreviewMigration("s", []string{"v1"}, migrateTarget(), locationMappings(), itemMappings())
	if err != nil {
		t.Fatal(err)
	}
	pv.State.Characters[0].Location = "FAKE"
	pv.State.Characters[0].Items[0].Item = "FAKE"

	res, err := a.Migrate("s", []string{"v1"}, migrateTarget(), locationMappings(), itemMappings(), latest.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a.Latest("s", []string{"v1", "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != res.Record.ID {
		t.Fatal("槽指针应指向迁移后的记录")
	}
	if rec.State.Characters[0].Location != "great_hall" || rec.State.Characters[0].Items[0].Item != "coin" {
		t.Fatalf("正式迁移应重新计算状态，不采信被篡改的预览: %+v", rec.State.Characters[0])
	}
}

func TestMigrateSourceMissing(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	deleteRecord(t, dir, info.ID)
	_, err := a.Migrate("s", []string{"v1"}, migrateTarget(), locationMappings(), itemMappings(), info.ID)
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("来源缺失应返回 *NotFoundError，得到 %T: %v", err, err)
	}
}

func TestMigrateSourceCorrupt(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	corruptChecksum(t, dir, info.ID)
	_, err := a.Migrate("s", []string{"v1"}, migrateTarget(), locationMappings(), itemMappings(), info.ID)
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("来源损坏应返回 *CorruptError，得到 %T: %v", err, err)
	}
}

func TestMigrateSourceVersionRejected(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	latest, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Migrate("s", []string{"v9"}, migrateTarget(), locationMappings(), itemMappings(), latest.ID)
	var vre *VersionRejectedError
	if !errors.As(err, &vre) {
		t.Fatalf("来源版本不被接受应返回 *VersionRejectedError，得到 %T: %v", err, err)
	}
}

func TestMigrateRecoverFindsOld(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	latest, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Migrate("s", []string{"v1"}, migrateTarget(), locationMappings(), itemMappings(), latest.ID); err != nil {
		t.Fatal(err)
	}
	// 只接受旧版本的恢复读取应能找到迁移前最近一份可用记录。
	a2, _ := Open(dir)
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("恢复读取应找到迁移前记录: %v", err)
	}
	if rec.ID != info.ID {
		t.Fatalf("应恢复到迁移前记录 %s，得到 %s", info.ID, rec.ID)
	}
	// 只接受新版本的普通读取应返回迁移后记录。
	newest, err := a2.Latest("s", []string{"v2"})
	if err != nil {
		t.Fatal(err)
	}
	if newest.State.Rules.Version != "v2" {
		t.Fatal("最新记录应使用 v2 规则")
	}
}

func TestMigrateReopenAndContinue(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	if _, err := a.Migrate("s", []string{"v1"}, migrateTarget(), locationMappings(), itemMappings(), info.ID); err != nil {
		t.Fatal(err)
	}
	// 重新打开存档，读取新记录并继续提交。
	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v1", "v2"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatalf("从迁移后记录重建世界失败: %v", err)
	}
	// 在目标规则下继续推进时间。
	next, err := w.Apply(Commit{Time: 5})
	if err != nil {
		t.Fatalf("迁移后继续提交失败: %v", err)
	}
	if next.Time != 5 {
		t.Fatalf("迁移后时间片推进错误: %d", next.Time)
	}
	// 目标规则允许 great_hall->tower 移动（v1 中不允许），验证迁移后按新规则处理。
	if _, err := w.Apply(Commit{Moves: []Move{{Character: "hero", To: "tower"}}, Time: 6}); err != nil {
		t.Fatalf("迁移后应按目标规则允许 great_hall->tower 移动: %v", err)
	}
}

func TestMigrateBranchUnaffected(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	// 从首条记录分出分支 b。
	if _, err := a.Branch("s", info.ID, "b"); err != nil {
		t.Fatal(err)
	}
	// 迁移 s。
	if _, err := a.Migrate("s", []string{"v1"}, migrateTarget(), locationMappings(), itemMappings(), info.ID); err != nil {
		t.Fatal(err)
	}
	// 分支 b 仍保留原规则，不受迁移影响。
	brec, err := a.Latest("b", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if brec.State.Rules.Version != "v1" {
		t.Fatal("分支规则被迁移影响")
	}
	// 分支的历史链不应跨入 s 的迁移记录。
	bhist, err := a.History("b")
	if err != nil {
		t.Fatal(err)
	}
	if len(bhist) != 1 {
		t.Fatalf("分支历史应只有首条记录，得到 %+v", bhist)
	}
}

func TestMigrateCallerMutationIsolation(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	target := migrateTarget()
	locs := locationMappings()
	items := itemMappings()
	res, err := a.Migrate("s", []string{"v1"}, target, locs, items, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 迁移后调用方修改目标规则、对应关系或结果，不应影响已保存的数据。
	target.Version = "MUTATED"
	target.Locations[0] = "MUTATED"
	locs[0].To = "MUTATED"
	items[0].To = "MUTATED"
	res.Preview.Blockers = append(res.Preview.Blockers, Blocker{Character: "fake"})
	res.Preview.Committable = false

	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v2"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.State.Rules.Version != "v2" {
		t.Fatalf("已保存规则被调用方修改影响: version=%s", rec.State.Rules.Version)
	}
	if rec.State.Rules.Locations[0] != "great_hall" {
		t.Fatalf("已保存地点被调用方修改影响: %v", rec.State.Rules.Locations)
	}
	if rec.State.Characters[0].Location != "great_hall" {
		t.Fatalf("已保存角色位置被调用方修改影响: %v", rec.State.Characters[0].Location)
	}
}

func TestMigrateConcurrent(t *testing.T) {
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
			_, uerr := ga.Migrate("s", []string{"v1"}, migrateTarget(), locationMappings(), itemMappings(), info.ID)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case uerr == nil:
				wins++
			case isConflict(uerr):
				conflicts++
			default:
				errs = append(errs, uerr)
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
	// 最终槽指向迁移后的完整记录。
	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v1", "v2"})
	if err != nil {
		t.Fatalf("并发迁移后最新记录应完好: %v", err)
	}
	if rec.State.Rules.Version != "v2" {
		t.Fatal("胜出记录应使用目标规则")
	}
}

func TestMigrateVersusOtherWritersConcurrent(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")

	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, conflicts := 0, 0
	errs := make([]error, 0)
	// 迁移、升级、普通覆盖、确认恢复都基于同一父记录。
	for i := 0; i < 4; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			ga, _ := Open(dir)
			_, err := ga.Migrate("s", []string{"v1"}, migrateTarget(), locationMappings(), itemMappings(), info.ID)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if isConflict(err) {
				conflicts++
			} else {
				errs = append(errs, err)
			}
		}()
		go func() {
			defer wg.Done()
			ga, _ := Open(dir)
			_, err := ga.Upgrade("s", []string{"v1"}, v2Rules(), info.ID)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if isConflict(err) {
				conflicts++
			} else {
				errs = append(errs, err)
			}
		}()
		go func() {
			defer wg.Done()
			ga, _ := Open(dir)
			w := baseWorld(t)
			_, err := ga.Replace("s", w, info.ID)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if isConflict(err) {
				conflicts++
			} else {
				errs = append(errs, err)
			}
		}()
		go func() {
			defer wg.Done()
			ga, _ := Open(dir)
			_, err := ga.ConfirmRecovery("s", info.ID, info.ID, []string{"v1"})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if isConflict(err) {
				conflicts++
			} else {
				errs = append(errs, err)
			}
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

func TestMigrateBlockedErrorMessage(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	target := Rules{
		Version:     "v2",
		Locations:   []string{"yard"},
		ItemKinds:   []string{"key"},
		CarryLimits: map[string]int{"hero": 0, "mage": 0},
	}
	res, err := a.Migrate("s", []string{"v1"}, target, nil, nil, mustLatest(t, a, "s"))
	if err == nil {
		t.Fatal("应返回错误")
	}
	// 错误信息应包含阻碍数量。
	if !strings.Contains(err.Error(), "阻碍") {
		t.Fatalf("错误信息应提及阻碍，得到: %v", err)
	}
	// 结果中应包含完整阻碍信息。
	if len(res.Preview.Blockers) == 0 {
		t.Fatal("结果应包含阻碍信息")
	}
	for _, b := range res.Preview.Blockers {
		if b.Character == "" {
			t.Fatalf("阻碍应说明角色: %+v", b)
		}
		switch b.Kind {
		case BlockerLocation:
			if b.Location == "" {
				t.Fatalf("地点阻碍应说明缺失地点: %+v", b)
			}
		case BlockerItem:
			if b.Item == "" {
				t.Fatalf("物品阻碍应说明被移除物品: %+v", b)
			}
		case BlockerLimit:
			if b.Total <= b.Limit {
				t.Fatalf("上限阻碍应说明总量与新上限: %+v", b)
			}
		}
	}
}
