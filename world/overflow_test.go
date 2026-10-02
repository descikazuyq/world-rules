package world

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"os"
	"testing"
)

// int64ToInt 在 64 位平台上 int 即 int64，直接转换。
func int64ToInt(v int64) int { return int(v) }

// tamperRecordState 读出记录文件，用 mutate 修改状态后重算校验和并写回，
// 模拟“校验和正确但内容违规”的记录。
func tamperRecordState(t *testing.T, a *Archive, id RecordID, mutate func(*State)) {
	t.Helper()
	env, err := loadRecord(recordPath(a.dir, id))
	if err != nil {
		t.Fatalf("loadRecord: %v", err)
	}
	mutate(&env.State)
	env.Checksum = computeChecksum(env)
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(recordPath(a.dir, id), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// bigTotalString 返回若干 int 的精确十进制总和。
func bigTotalString(vals ...int) string {
	t := new(big.Int)
	for _, v := range vals {
		t.Add(t, big.NewInt(int64(v)))
	}
	return t.String()
}

// ---- Apply 合并增减量 ----

func TestApplyMergeChangesCancelToZero(t *testing.T) {
	// 原数量为 1，同批增加 MaxInt 和 MinInt 后结果为 0，应成功。
	w := baseWorld(t)
	st, err := w.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "gold", Delta: math.MaxInt},
			{Character: "hero", Item: "gold", Delta: math.MinInt},
		},
		Time: 1,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var got int
	for _, it := range st.Characters[0].Items {
		if it.Item == "gold" {
			got = it.Count
		}
	}
	if got != 0 {
		t.Fatalf("合并后数量应为 0，得到 %d", got)
	}
}

func TestApplyMergeChangesOrderIndependent(t *testing.T) {
	// 调整增减条目的次序不能改变成败与最终数量。
	orders := [][]ItemChange{
		{
			{Character: "hero", Item: "gold", Delta: math.MaxInt},
			{Character: "hero", Item: "gold", Delta: math.MinInt},
			{Character: "hero", Item: "gold", Delta: 5},
		},
		{
			{Character: "hero", Item: "gold", Delta: 5},
			{Character: "hero", Item: "gold", Delta: math.MinInt},
			{Character: "hero", Item: "gold", Delta: math.MaxInt},
		},
		{
			{Character: "hero", Item: "gold", Delta: math.MinInt},
			{Character: "hero", Item: "gold", Delta: 5},
			{Character: "hero", Item: "gold", Delta: math.MaxInt},
		},
	}
	want := 1 + math.MaxInt + math.MinInt + 5
	for i, changes := range orders {
		w := baseWorld(t)
		st, err := w.Apply(Commit{ItemChanges: changes, Time: 1})
		if err != nil {
			t.Fatalf("次序 %d: Apply: %v", i, err)
		}
		var got int
		for _, it := range st.Characters[0].Items {
			if it.Item == "gold" {
				got = it.Count
			}
		}
		if got != want {
			t.Fatalf("次序 %d: 最终数量应为 %d，得到 %d", i, want, got)
		}
	}
}

func TestApplyMergeFinalNegativeRejected(t *testing.T) {
	// 原数量为 1，同批增减后最终为负，应拒绝并回滚。
	w := baseWorld(t)
	before := w.Snapshot()
	_, err := w.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "gold", Delta: -3},
			{Character: "hero", Item: "gold", Delta: 1},
		},
		Time: 1,
	})
	if err == nil {
		t.Fatal("期望最终为负时提交失败")
	}
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("期望 *RuleError，得到 %T", err)
	}
	after := w.Snapshot()
	if !statesEqual(before, after) {
		t.Fatal("失败提交改变了世界状态")
	}
}

func TestApplyMergeFinalOverflowRejected(t *testing.T) {
	// 原数量为 1，同批增加两个 MaxInt，最终超出 int 范围，应拒绝。
	w := baseWorld(t)
	_, err := w.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "gold", Delta: math.MaxInt},
			{Character: "hero", Item: "gold", Delta: math.MaxInt},
		},
		Time: 1,
	})
	if err == nil {
		t.Fatal("期望最终超出 int 范围时提交失败")
	}
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("期望 *RuleError，得到 %T", err)
	}
}

func TestApplyMergeTotalOverflowWithLimitRejected(t *testing.T) {
	// 角色有携带上限 5，原数量 1，同批增减后最终数量合法但总量超上限，应拒绝。
	w := baseWorld(t)
	_, err := w.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "gold", Delta: math.MaxInt},
			{Character: "hero", Item: "gold", Delta: math.MinInt},
			{Character: "hero", Item: "gold", Delta: 10},
		},
		Time: 1,
	})
	if err == nil {
		t.Fatal("期望总量超上限时提交失败")
	}
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("期望 *RuleError，得到 %T", err)
	}
}

func TestApplyNoLimitLargeTotalAllowed(t *testing.T) {
	// 未设上限的角色，单种数量合法但总量超过 int 最大值，应允许提交。
	rules := baseRules()
	delete(rules.CarryLimits, "hero")
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: rules,
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
	// 再增加一个 MaxInt，总量进一步增大但仍无上限。
	st, err := w.Apply(Commit{
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 0}},
		Time:        1,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if st.Characters[0].Items[0].Count != math.MaxInt {
		t.Fatalf("数量应保持 MaxInt，得到 %d", st.Characters[0].Items[0].Count)
	}
}

func TestApplyCancelChangesStillRejectNonexistent(t *testing.T) {
	// 即使增减相互抵消，不存在的角色仍须拒绝。
	w := baseWorld(t)
	_, err := w.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "ghost", Item: "gold", Delta: 5},
			{Character: "ghost", Item: "gold", Delta: -5},
		},
		Time: 1,
	})
	if err == nil {
		t.Fatal("期望不存在的角色被拒绝")
	}
}

func TestApplyCancelChangesStillRejectDisallowedItem(t *testing.T) {
	// 即使增减相互抵消，不被规则允许的物品仍须拒绝。
	w := baseWorld(t)
	_, err := w.Apply(Commit{
		ItemChanges: []ItemChange{
			{Character: "hero", Item: "rock", Delta: 5},
			{Character: "hero", Item: "rock", Delta: -5},
		},
		Time: 1,
	})
	if err == nil {
		t.Fatal("期望不被允许的物品被拒绝")
	}
}

// ---- NewWorld / WorldFromState 真实总量 ----

func TestNewWorldNoLimitLargeTotal(t *testing.T) {
	// 未设上限的角色，总量超过 int 最大值，建立世界应成功。
	rules := baseRules()
	delete(rules.CarryLimits, "hero")
	_, err := NewWorld(InitialData{
		Seed:  1,
		Rules: rules,
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
}

func TestNewWorldLimitOverflowRejected(t *testing.T) {
	// 角色有携带上限，总量（真实值）超过上限，即使 int 加法回绕也应拒绝。
	rules := baseRules()
	rules.CarryLimits["hero"] = math.MaxInt
	_, err := NewWorld(InitialData{
		Seed:  1,
		Rules: rules,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: math.MaxInt},
				{Item: "key", Count: 1},
			}},
		},
	})
	if err == nil {
		t.Fatal("期望总量超上限时建立世界失败")
	}
}

func TestWorldFromStateNoLimitLargeTotal(t *testing.T) {
	// 从完整状态继续：未设上限、总量很大的旧存档应可读取。
	rules := baseRules()
	delete(rules.CarryLimits, "hero")
	st := State{
		Seed:  1,
		Rules: rules,
		Time:  5,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: math.MaxInt},
				{Item: "key", Count: math.MaxInt},
			}},
		},
	}
	w, err := WorldFromState(st)
	if err != nil {
		t.Fatalf("WorldFromState: %v", err)
	}
	if w.Snapshot().Time != 5 {
		t.Fatalf("时间片应为 5，得到 %d", w.Snapshot().Time)
	}
}

func TestWorldFromStateLimitViolationRejected(t *testing.T) {
	// 从完整状态继续：违反携带上限的状态应被拒绝。
	rules := baseRules()
	rules.CarryLimits["hero"] = 1
	st := State{
		Seed:  1,
		Rules: rules,
		Time:  5,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: 2},
			}},
		},
	}
	_, err := WorldFromState(st)
	if err == nil {
		t.Fatal("期望违反上限时 WorldFromState 失败")
	}
}

// ---- 升级阻碍的真实总量 ----

func TestCheckUpgradeLimitBlockerExactTotal(t *testing.T) {
	// 来源没有上限、总量超过 int 最大值；目标设置携带上限。
	// 阻碍须给出完整十进制真实总量。
	a, _ := newTestArchive(t)
	rules := baseRules()
	delete(rules.CarryLimits, "hero")
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: rules,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: math.MaxInt},
				{Item: "key", Count: math.MaxInt},
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
		ItemKinds:   []string{"gold", "key"},
		CarryLimits: map[string]int{"hero": 10},
	}
	check, err := a.CheckUpgrade("s", []string{"v1"}, target)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	if check.Compatible {
		t.Fatal("有上限阻碍时不应兼容")
	}
	var limitBlocker *Blocker
	for i := range check.Blockers {
		if check.Blockers[i].Kind == BlockerLimit {
			limitBlocker = &check.Blockers[i]
		}
	}
	if limitBlocker == nil {
		t.Fatalf("应包含上限阻碍，得到 %+v", check.Blockers)
	}
	wantExact := bigTotalString(math.MaxInt, math.MaxInt)
	if limitBlocker.TotalExact != wantExact {
		t.Fatalf("完整十进制总量应为 %s，得到 %q", wantExact, limitBlocker.TotalExact)
	}
	if limitBlocker.Total != 0 {
		t.Fatalf("总量超出 int 范围时 Total 应为 0，得到 %d", limitBlocker.Total)
	}
}

func TestCheckUpgradeLimitBlockerFitsInInt(t *testing.T) {
	// 总量能被 int 表示时，Total 即为真实总量，TotalExact 为空。
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	target := Rules{
		Version:     "v2",
		Locations:   []string{"hall"},
		ItemKinds:   []string{"gold", "key"},
		CarryLimits: map[string]int{"hero": 0},
	}
	check, err := a.CheckUpgrade("s", []string{"v1"}, target)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	var limitBlocker *Blocker
	for i := range check.Blockers {
		if check.Blockers[i].Kind == BlockerLimit {
			limitBlocker = &check.Blockers[i]
		}
	}
	if limitBlocker == nil {
		t.Fatalf("应包含上限阻碍，得到 %+v", check.Blockers)
	}
	if limitBlocker.Total != 1 {
		t.Fatalf("Total 应为真实总量 1，得到 %d", limitBlocker.Total)
	}
	if limitBlocker.TotalExact != "" {
		t.Fatalf("总量能被 int 表示时 TotalExact 应为空，得到 %q", limitBlocker.TotalExact)
	}
}

func TestUpgradeRejectedWhenLimitBlocker(t *testing.T) {
	// 有上限阻碍时正式升级不得保存。
	a, _ := newTestArchive(t)
	rules := baseRules()
	delete(rules.CarryLimits, "hero")
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: rules,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: math.MaxInt},
			}},
		},
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
		ItemKinds:   []string{"gold"},
		CarryLimits: map[string]int{"hero": 10},
	}
	_, err = a.Upgrade("s", []string{"v1"}, target, info.ID)
	if err == nil {
		t.Fatal("期望有阻碍时升级被拒绝")
	}
	// 槽当前记录与历史不变。
	latest, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.ID != info.ID {
		t.Fatalf("升级被拒绝后槽当前记录不应改变，得到 %s", latest.ID)
	}
}

// ---- 读档拒绝违规状态 ----

func TestLatestRejectsCarryLimitViolation(t *testing.T) {
	// 校验和正确也不能使违反携带上限的状态被接受：直接读取返回 CorruptError。
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	// 篡改记录：把 hero 的携带上限降到 0，使总量 1 超上限，重算校验和。
	tamperRecordState(t, a, info.ID, func(st *State) {
		st.Rules.CarryLimits["hero"] = 0
	})
	_, err = a.Latest("s", []string{"v1"})
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("期望 CorruptError，得到 %T: %v", err, err)
	}
}

func TestRecoverLatestSkipsIllegalRecord(t *testing.T) {
	// 恢复读取按本槽保存次序跳过违规记录，找到下一份合法记录。
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	infoA, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	// 用另一个世界覆盖，产生记录 B。
	w2, err := NewWorld(InitialData{
		Seed:  2,
		Rules: baseRules(),
		Characters: []Character{
			{ID: "hero", Location: "yard", Items: []CharacterItem{{Item: "key", Count: 2}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	infoB, err := a.Replace("s", w2, infoA.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 篡改最新记录 B：把 hero 上限降到 0，使总量 2 超上限。
	tamperRecordState(t, a, infoB.ID, func(st *State) {
		st.Rules.CarryLimits["hero"] = 0
	})
	// RecoverLatest 应跳过 B，返回 A。
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("RecoverLatest: %v", err)
	}
	if rec.ID != infoA.ID {
		t.Fatalf("应恢复到记录 A %s，得到 %s", infoA.ID, rec.ID)
	}
}

func TestPreviewRecoverySkipsIllegalRecord(t *testing.T) {
	// 恢复预览同样按保存次序跳过违规记录。
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	infoA, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	w2, err := NewWorld(InitialData{
		Seed:  2,
		Rules: baseRules(),
		Characters: []Character{
			{ID: "hero", Location: "yard", Items: []CharacterItem{{Item: "key", Count: 2}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	infoB, err := a.Replace("s", w2, infoA.ID)
	if err != nil {
		t.Fatal(err)
	}
	tamperRecordState(t, a, infoB.ID, func(st *State) {
		st.Rules.CarryLimits["hero"] = 0
	})
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("PreviewRecovery: %v", err)
	}
	if pv.Source != infoA.ID {
		t.Fatalf("预览来源应为 A %s，得到 %s", infoA.ID, pv.Source)
	}
	if pv.Current != infoB.ID {
		t.Fatalf("当前记录应为 B %s，得到 %s", infoB.ID, pv.Current)
	}
}

func TestRecoverLatestNoValidRecordReturnsUnrecoverable(t *testing.T) {
	// 所有记录都违规时返回 ErrUnrecoverable。
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	tamperRecordState(t, a, info.ID, func(st *State) {
		st.Rules.CarryLimits["hero"] = 0
	})
	_, err = a.RecoverLatest("s", []string{"v1"})
	if !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("期望 ErrUnrecoverable，得到 %T: %v", err, err)
	}
}

func TestBranchNoLimitLargeTotalReadable(t *testing.T) {
	// 没有上限、单种数量合法但总量很大的旧存档仍可读取和分支。
	a, _ := newTestArchive(t)
	rules := baseRules()
	delete(rules.CarryLimits, "hero")
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: rules,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "gold", Count: math.MaxInt},
				{Item: "key", Count: math.MaxInt},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	// 直接读取应成功。
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rec.ID != info.ID {
		t.Fatalf("记录标识不符")
	}
	// 分支应成功。
	branchInfo, err := a.Branch("s", info.ID, "s2")
	if err != nil {
		t.Fatalf("Branch: %v", err)
	}
	if branchInfo.Parent != info.ID {
		t.Fatalf("分支父记录应为 %s，得到 %s", info.ID, branchInfo.Parent)
	}
	// 分支槽也能读取。
	rec2, err := a.Latest("s2", []string{"v1"})
	if err != nil {
		t.Fatalf("分支 Latest: %v", err)
	}
	if rec2.State.Characters[0].Items[0].Count != math.MaxInt {
		t.Fatalf("分支应保留原数量，得到 %d", rec2.State.Characters[0].Items[0].Count)
	}
}

func TestConfirmRecoveryDoesNotCopyIllegalSource(t *testing.T) {
	// 确认恢复不能把违规来源复制成新记录。
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	infoA, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	w2, err := NewWorld(InitialData{
		Seed:  2,
		Rules: baseRules(),
		Characters: []Character{
			{ID: "hero", Location: "yard", Items: []CharacterItem{{Item: "key", Count: 2}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	infoB, err := a.Replace("s", w2, infoA.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 篡改 B 使其违规。
	tamperRecordState(t, a, infoB.ID, func(st *State) {
		st.Rules.CarryLimits["hero"] = 0
	})
	// 预览应选中 A（合法记录）。
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("PreviewRecovery: %v", err)
	}
	if pv.Source != infoA.ID {
		t.Fatalf("预览来源应为 A，得到 %s", pv.Source)
	}
	// 确认恢复应成功，新记录以 A 为父。
	info, err := a.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("ConfirmRecovery: %v", err)
	}
	if info.Parent != infoA.ID {
		t.Fatalf("新记录父记录应为 A %s，得到 %s", infoA.ID, info.Parent)
	}
}
