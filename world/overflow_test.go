package world

import (
	"errors"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"
)

// noLimitRules 是不设携带上限、含两个地点与两种物品的规则。
func noLimitRules() Rules {
	return Rules{
		Version:   "v1",
		Locations: []string{"hall", "yard"},
		Edges:     []Edge{{From: "hall", To: "yard"}},
		ItemKinds: []string{"gold", "key"},
	}
}

func mustRuleError(t *testing.T, err error, substrs ...string) {
	t.Helper()
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("应返回 *RuleError，得到 %T: %v", err, err)
	}
	for _, s := range substrs {
		if !strings.Contains(re.Reason, s) {
			t.Fatalf("错误信息应包含 %q: %v", s, re)
		}
	}
}

// 同一角色同一物品的增减与原始数量合并后判断：1 + MaxInt + MinInt = 0，
// 且与条目次序无关。
func TestApplyMergesDeltasExactly(t *testing.T) {
	deltas := [][]ItemChange{
		{{Character: "hero", Item: "gold", Delta: math.MaxInt}, {Character: "hero", Item: "gold", Delta: math.MinInt}},
		{{Character: "hero", Item: "gold", Delta: math.MinInt}, {Character: "hero", Item: "gold", Delta: math.MaxInt}},
	}
	for _, d := range deltas {
		w, err := NewWorld(InitialData{
			Seed:  1,
			Rules: noLimitRules(),
			Characters: []Character{
				{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
			},
		})
		if err != nil {
			t.Fatalf("NewWorld: %v", err)
		}
		st, err := w.Apply(Commit{ItemChanges: d, Time: 1})
		if err != nil {
			t.Fatalf("合并后结果为 0 应成功: %v", err)
		}
		items := st.Characters[0].Items
		if len(items) != 1 || items[0].Item != "gold" || items[0].Count != 0 {
			t.Fatalf("最终数量应为 0 且条目保留，得到 %+v", items)
		}
	}
}

// 中途暂时为负、最终合法时应成功，且次序不影响结果。
func TestApplyIntermediateNegativeAllowed(t *testing.T) {
	deltas := [][]ItemChange{
		{{Character: "hero", Item: "gold", Delta: -2}, {Character: "hero", Item: "gold", Delta: 5}},
		{{Character: "hero", Item: "gold", Delta: 5}, {Character: "hero", Item: "gold", Delta: -2}},
	}
	for _, d := range deltas {
		w, err := NewWorld(InitialData{
			Seed:  1,
			Rules: noLimitRules(),
			Characters: []Character{
				{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
			},
		})
		if err != nil {
			t.Fatalf("NewWorld: %v", err)
		}
		st, err := w.Apply(Commit{ItemChanges: d, Time: 1})
		if err != nil {
			t.Fatalf("最终数量 4 应成功: %v", err)
		}
		if got := st.Characters[0].Items[0].Count; got != 4 {
			t.Fatalf("最终数量应为 4，得到 %d", got)
		}
	}
}

// 最终数量超出 int 范围或为负时拒绝，并说明角色与物品。
func TestApplyFinalCountOutOfRange(t *testing.T) {
	cases := []struct {
		name   string
		count  int
		deltas []ItemChange
	}{
		{"正向溢出", 5, []ItemChange{{Character: "hero", Item: "gold", Delta: math.MaxInt}, {Character: "hero", Item: "gold", Delta: -3}}},
		{"直接到顶再加", math.MaxInt, []ItemChange{{Character: "hero", Item: "gold", Delta: 1}}},
		{"最终为负", 1, []ItemChange{{Character: "hero", Item: "gold", Delta: -2}}},
		{"负向越界", 0, []ItemChange{{Character: "hero", Item: "gold", Delta: math.MinInt}, {Character: "hero", Item: "gold", Delta: -1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := NewWorld(InitialData{
				Seed:  1,
				Rules: noLimitRules(),
				Characters: []Character{
					{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: tc.count}}},
				},
			})
			if err != nil {
				t.Fatalf("NewWorld: %v", err)
			}
			before := w.Snapshot()
			if _, err := w.Apply(Commit{ItemChanges: tc.deltas, Time: 1}); err == nil {
				t.Fatalf("最终数量非法应失败")
			} else {
				mustRuleError(t, err, "hero", "gold")
			}
			if !reflect.DeepEqual(before, w.Snapshot()) {
				t.Fatalf("失败后世界状态不应改变")
			}
		})
	}
}

// 有携带上限的角色按真实总量判断，不能因加法回绕而通过。
func TestApplyCarryLimitUsesTrueTotal(t *testing.T) {
	rules := noLimitRules()
	rules.CarryLimits = map[string]int{"hero": math.MaxInt}
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: rules,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	// 真实总量 MaxInt+1 超过上限 MaxInt；回绕求和会得到负数而误判通过。
	_, err = w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "key", Delta: math.MaxInt}},
		Time:        1,
	})
	mustRuleError(t, err, "hero", "上限")
}

// 未设上限的角色可携带多种物品，总量允许超过 int 最大值。
func TestApplyNoLimitAllowsHugeTotal(t *testing.T) {
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: noLimitRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: math.MaxInt}}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	st, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "key", Delta: math.MaxInt}},
		Time:        1,
	})
	if err != nil {
		t.Fatalf("无上限角色总量超过 int 最大值应允许: %v", err)
	}
	items := st.Characters[0].Items
	if len(items) != 2 || items[0].Count != math.MaxInt || items[1].Item != "key" || items[1].Count != math.MaxInt {
		t.Fatalf("每种数量应各自合法保留，得到 %+v", items)
	}
}

// 即使增减相互抵消，不存在的角色与不被允许的物品仍须拒绝。
func TestApplyUnknownStillRejectedWhenCancelling(t *testing.T) {
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: noLimitRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall"},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	_, err = w.Apply(Commit{ItemChanges: []ItemChange{
		{Character: "ghost", Item: "gold", Delta: 5},
		{Character: "ghost", Item: "gold", Delta: -5},
	}, Time: 1})
	mustRuleError(t, err, "ghost")
	_, err = w.Apply(Commit{ItemChanges: []ItemChange{
		{Character: "hero", Item: "gem", Delta: 5},
		{Character: "hero", Item: "gem", Delta: -5},
	}, Time: 1})
	mustRuleError(t, err, "hero", "gem")
}

// 任一角色失败时，整次提交的移动、物品变化与时间推进全部撤销。
func TestApplyFailureRollsBackEverything(t *testing.T) {
	rules := noLimitRules()
	rules.CarryLimits = map[string]int{"hero": math.MaxInt}
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: rules,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
			{ID: "mage", Location: "hall"},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	before := w.Snapshot()
	_, err = w.Apply(Commit{
		Moves: []Move{{Character: "mage", To: "yard"}},
		ItemChanges: []ItemChange{
			{Character: "mage", Item: "gold", Delta: 3},
			{Character: "hero", Item: "key", Delta: math.MaxInt}, // 真实总量超限
		},
		Time: 9,
	})
	mustRuleError(t, err, "hero")
	after := w.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("失败提交应整体撤销，之前 %+v 之后 %+v", before, after)
	}
}

// 建立世界与从完整状态继续时，携带上限同样按真实总量判断。
func TestBuildWorldLimitUsesTrueTotal(t *testing.T) {
	rules := noLimitRules()
	rules.CarryLimits = map[string]int{"hero": 5}
	chars := []Character{
		{ID: "hero", Location: "hall", Items: []CharacterItem{
			{Item: "gold", Count: math.MaxInt},
			{Item: "key", Count: math.MaxInt},
		}},
	}
	if _, err := NewWorld(InitialData{Seed: 1, Rules: rules, Characters: chars}); err == nil {
		t.Fatalf("真实总量超限应拒绝建立世界")
	} else {
		mustRuleError(t, err, "hero", "上限")
	}
	if _, err := WorldFromState(State{Seed: 1, Rules: rules, Characters: chars}); err == nil {
		t.Fatalf("真实总量超限应拒绝从状态继续")
	} else {
		mustRuleError(t, err, "hero", "上限")
	}
	// 未设上限时同样的总量合法。
	rules.CarryLimits = nil
	if _, err := NewWorld(InitialData{Seed: 1, Rules: rules, Characters: chars}); err != nil {
		t.Fatalf("无上限角色总量大应允许建立世界: %v", err)
	}
	if _, err := WorldFromState(State{Seed: 1, Rules: rules, Characters: chars}); err != nil {
		t.Fatalf("无上限角色总量大应允许从状态继续: %v", err)
	}
}

// 升级检查按真实总量判断：来源无上限、总量超过 int 最大值，目标设置
// 上限时必须报告上限阻碍，并给出完整十进制真实总量。
func TestCheckUpgradeTrueTotalOverflow(t *testing.T) {
	a, _ := newTestArchive(t)
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: noLimitRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: math.MaxInt},
				{Item: "key", Count: math.MaxInt},
			}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	info, err := a.Save("slot", w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	target := noLimitRules()
	target.Version = "v2"
	target.CarryLimits = map[string]int{"hero": 10}
	check, err := a.CheckUpgrade("slot", []string{"v1"}, target)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	if check.Compatible {
		t.Fatalf("真实总量超过目标上限应不兼容")
	}
	if len(check.Blockers) != 1 {
		t.Fatalf("应只有上限阻碍，得到 %+v", check.Blockers)
	}
	b := check.Blockers[0]
	if b.Character != "hero" || b.Kind != BlockerLimit || b.Limit != 10 {
		t.Fatalf("阻碍内容不符: %+v", b)
	}
	if !b.TotalOverflow {
		t.Fatalf("真实总量超出 int 范围时 TotalOverflow 应为真: %+v", b)
	}
	wantTotal := new(big.Int).Add(big.NewInt(math.MaxInt), big.NewInt(math.MaxInt)).String()
	if b.TotalText != wantTotal {
		t.Fatalf("TotalText 应为完整十进制真实总量 %s，得到 %q", wantTotal, b.TotalText)
	}

	// 正式升级不得保存，槽当前记录与历史不变。
	if _, err := a.Upgrade("slot", []string{"v1"}, target, info.ID); err == nil {
		t.Fatalf("有上限阻碍时升级应被拒绝")
	} else {
		mustRuleError(t, err)
	}
	latest, err := a.Latest("slot", []string{"v1"})
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.ID != info.ID {
		t.Fatalf("被拒绝的升级不应改变槽当前记录")
	}
	history, err := a.History("slot")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 || history[0].ID != info.ID {
		t.Fatalf("被拒绝的升级不应改变历史: %+v", history)
	}
}

// 真实总量能用 int 表示时，上限阻碍沿用 Total 字段且不置 TotalOverflow。
func TestCheckUpgradeTotalFitsInt(t *testing.T) {
	a, _ := newTestArchive(t)
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: noLimitRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 8}}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	if _, err := a.Save("slot", w); err != nil {
		t.Fatalf("Save: %v", err)
	}
	target := noLimitRules()
	target.Version = "v2"
	target.CarryLimits = map[string]int{"hero": 5}
	check, err := a.CheckUpgrade("slot", []string{"v1"}, target)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	want := Blocker{Character: "hero", Kind: BlockerLimit, Total: 8, Limit: 5}
	if len(check.Blockers) != 1 || check.Blockers[0] != want {
		t.Fatalf("阻碍应为 %+v，得到 %+v", want, check.Blockers)
	}
}

// writeIllegalLimitRecord 直接写入一条“校验和正确但违反携带上限”的记录
// （模拟旧版本回绕缺陷留下的存档），并把槽指针指向它。
func writeIllegalLimitRecord(t *testing.T, a *Archive, dir, slot string) (badID, goodID RecordID) {
	t.Helper()
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: noLimitRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	good, err := a.Save(slot, w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	// 构造违反携带上限的状态：上限 5，真实总量 1+6=7。
	st := w.Snapshot()
	st.Rules.CarryLimits = map[string]int{"hero": 5}
	st.Characters[0].Items = append(st.Characters[0].Items, CharacterItem{Item: "key", Count: 6})
	env := &envelope{
		Format: archiveFormatVersion,
		ID:     RecordID("r" + strings.Repeat("ab", 16)),
		Parent: good.ID,
		State:  st,
	}
	env.Checksum = computeChecksum(env)
	if err := writeRecord(dir, env); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}
	if err := a.persistSlotPointerLocked(slot, slotPointer{
		Latest:  env.ID,
		History: []RecordID{env.ID, good.ID},
	}); err != nil {
		t.Fatalf("persistSlotPointerLocked: %v", err)
	}
	return env.ID, good.ID
}

// 校验和正确但违反携带上限的记录：直接读取报损坏，恢复读取与预览按保存
// 次序跳过它，分支、确认恢复、升级与迁移都不能把它复制成新记录。
func TestReadRejectsLimitViolatingRecord(t *testing.T) {
	a, _ := newTestArchive(t)
	badID, goodID := writeIllegalLimitRecord(t, a, a.Dir(), "slot")
	versions := []string{"v1"}

	if _, err := a.Latest("slot", versions); err == nil {
		t.Fatalf("直接读取违反上限的记录应失败")
	} else {
		var ce *CorruptError
		if !errors.As(err, &ce) {
			t.Fatalf("应返回 *CorruptError，得到 %T: %v", err, err)
		}
	}
	if _, err := a.Record("slot", badID, versions); err == nil {
		t.Fatalf("按标识读取违反上限的记录应失败")
	} else {
		var ce *CorruptError
		if !errors.As(err, &ce) {
			t.Fatalf("应返回 *CorruptError，得到 %T: %v", err, err)
		}
	}

	rec, err := a.RecoverLatest("slot", versions)
	if err != nil {
		t.Fatalf("RecoverLatest 应跳过非法记录: %v", err)
	}
	if rec.ID != goodID {
		t.Fatalf("恢复应选中更早的合法记录 %s，得到 %s", goodID, rec.ID)
	}
	preview, err := a.PreviewRecovery("slot", versions)
	if err != nil {
		t.Fatalf("PreviewRecovery 应跳过非法记录: %v", err)
	}
	if preview.Current != badID || preview.Source != goodID {
		t.Fatalf("预览当前/来源应为 %s/%s，得到 %s/%s", badID, goodID, preview.Current, preview.Source)
	}

	// 分支不能把非法来源复制成新记录。
	if _, err := a.Branch("slot", badID, "dst"); err == nil {
		t.Fatalf("分支非法记录应被拒绝")
	} else {
		var ce *CorruptError
		if !errors.As(err, &ce) {
			t.Fatalf("分支非法记录应返回 *CorruptError，得到 %T: %v", err, err)
		}
	}
	if _, err := a.readLatestLocked("dst"); err == nil {
		t.Fatalf("被拒绝的分支不应创建目标槽")
	}

	// 确认恢复不能以非法记录为来源。
	if _, err := a.ConfirmRecovery("slot", badID, badID, versions); err == nil {
		t.Fatalf("以非法记录为来源的确认恢复应被拒绝")
	} else {
		var ce *CorruptError
		if !errors.As(err, &ce) {
			t.Fatalf("确认恢复应返回 *CorruptError，得到 %T: %v", err, err)
		}
	}

	// 升级与迁移也不能承接非法来源。
	target := noLimitRules()
	target.Version = "v2"
	if _, err := a.CheckUpgrade("slot", versions, target); err == nil {
		t.Fatalf("对非法记录的升级检查应失败")
	} else {
		var ce *CorruptError
		if !errors.As(err, &ce) {
			t.Fatalf("升级检查应返回 *CorruptError，得到 %T: %v", err, err)
		}
	}
	if _, err := a.Upgrade("slot", versions, target, badID); err == nil {
		t.Fatalf("对非法记录的升级应失败")
	}
	if _, err := a.PreviewMigration("slot", versions, target, nil, nil); err == nil {
		t.Fatalf("对非法记录的迁移预览应失败")
	}
	if _, err := a.Migrate("slot", versions, target, nil, nil, badID); err == nil {
		t.Fatalf("对非法记录的迁移应失败")
	}

	// 全部拒绝后槽当前记录与指针内嵌历史不变（查询不修补数据）。
	latest, err := a.readLatestLocked("slot")
	if err != nil {
		t.Fatalf("readLatestLocked: %v", err)
	}
	if latest != badID {
		t.Fatalf("槽当前记录不应改变，得到 %s", latest)
	}
	p, err := a.readSlotPointerLocked("slot")
	if err != nil {
		t.Fatalf("readSlotPointerLocked: %v", err)
	}
	if len(p.History) != 2 || p.History[0] != badID || p.History[1] != goodID {
		t.Fatalf("指针内嵌历史不应改变: %v", p.History)
	}
	// History 只展示通过完整性检查的记录：非法记录被略过，完好旧记录保留。
	history, err := a.History("slot")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 || history[0].ID != goodID {
		t.Fatalf("历史列表应只留完好记录: %+v", history)
	}
}

// 槽中只有违反携带上限的记录时，恢复返回 ErrUnrecoverable。
func TestRecoverUnrecoverableWhenOnlyIllegalRecord(t *testing.T) {
	a, dir := newTestArchive(t)
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: noLimitRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	st := w.Snapshot()
	st.Rules.CarryLimits = map[string]int{"hero": 0}
	env := &envelope{
		Format:    archiveFormatVersion,
		ID:        RecordID("r" + strings.Repeat("cd", 16)),
		SlotFirst: true,
		State:     st,
	}
	env.Checksum = computeChecksum(env)
	if err := writeRecord(dir, env); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}
	if err := a.createSlotPointer("slot", env.ID); err != nil {
		t.Fatalf("createSlotPointer: %v", err)
	}
	if _, err := a.RecoverLatest("slot", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("没有可用记录应返回 ErrUnrecoverable，得到 %v", err)
	}
	if _, err := a.PreviewRecovery("slot", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("没有可用记录时预览应返回 ErrUnrecoverable，得到 %v", err)
	}
}

// 没有上限、各单种数量合法但总量很大的旧存档仍可读取和分支。
func TestHugeTotalNoLimitRecordReadable(t *testing.T) {
	a, _ := newTestArchive(t)
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: noLimitRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: math.MaxInt},
				{Item: "key", Count: math.MaxInt},
			}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	info, err := a.Save("slot", w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	rec, err := a.Latest("slot", []string{"v1"})
	if err != nil {
		t.Fatalf("无上限大总量记录应可读取: %v", err)
	}
	if rec.State.Characters[0].Items[0].Count != math.MaxInt {
		t.Fatalf("读取不应修补数量: %+v", rec.State.Characters[0].Items)
	}
	if _, err := WorldFromState(rec.State); err != nil {
		t.Fatalf("无上限大总量状态应可继续: %v", err)
	}
	br, err := a.Branch("slot", info.ID, "dst")
	if err != nil {
		t.Fatalf("无上限大总量记录应可分支: %v", err)
	}
	brec, err := a.Latest("dst", []string{"v1"})
	if err != nil {
		t.Fatalf("分支记录应可读取: %v", err)
	}
	if brec.ID != br.ID || brec.State.Characters[0].Items[1].Count != math.MaxInt {
		t.Fatalf("分支应原样复制状态: %+v", brec.State.Characters[0].Items)
	}
}
