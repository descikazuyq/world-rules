package world

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

// v2Rules 是在 v1 基础上“只增不减”的目标规则：新增地点、连通关系和
// 物品种类，保留全部旧内容并提高上限，因此原样承接当前状态没有阻碍。
func v2Rules() Rules {
	return Rules{
		Version:   "v2",
		Locations: []string{"hall", "yard", "cave", "tower"},
		Edges: []Edge{
			{From: "hall", To: "yard"},
			{From: "yard", To: "cave"},
			{From: "hall", To: "tower"},
		},
		ItemKinds:   []string{"gold", "key", "gem"},
		CarryLimits: map[string]int{"hero": 10, "mage": 5},
	}
}

// saveBase 保存一个基础世界并返回槽名与首条记录元信息。
func saveBase(t *testing.T, a *Archive, slot string) RecordInfo {
	t.Helper()
	w := baseWorld(t)
	info, err := a.Save(slot, w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return info
}

func TestCheckUpgradeCompatible(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")

	check, err := a.CheckUpgrade("s", []string{"v1"}, v2Rules())
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	if !check.Compatible {
		t.Fatalf("只增不减的规则应当兼容，得到阻碍: %+v", check.Blockers)
	}
	if check.RecordID != info.ID {
		t.Fatalf("检查的记录标识应为 %s，得到 %s", info.ID, check.RecordID)
	}
	if check.OldVersion != "v1" || check.TargetVersion != "v2" {
		t.Fatalf("版本信息错误: %+v", check)
	}
	if len(check.Blockers) != 0 {
		t.Fatalf("兼容时不应有阻碍，得到 %+v", check.Blockers)
	}
}

func TestCheckUpgradeBlockers(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")

	// 目标规则：删除 hero 所在的 hall、移除 hero 持有的 gold、把 hero 上限
	// 降到 0。三类阻碍应一次全部返回。
	target := Rules{
		Version:     "v2",
		Locations:   []string{"yard", "cave"},
		Edges:       []Edge{{From: "yard", To: "cave"}},
		ItemKinds:   []string{"key"},
		CarryLimits: map[string]int{"hero": 0, "mage": 5},
	}
	check, err := a.CheckUpgrade("s", []string{"v1"}, target)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	if check.Compatible {
		t.Fatal("存在阻碍时不应兼容")
	}
	want := []Blocker{
		{Character: "hero", Kind: BlockerLocation, Location: "hall"},
		{Character: "hero", Kind: BlockerItem, Item: "gold"},
		{Character: "hero", Kind: BlockerLimit, Total: 1, Limit: 0},
	}
	if len(check.Blockers) != len(want) {
		t.Fatalf("期望 %d 条阻碍，得到 %d: %+v", len(want), len(check.Blockers), check.Blockers)
	}
	for i := range want {
		if check.Blockers[i] != want[i] {
			t.Fatalf("第 %d 条阻碍应为 %+v，得到 %+v", i, want[i], check.Blockers[i])
		}
	}
}

func TestCheckUpgradeCountZeroStillRequiresKind(t *testing.T) {
	a, _ := newTestArchive(t)
	// hero 持有 gold 但数量为 0；目标规则移除 gold 种类仍应算阻碍。
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
	check, err := a.CheckUpgrade("s", []string{"v1"}, target)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	if check.Compatible {
		t.Fatal("数量为零的已列物品被移除种类仍应算阻碍")
	}
	found := false
	for _, b := range check.Blockers {
		if b.Character == "hero" && b.Kind == BlockerItem && b.Item == "gold" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应包含 hero/gold 物品种类阻碍，得到 %+v", check.Blockers)
	}
}

func TestCheckUpgradeNoLimitMeansUnlimited(t *testing.T) {
	a, _ := newTestArchive(t)
	// hero 携带 5 件 gold，上限也是 5；目标规则不设 hero 上限，
	// 因此不应有上限阻碍。
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: baseRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 5}}},
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
		ItemKinds:   []string{"gold"},
		CarryLimits: map[string]int{}, // 不设上限
	}
	check, err := a.CheckUpgrade("s", []string{"v1"}, target)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	if !check.Compatible {
		t.Fatalf("目标没有该角色上限时应不设上限，得到阻碍: %+v", check.Blockers)
	}
}

func TestCheckUpgradeBlockersSorted(t *testing.T) {
	a, _ := newTestArchive(t)
	// 故意按与标识不同的顺序插入角色，验证阻碍按角色标识排序。
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: baseRules(),
		Characters: []Character{
			{ID: "zara", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
			{ID: "anna", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
			{ID: "mike", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	// 移除 hall 地点与 gold 种类，并把三人上限都降到 0。
	target := Rules{
		Version:     "v2",
		Locations:   []string{"yard"},
		ItemKinds:   []string{"key"},
		CarryLimits: map[string]int{"zara": 0, "anna": 0, "mike": 0},
	}
	check, err := a.CheckUpgrade("s", []string{"v1"}, target)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	// 期望顺序：anna(location, item, limit)、mike(...)、zara(...)。
	wantOrder := []string{"anna", "anna", "anna", "mike", "mike", "mike", "zara", "zara", "zara"}
	if len(check.Blockers) != len(wantOrder) {
		t.Fatalf("期望 %d 条阻碍，得到 %d: %+v", len(wantOrder), len(check.Blockers), check.Blockers)
	}
	for i, want := range wantOrder {
		if check.Blockers[i].Character != want {
			t.Fatalf("第 %d 条阻碍角色应为 %s，得到 %s", i, want, check.Blockers[i].Character)
		}
	}
	// 重复检查输出顺序一致。
	check2, err := a.CheckUpgrade("s", []string{"v1"}, target)
	if err != nil {
		t.Fatal(err)
	}
	for i := range check.Blockers {
		if check.Blockers[i] != check2.Blockers[i] {
			t.Fatalf("重复检查输出顺序不一致: %+v vs %+v", check.Blockers, check2.Blockers)
		}
	}
}

func TestCheckUpgradeIllegalTarget(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	target := v2Rules()
	target.Version = "" // 非法：版本为空
	_, err := a.CheckUpgrade("s", []string{"v1"}, target)
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("目标规则非法应返回 *RuleError，得到 %T: %v", err, err)
	}
}

func TestCheckUpgradeSameVersion(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	target := v2Rules()
	target.Version = "v1" // 与来源版本相同
	_, err := a.CheckUpgrade("s", []string{"v1"}, target)
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("版本相同应返回 *RuleError，得到 %T: %v", err, err)
	}
}

func TestCheckUpgradeCorruptSource(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	// 篡改最新记录的校验和。
	path := recordPath(dir, info.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	i := strings.Index(s, `"checksum": "`)
	if i < 0 {
		t.Fatal("找不到 checksum 字段")
	}
	start := i + len(`"checksum": "`)
	end := start + 64
	s = s[:start] + strings.Repeat("0", 64) + s[end:]
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = a.CheckUpgrade("s", []string{"v1"}, v2Rules())
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("来源损坏应返回 *CorruptError，得到 %T: %v", err, err)
	}
}

func TestCheckUpgradeVersionRejected(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	_, err := a.CheckUpgrade("s", []string{"v9"}, v2Rules())
	var vre *VersionRejectedError
	if !errors.As(err, &vre) {
		t.Fatalf("来源版本不被接受应返回 *VersionRejectedError，得到 %T: %v", err, err)
	}
}

func TestCheckUpgradeMissingSlot(t *testing.T) {
	a, _ := newTestArchive(t)
	_, err := a.CheckUpgrade("nope", []string{"v1"}, v2Rules())
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("槽不存在应返回 *NotFoundError，得到 %T: %v", err, err)
	}
}

func TestCheckUpgradeDoesNotMutate(t *testing.T) {
	a, dir := newTestArchive(t)
	saveBase(t, a, "s")
	ptrBefore, err := a.readLatestLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	histBefore, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	target := v2Rules()
	if _, err := a.CheckUpgrade("s", []string{"v1"}, target); err != nil {
		t.Fatal(err)
	}
	// 槽指针与历史不变。
	ptrAfter, err := a.readLatestLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	if ptrAfter != ptrBefore {
		t.Fatal("检查改变了槽指针")
	}
	histAfter, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(histAfter) != len(histBefore) {
		t.Fatal("检查改变了历史长度")
	}
	// 目标规则不被检查改写。
	if target.Version != "v2" || len(target.Locations) != 4 {
		t.Fatal("检查改写了目标规则")
	}
	// 重新打开存档后一切照旧。
	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("检查后读取旧版本记录应完好: %v", err)
	}
	if rec.State.Rules.Version != "v1" {
		t.Fatal("检查替换了记录中的规则")
	}
}

func TestUpgradeSuccess(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	// 推进时间并移动角色，验证升级后这些状态保持原样。
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

	target := v2Rules()
	res, err := a.Upgrade("s", []string{"v1"}, target, latest.ID)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if !res.Check.Compatible {
		t.Fatalf("兼容升级不应有阻碍: %+v", res.Check.Blockers)
	}
	if res.Record.ID == "" || res.Record.ID == latest.ID {
		t.Fatal("升级应产生新记录标识")
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
		t.Fatal("槽指针应指向升级后的记录")
	}
	// 新记录保存目标规则。
	if rec.State.Rules.Version != "v2" || len(rec.State.Rules.Locations) != 4 {
		t.Fatalf("新记录应保存目标规则: %+v", rec.State.Rules)
	}
	// 种子、时间片、角色位置及物品数量和排列保持原样。
	if rec.State.Seed != 42 || rec.State.Time != 3 {
		t.Fatalf("种子/时间片未保持原样: seed=%d time=%d", rec.State.Seed, rec.State.Time)
	}
	if rec.State.Characters[0].Location != "yard" {
		t.Fatalf("角色位置未保持原样: %+v", rec.State.Characters)
	}
	if rec.State.Characters[0].Items[0].Item != "gold" || rec.State.Characters[0].Items[0].Count != 1 {
		t.Fatalf("物品数量/排列未保持原样: %+v", rec.State.Characters[0].Items)
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
		t.Fatalf("升级后历史应有 3 条，得到 %d", len(hist))
	}
}

func TestUpgradeBlockedNoWrite(t *testing.T) {
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

	target := Rules{
		Version:     "v2",
		Locations:   []string{"yard"},
		ItemKinds:   []string{"key"},
		CarryLimits: map[string]int{"hero": 0, "mage": 0},
	}
	res, err := a.Upgrade("s", []string{"v1"}, target, info.ID)
	if err == nil {
		t.Fatal("有阻碍时升级应被拒绝")
	}
	if res.Check.Compatible {
		t.Fatal("被拒绝的升级结果应标记为不兼容")
	}
	if len(res.Check.Blockers) == 0 {
		t.Fatal("被拒绝的升级应返回阻碍信息")
	}
	// 槽指针与历史不变。
	ptrAfter, err := a.readLatestLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	if ptrAfter != ptrBefore {
		t.Fatal("被拒绝的升级改变了槽指针")
	}
	histAfter, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(histAfter) != len(histBefore) {
		t.Fatal("被拒绝的升级增加了历史记录")
	}
	// 重新打开后仍是旧记录。
	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != info.ID {
		t.Fatal("被拒绝的升级后槽指针不应指向新记录")
	}
}

func TestUpgradeEmptyExpected(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	_, err := a.Upgrade("s", []string{"v1"}, v2Rules(), "")
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("预期标识为空应返回 ConflictError，得到 %T: %v", err, err)
	}
}

func TestUpgradeStaleExpected(t *testing.T) {
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
	// 用过时的首条记录标识升级。
	_, err = a.Upgrade("s", []string{"v1"}, v2Rules(), info.ID)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("预期标识过期应返回 ConflictError，得到 %T: %v", err, err)
	}
}

func TestUpgradeConcurrent(t *testing.T) {
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
			// 每个 goroutine 使用独立的 Archive 实例（独立 flock 句柄）。
			ga, err := Open(dir)
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			_, uerr := ga.Upgrade("s", []string{"v1"}, v2Rules(), info.ID)
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
		t.Fatalf("并发升级出现非冲突错误: %v", errs)
	}
	if wins != 1 || conflicts != n-1 {
		t.Fatalf("期望恰好 1 成功 %d 冲突，得到 %d 成功 %d 冲突", n-1, wins, conflicts)
	}
	// 最终槽指向升级后的完整记录。
	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v1", "v2"})
	if err != nil {
		t.Fatalf("并发升级后最新记录应完好: %v", err)
	}
	if rec.State.Rules.Version != "v2" {
		t.Fatal("胜出记录应使用目标规则")
	}
}

func TestUpgradeVersusReplaceConcurrent(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")

	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, conflicts := 0, 0
	errs := make([]error, 0)
	// 一半升级，一半普通覆盖，都基于同一父记录。
	for i := 0; i < 6; i++ {
		wg.Add(2)
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
	}
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("竞争出现非冲突错误: %v", errs)
	}
	if wins != 1 || conflicts != 11 {
		t.Fatalf("期望恰好 1 成功 11 冲突，得到 %d 成功 %d 冲突", wins, conflicts)
	}
}

func isConflict(err error) bool {
	var ce *ConflictError
	return errors.As(err, &ce)
}

func TestUpgradeRecoverFindsPreUpgrade(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	if _, err := a.Upgrade("s", []string{"v1"}, v2Rules(), info.ID); err != nil {
		t.Fatal(err)
	}
	// 只接受旧版本的恢复读取应能找到升级前最近一份可用记录。
	a2, _ := Open(dir)
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("恢复读取应找到升级前记录: %v", err)
	}
	if rec.ID != info.ID {
		t.Fatalf("应恢复到升级前记录 %s，得到 %s", info.ID, rec.ID)
	}
	// 只接受新版本的普通读取应返回升级后记录。
	latest, err := a2.Latest("s", []string{"v2"})
	if err != nil {
		t.Fatal(err)
	}
	if latest.State.Rules.Version != "v2" {
		t.Fatal("最新记录应使用 v2 规则")
	}
}

func TestUpgradeReopenAndContinue(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	if _, err := a.Upgrade("s", []string{"v1"}, v2Rules(), info.ID); err != nil {
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
		t.Fatalf("从升级后记录重建世界失败: %v", err)
	}
	// 在目标规则下继续推进时间。
	next, err := w.Apply(Commit{Time: 5})
	if err != nil {
		t.Fatalf("升级后继续提交失败: %v", err)
	}
	if next.Time != 5 {
		t.Fatalf("升级后时间片推进错误: %d", next.Time)
	}
	// 目标规则允许 hall->tower 移动（v1 中不允许），验证升级后按新规则处理。
	if _, err := w.Apply(Commit{Moves: []Move{{Character: "hero", To: "tower"}}, Time: 6}); err != nil {
		t.Fatalf("升级后应按目标规则允许 hall->tower 移动: %v", err)
	}
}

func TestUpgradeCallerMutationIsolation(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	target := v2Rules()
	res, err := a.Upgrade("s", []string{"v1"}, target, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 升级后调用方修改目标规则，不应影响已保存的数据。
	target.Version = "MUTATED"
	target.Locations[0] = "MUTATED"
	target.CarryLimits["hero"] = 999
	// 修改检查结果也不应影响任何数据。
	res.Check.Blockers = append(res.Check.Blockers, Blocker{Character: "fake"})
	res.Check.Compatible = false

	a2, _ := Open(dir)
	rec, err := a2.Latest("s", []string{"v2"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.State.Rules.Version != "v2" {
		t.Fatalf("已保存规则被调用方修改影响: version=%s", rec.State.Rules.Version)
	}
	if rec.State.Rules.Locations[0] != "hall" {
		t.Fatalf("已保存地点被调用方修改影响: %v", rec.State.Rules.Locations)
	}
	if rec.State.Rules.CarryLimits["hero"] != 10 {
		t.Fatalf("已保存上限被调用方修改影响: %d", rec.State.Rules.CarryLimits["hero"])
	}
}

func TestUpgradeBranchUnaffected(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	// 从首条记录分出分支 b。
	if _, err := a.Branch("s", info.ID, "b"); err != nil {
		t.Fatal(err)
	}
	// 升级 s。
	if _, err := a.Upgrade("s", []string{"v1"}, v2Rules(), info.ID); err != nil {
		t.Fatal(err)
	}
	// 分支 b 仍保留原规则，不受升级影响。
	brec, err := a.Latest("b", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if brec.State.Rules.Version != "v1" {
		t.Fatal("分支规则被升级影响")
	}
	// 分支的历史链不应跨入 s 的升级记录；分支首记录以源记录为父。
	bhist, err := a.History("b")
	if err != nil {
		t.Fatal(err)
	}
	if len(bhist) != 1 {
		t.Fatalf("分支历史应只有首条记录，得到 %+v", bhist)
	}
	if bhist[0].Parent != info.ID || !bhist[0].SlotFirst {
		t.Fatalf("分支首记录应以源记录为父且为槽首记录，得到 %+v", bhist[0])
	}
}

func TestUpgradeVersionsComparedByEquality(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	// 版本只按相等判断：v1 -> v100 也允许（不按数字大小限制升级）。
	target := v2Rules()
	target.Version = "v100"
	res, err := a.Upgrade("s", []string{"v1"}, target, mustLatest(t, a, "s"))
	if err != nil {
		t.Fatalf("v1 -> v100 应允许: %v", err)
	}
	if res.Record.Version != "v100" {
		t.Fatalf("新版本应为 v100，得到 %s", res.Record.Version)
	}
}

func mustLatest(t *testing.T, a *Archive, slot string) RecordID {
	t.Helper()
	rec, err := a.Latest(slot, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	return rec.ID
}

func TestUpgradeBlockedErrorMessage(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	target := Rules{
		Version:     "v2",
		Locations:   []string{"yard"},
		ItemKinds:   []string{"key"},
		CarryLimits: map[string]int{"hero": 0, "mage": 0},
	}
	res, err := a.Upgrade("s", []string{"v1"}, target, mustLatest(t, a, "s"))
	if err == nil {
		t.Fatal("应返回错误")
	}
	// 错误信息应包含阻碍数量。
	if !strings.Contains(err.Error(), "阻碍") {
		t.Fatalf("错误信息应提及阻碍，得到: %v", err)
	}
	// 结果中应包含完整阻碍信息。
	if len(res.Check.Blockers) == 0 {
		t.Fatal("结果应包含阻碍信息")
	}
	// 每条阻碍都应说明角色及具体原因。
	for _, b := range res.Check.Blockers {
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

// 确保 fmt 包被使用（用于调试输出）。
var _ = fmt.Sprintf
