package world

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// v2Rules 是一份合法且与基础状态兼容的目标规则：增加地点、连通与
// 物品种类，收紧 hero 上限到 3（基础状态 hero 仅持有 1 个 gold），
// mage 不给上限（表示不设限）。
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
		CarryLimits: map[string]int{"hero": 3},
	}
}

func saveBase(t *testing.T, a *Archive, slot string) RecordInfo {
	t.Helper()
	info, err := a.Save(slot, baseWorld(t))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return info
}

func TestCheckUpgradeCompatibleAndReadonly(t *testing.T) {
	a, dir := newTestArchive(t)
	r0 := saveBase(t, a, "s")

	target := v2Rules()
	chk, err := a.CheckUpgrade("s", []string{"v1"}, target)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	if !chk.Compatible || len(chk.Blockers) != 0 {
		t.Fatalf("兼容目标应无阻碍: %+v", chk)
	}
	if chk.Record != r0.ID || chk.OldVersion != "v1" || chk.TargetVersion != "v2" {
		t.Fatalf("检查结果元信息错误: %+v", chk)
	}

	// 允许删去无人所在的地点、旧连通与无人列出的物品种类。
	shrunk := Rules{
		Version:     "v2",
		Locations:   []string{"hall", "yard"},
		Edges:       []Edge{{From: "hall", To: "yard"}},
		ItemKinds:   []string{"gold"},
		CarryLimits: map[string]int{"hero": 5, "mage": 2},
	}
	chk2, err := a.CheckUpgrade("s", []string{"v1"}, shrunk)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	if !chk2.Compatible {
		t.Fatalf("删去无人内容不应阻碍: %+v", chk2.Blockers)
	}

	// 目标中没有该角色上限 = 不设上限：mage 不给上限也兼容。
	noLimit := v2Rules()
	noLimit.CarryLimits = map[string]int{"hero": 3}
	if chk, err := a.CheckUpgrade("s", []string{"v1"}, noLimit); err != nil || !chk.Compatible {
		t.Fatalf("缺少上限应表示不设限: %+v %v", chk, err)
	}

	// 检查只读：槽指针、历史均不变，普通读取仍是 v1，且重复检查顺序一致。
	ptr, err := a.readLatestLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	if ptr != r0.ID {
		t.Fatal("检查改写了槽指针")
	}
	hist, err := a.History("s")
	if err != nil || len(hist) != 1 {
		t.Fatalf("检查不应增加历史: %v %v", hist, err)
	}
	if _, err := a.Latest("s", []string{"v1"}); err != nil {
		t.Fatalf("检查后旧版本读取应照常: %v", err)
	}

	// 修改传入规则不影响任何已保存数据，也不影响后续检查。
	target.Locations[0] = "mutated"
	target.CarryLimits["hero"] = -99
	if _, err := a.CheckUpgrade("s", []string{"v1"}, v2Rules()); err != nil {
		t.Fatalf("传入规则被调用方修改不应波及存档: %v", err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatalf("重新打开失败: %v", err)
	}
}

func TestCheckUpgradeBlockersAndOrder(t *testing.T) {
	a, _ := newTestArchive(t)
	// 构造多问题状态：
	//	hero 在 hall，持有 gold(1) 与 key(0)，总量 1；
	//	mage 在 yard，持有 gold(2)，总量 2。
	w, err := NewWorld(InitialData{
		Seed: 42,
		Rules: Rules{
			Version:     "v1",
			Locations:   []string{"hall", "yard", "cave"},
			Edges:       []Edge{{From: "hall", To: "yard"}},
			ItemKinds:   []string{"gold", "key"},
			CarryLimits: map[string]int{"hero": 5, "mage": 2},
		},
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: 1},
				{Item: "key", Count: 0},
			}},
			{ID: "mage", Location: "yard", Items: []CharacterItem{
				{Item: "gold", Count: 2},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}

	// hall、yard 都删；gold、key 都删；hero 上限收紧到 0、mage 到 1。
	target := Rules{
		Version:     "v2",
		Locations:   []string{"cave"},
		ItemKinds:   nil,
		CarryLimits: map[string]int{"hero": 0, "mage": 1},
	}
	chk, err := a.CheckUpgrade("s", []string{"v1"}, target)
	if err != nil {
		t.Fatal(err)
	}
	if chk.Compatible {
		t.Fatal("存在阻碍时 Compatible 应为 false")
	}
	want := []UpgradeBlocker{
		{Character: "hero", Kind: BlockerMissingLocation, Location: "hall"},
		{Character: "hero", Kind: BlockerRemovedItem, Item: "gold"},
		{Character: "hero", Kind: BlockerRemovedItem, Item: "key"}, // 数量为零也算
		{Character: "hero", Kind: BlockerOverLimit, Total: 1, Limit: 0},
		{Character: "mage", Kind: BlockerMissingLocation, Location: "yard"},
		{Character: "mage", Kind: BlockerRemovedItem, Item: "gold"},
		{Character: "mage", Kind: BlockerOverLimit, Total: 2, Limit: 1},
	}
	if !reflect.DeepEqual(chk.Blockers, want) {
		t.Fatalf("阻碍集合/顺序错误:\n got %+v\nwant %+v", chk.Blockers, want)
	}

	// 重复检查输出顺序一致。
	chk2, _ := a.CheckUpgrade("s", []string{"v1"}, target)
	if !reflect.DeepEqual(chk.Blockers, chk2.Blockers) {
		t.Fatal("重复检查结果不一致")
	}

	// 阻碍信息应具体说明角色、缺失地点/物品或实际总量与新上限。
	for _, b := range chk.Blockers {
		if b.Error() == "" {
			t.Fatal("阻碍缺少说明")
		}
	}
}

func TestCheckUpgradeErrors(t *testing.T) {
	a, _ := newTestArchive(t)
	r0 := saveBase(t, a, "s")

	expectRuleError := func(name string, target Rules) {
		t.Helper()
		if _, err := a.CheckUpgrade("s", []string{"v1"}, target); !errors.As(err, new(*RuleError)) {
			t.Fatalf("%s: 期望 RuleError，得到 %T: %v", name, err, err)
		}
	}

	// 目标规则非法。
	bad := v2Rules()
	bad.Version = ""
	expectRuleError("空版本", bad)
	bad = v2Rules()
	bad.Locations = []string{"hall", "hall"}
	expectRuleError("地点重复", bad)
	bad = v2Rules()
	bad.CarryLimits = map[string]int{"hero": -1}
	expectRuleError("负上限", bad)

	// 版本相同：只按相等判断。
	if _, err := a.CheckUpgrade("s", []string{"v1"}, func() Rules {
		r := v2Rules()
		r.Version = "v1"
		return r
	}()); !errors.As(err, new(*RuleError)) {
		t.Fatalf("版本相同应 RuleError，得到 %T: %v", err, err)
	}
	// 版本名数字更小也允许（不按大小限制升级方向）。
	down := v2Rules()
	down.Version = "v0"
	if chk, err := a.CheckUpgrade("s", []string{"v1"}, down); err != nil || !chk.Compatible {
		t.Fatalf("版本号数字更小不应报错: %+v %v", chk, err)
	}

	// 来源版本不被接受：直接放一条 v2 的最新记录。
	w := baseWorld(t)
	v2State := w.Snapshot()
	v2State.Rules.Version = "v2"
	r2 := writeRawRecord(t, a, r0.ID, false, v2State)
	updatePointer(t, a, "s", r2)
	_, err := a.CheckUpgrade("s", []string{"v1"}, v2RulesFor("v3"))
	if !errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("来源版本不被接受应 VersionRejectedError，得到 %T: %v", err, err)
	}

	// 来源损坏。
	if err := corruptChecksum(t, a.dir, r2); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CheckUpgrade("s", []string{"v1", "v2"}, v2RulesFor("v3")); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("来源损坏应 CorruptError，得到 %T: %v", err, err)
	}

	// 槽不存在沿用现有不存在错误；非法槽名同样按不存在处理。
	if _, err := a.CheckUpgrade("nope", []string{"v1"}, v2Rules()); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("槽不存在应 NotFoundError，得到 %T", err)
	}
	if _, err := a.CheckUpgrade("../x", []string{"v1"}, v2Rules()); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("非法槽名应 NotFoundError，得到 %T", err)
	}
}

func v2RulesFor(version string) Rules {
	r := v2Rules()
	r.Version = version
	return r
}

func corruptChecksum(t *testing.T, dir string, id RecordID) error {
	t.Helper()
	path := recordPath(dir, id)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := string(raw)
	i := strings.Index(s, `"checksum": "`)
	if i < 0 {
		return fmt.Errorf("找不到 checksum 字段")
	}
	start := i + len(`"checksum": "`)
	s = s[:start] + strings.Repeat("0", 64) + s[start+64:]
	return os.WriteFile(path, []byte(s), 0o600)
}

func TestUpgradeRulesSuccess(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	// 让 hero 持有两种物品并推进时间，验证数量、排列与时间片原样保留。
	if _, err := w.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "yard"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "key", Delta: 2}},
		Time:        5,
	}); err != nil {
		t.Fatal(err)
	}
	r0, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}

	// 目标规则删去无人所在的 cave 与其连通、删去旧边 hall-yard，
	// 新增 tower 与 yard-tower、新增 gem；hero 上限收紧到 3
	// （当前 gold1+key2=3）。升级时 hero 在 yard。
	target := Rules{
		Version:   "v2",
		Locations: []string{"hall", "yard", "tower"},
		Edges: []Edge{
			{From: "yard", To: "tower"},
		},
		ItemKinds:   []string{"gold", "key", "gem"},
		CarryLimits: map[string]int{"hero": 3, "mage": 2},
	}
	chk, err := a.CheckUpgrade("s", []string{"v1"}, target)
	if err != nil || !chk.Compatible {
		t.Fatalf("前置检查应兼容: %+v %v", chk, err)
	}

	info, check2, err := a.UpgradeRules("s", []string{"v1"}, target, r0.ID)
	if err != nil {
		t.Fatalf("UpgradeRules: %v", err)
	}
	if !check2.Compatible || check2.Record != r0.ID {
		t.Fatalf("成功升级的检查结果异常: %+v", check2)
	}
	if info.ID == "" || info.ID == r0.ID || info.Parent != r0.ID || info.Version != "v2" || info.SlotFirst {
		t.Fatalf("新记录元信息错误: %+v", info)
	}

	// 重新打开后能读取新记录；普通读取保留版本接受方式，不自动升级。
	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a2.Latest("s", []string{"v1"}); !errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("旧版本读取不应自动升级，得到 %T: %v", err, err)
	}
	rec, err := a2.Latest("s", []string{"v2"})
	if err != nil {
		t.Fatalf("新版本读取失败: %v", err)
	}
	if rec.ID != info.ID || rec.Parent != r0.ID || rec.SlotFirst {
		t.Fatalf("新记录关系错误: %+v", rec)
	}
	st := rec.State
	if st.Seed != 42 || st.Time != 5 {
		t.Fatalf("种子或时间片未原样保留: %+v", st)
	}
	if !reflect.DeepEqual(st.Rules, target) {
		t.Fatalf("规则未完整保存:\n got %+v\nwant %+v", st.Rules, target)
	}
	if len(st.Characters) != 2 ||
		st.Characters[0].ID != "hero" || st.Characters[0].Location != "yard" ||
		!reflect.DeepEqual(st.Characters[0].Items,
			[]CharacterItem{{Item: "gold", Count: 1}, {Item: "key", Count: 2}}) {
		t.Fatalf("角色位置或物品数量/排列未原样保留: %+v", st.Characters)
	}

	// 升级后移动与物品变化按目标规则处理：旧边已删、cave 已删、
	// gem 是新增物品种类。
	nw, err := WorldFromState(st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nw.Apply(Commit{Moves: []Move{{Character: "hero", To: "hall"}}, Time: 6}); err == nil {
		t.Fatal("旧连通 hall-yard 已删，移动应被拒绝")
	}
	afterMove, err := nw.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "tower"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gem", Delta: 0}},
		Time:        6,
	})
	if err != nil {
		t.Fatalf("按新规则的移动/物品应成功: %v", err)
	}
	if afterMove.Characters[0].Location != "tower" {
		t.Fatalf("应沿新边移动到 tower: %+v", afterMove.Characters[0])
	}
	if _, err := nw.Apply(Commit{ItemChanges: []ItemChange{{Character: "hero", Item: "gem", Delta: 1}}, Time: 7}); err == nil {
		t.Fatal("总量 3 已达新上限，再加应被拒绝")
	}

	// 升级后可以继续普通覆盖提交。
	if _, err := a2.Replace("s", nw, info.ID); err != nil {
		t.Fatalf("升级后覆盖失败: %v", err)
	}

	// 旧记录保留原规则；只接受旧版本的恢复读取找到升级前最近一份。
	old, err := a2.Record("s", r0.ID, []string{"v1"})
	if err != nil {
		t.Fatalf("旧记录应仍可读: %v", err)
	}
	if old.State.Rules.Version != "v1" {
		t.Fatal("旧记录规则被改动")
	}
	rec1, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("旧版本恢复读取失败: %v", err)
	}
	if rec1.ID != r0.ID {
		t.Fatalf("恢复读取应落到升级前记录 %s，得到 %s", r0.ID, rec1.ID)
	}
	hist, err := a2.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 {
		t.Fatalf("升级加一次覆盖后历史应为 3 条，得到 %d", len(hist))
	}

	// 调用方修改读回状态不影响存档（先覆盖再读，指针仍为最新）。
	st.Characters[0].Location = "hacked"
	again, err := a2.Latest("s", []string{"v2"})
	if err != nil {
		t.Fatal(err)
	}
	if again.State.Characters[0].Location != "tower" {
		t.Fatal("修改读回状态影响了存档")
	}
}

func TestUpgradeRulesRejectedLeavesNoTrace(t *testing.T) {
	a, _ := newTestArchive(t)
	r0 := saveBase(t, a, "s")

	// 有阻碍：返回同样的阻碍信息、零值元信息、无错误，但拒绝写入。
	target := Rules{
		Version:     "v2",
		Locations:   []string{"yard"}, // hall 被删
		ItemKinds:   []string{"key"},  // gold 被删
		CarryLimits: map[string]int{"hero": 0, "mage": 2},
	}
	chk, err := a.CheckUpgrade("s", []string{"v1"}, target)
	if err != nil || chk.Compatible {
		t.Fatalf("前置检查应报阻碍: %+v %v", chk, err)
	}
	info, got, err := a.UpgradeRules("s", []string{"v1"}, target, r0.ID)
	if err != nil {
		t.Fatalf("有阻碍时不应作为 error 返回: %v", err)
	}
	if info.ID != "" || got.Compatible {
		t.Fatalf("被拒绝升级不应返回新记录: %+v %+v", info, got)
	}
	if !reflect.DeepEqual(got.Blockers, chk.Blockers) {
		t.Fatalf("升级时的阻碍应与检查一致:\n got %+v\nwant %+v", got.Blockers, chk.Blockers)
	}
	ptr, _ := a.readLatestLocked("s")
	if ptr != r0.ID {
		t.Fatal("被拒绝升级改变了槽当前记录")
	}
	if hist, _ := a.History("s"); len(hist) != 1 {
		t.Fatalf("被拒绝升级不应增加历史，得到 %d 条", len(hist))
	}

	// 空预期标识 / 过期标识 -> 现有冲突错误，同样不写入。
	if _, _, err := a.UpgradeRules("s", []string{"v1"}, v2Rules(), ""); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("空标识应 ConflictError，得到 %T", err)
	}
	w := baseWorld(t)
	if _, err := w.Apply(Commit{Time: 1}); err != nil {
		t.Fatal(err)
	}
	r1, err := a.Replace("s", w, r0.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.UpgradeRules("s", []string{"v1"}, v2Rules(), r0.ID); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("过期标识应 ConflictError，得到 %T", err)
	}
	if hist, _ := a.History("s"); len(hist) != 2 {
		t.Fatalf("冲突升级不应增加历史，得到 %d 条", len(hist))
	}

	// 各类非法前提也不写入（在 r1 之上尝试）。
	if _, _, err := a.UpgradeRules("s", []string{"v9"}, v2Rules(), r1.ID); !errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("版本不被接受应报错，得到 %T", err)
	}
	same := v2Rules()
	same.Version = "v1"
	if _, _, err := a.UpgradeRules("s", []string{"v1"}, same, r1.ID); !errors.As(err, new(*RuleError)) {
		t.Fatalf("版本相同应 RuleError，得到 %T", err)
	}
	bad := v2Rules()
	bad.Locations = []string{"x", "x"}
	if _, _, err := a.UpgradeRules("s", []string{"v1"}, bad, r1.ID); !errors.As(err, new(*RuleError)) {
		t.Fatalf("非法目标规则应 RuleError，得到 %T", err)
	}
	ptr, _ = a.readLatestLocked("s")
	if ptr != r1.ID {
		t.Fatal("非法升级尝试改变了槽当前记录")
	}
}

func TestUpgradeRulesDoesNotAffectBranches(t *testing.T) {
	a, _ := newTestArchive(t)
	r0 := saveBase(t, a, "s")
	b0, err := a.Branch("s", r0.ID, "b")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.UpgradeRules("s", []string{"v1"}, v2Rules(), r0.ID); err != nil {
		t.Fatalf("UpgradeRules: %v", err)
	}

	bRec, err := a.Latest("b", []string{"v1"})
	if err != nil {
		t.Fatalf("分支槽读取失败: %v", err)
	}
	if bRec.ID != b0.ID || bRec.State.Rules.Version != "v1" {
		t.Fatalf("已分出的分支不应被升级影响: %+v", bRec)
	}
	sHist, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(sHist) != 2 || sHist[len(sHist)-1].ID != r0.ID {
		t.Fatalf("源槽历史链错误: %+v", sHist)
	}
}

func TestConcurrentUpgradeAcrossProcesses(t *testing.T) {
	if os.Getenv("WORLD_TEST_HELPER") != "" {
		t.Skip("子进程模式")
	}
	a, dir := newTestArchive(t)
	info, err := a.Save("s", baseWorld(t))
	if err != nil {
		t.Fatal(err)
	}

	const n = 8
	cmds := make([]*exec.Cmd, n)
	for i := range cmds {
		c := exec.Command(os.Args[0], "-test.run=TestMain", "-test.v")
		c.Env = append(os.Environ(),
			"WORLD_TEST_HELPER=concurrent_upgrade",
			"WORLD_TEST_DIR="+dir,
			"WORLD_TEST_PARENT="+string(info.ID),
			"WORLD_TEST_KIND="+func() string {
				if i%2 == 0 {
					return "upgrade"
				}
				return "replace"
			}(),
		)
		cmds[i] = c
	}

	wins, conflicts := 0, 0
	for _, c := range cmds {
		err := c.Run()
		switch {
		case err == nil:
			wins++
		case c.ProcessState.ExitCode() == 3:
			conflicts++
		default:
			t.Fatalf("子进程意外失败: %v", err)
		}
	}
	if wins != 1 || conflicts != n-1 {
		t.Fatalf("期望恰好 1 成功 %d 冲突，得到 %d 成功 %d 冲突", n-1, wins, conflicts)
	}

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a2.Latest("s", []string{"v1", "v2"})
	if err != nil {
		t.Fatalf("并发后最新记录应完好: %v", err)
	}
	if rec.Parent != info.ID {
		t.Fatalf("胜出记录的父应为 %s，得到 %s", info.ID, rec.Parent)
	}
	hist, err := a2.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("并发竞争只应留下一条新记录，历史长度应为 2，得到 %d", len(hist))
	}
	// 无论升级还是普通覆盖胜出，只接受旧版本的恢复读取都应落到首记录。
	rec1, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("旧版本恢复读取: %v", err)
	}
	if rec1.ID != info.ID {
		t.Fatalf("恢复读取应找到升级前记录 %s，得到 %s", info.ID, rec1.ID)
	}
}

// helperConcurrentUpgrade 在子进程中执行一次基于固定父记录的升级或
// 普通覆盖。成功退出码 0；冲突退出码 3；其他错误退出码 1。
func helperConcurrentUpgrade() {
	dir := os.Getenv("WORLD_TEST_DIR")
	parent := RecordID(os.Getenv("WORLD_TEST_PARENT"))

	a, err := Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch os.Getenv("WORLD_TEST_KIND") {
	case "replace":
		// 用与首存一致的 v1 状态构造世界；败方无需先成功读取最新记录
		// （胜者可能已写入 v2），CAS 比较直接给出冲突。
		w, err := NewWorld(InitialData{
			Seed:  42,
			Rules: baseRules(),
			Characters: []Character{
				{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
				{ID: "mage", Location: "yard"},
			},
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if _, err := a.Replace("s", w, parent); err != nil {
			reportHelperConflict(err)
		}
	case "upgrade":
		// 只接受 v1：败方在胜者写入 v2 后仍应收到冲突而非版本拒绝。
		if _, _, err := a.UpgradeRules("s", []string{"v1"}, v2Rules(), parent); err != nil {
			reportHelperConflict(err)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown kind")
		os.Exit(1)
	}
	os.Exit(0)
}

func reportHelperConflict(err error) {
	var ce *ConflictError
	if errors.As(err, &ce) {
		os.Exit(3)
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
