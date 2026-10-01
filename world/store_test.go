package world

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain 支持以子进程方式运行多进程并发测试。
func TestMain(m *testing.M) {
	switch os.Getenv("WORLD_TEST_HELPER") {
	case "":
		os.Exit(m.Run())
	case "concurrent_replace":
		helperConcurrentReplace()
		os.Exit(0)
	default:
		fmt.Fprintln(os.Stderr, "unknown helper")
		os.Exit(2)
	}
}

func newTestArchive(t *testing.T) (*Archive, string) {
	t.Helper()
	dir := t.TempDir()
	a, err := Create(dir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return a, dir
}

func TestSaveDuplicateSlot(t *testing.T) {
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	if _, err := a.Save("slot1", w); err != nil {
		t.Fatal(err)
	}
	_, err := a.Save("slot1", w)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("重名应返回 ConflictError，得到 %T: %v", err, err)
	}
}

func TestReplaceRequiresExpectedAndCAS(t *testing.T) {
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}

	// 不带记录标识拒绝。
	if _, err := a.Replace("s", w, ""); err == nil {
		t.Fatal("空记录标识应当拒绝")
	}
	// 胡乱的记录标识冲突。
	if _, err := a.Replace("s", w, "rdeadbeef"); err == nil {
		t.Fatal("不存在的记录标识应当冲突")
	}

	// 正常覆盖：父记录是被覆盖的记录。
	if _, err := w.Apply(Commit{Time: 1}); err != nil {
		t.Fatal(err)
	}
	info2, err := a.Replace("s", w, info.ID)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if info2.Parent != info.ID {
		t.Fatalf("新记录父标识应为 %s，得到 %s", info.ID, info2.Parent)
	}
	if info2.ID == info.ID {
		t.Fatal("每次保存应产生新的唯一标识")
	}

	// 再次用旧标识覆盖 -> 冲突。
	if _, err := a.Replace("s", w, info.ID); err == nil {
		t.Fatal("用过时标识覆盖应冲突")
	} else {
		var ce *ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("应返回 ConflictError，得到 %T", err)
		}
	}
}

func TestOverwrittenHistoryStillReadable(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := w.Apply(Commit{Time: i}); err != nil {
			t.Fatal(err)
		}
		latest, err := a.Latest("s", []string{"v1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Replace("s", w, latest.ID); err != nil {
			t.Fatal(err)
		}
	}

	// 重新打开目录后，最早的记录仍可按标识读取，父关系仍在。
	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a2.Record("s", r0.ID, []string{"v1"})
	if err != nil {
		t.Fatalf("读取覆盖前的历史记录: %v", err)
	}
	if rec.State.Time != 0 || !rec.SlotFirst || rec.Parent != "" {
		t.Fatalf("首条记录关系错误: %+v", rec)
	}
	if rec.State.Seed != 42 {
		t.Fatal("历史记录应完整保存种子")
	}
	hist, err := a2.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 4 {
		t.Fatalf("历史应有 4 条，得到 %d", len(hist))
	}
	if hist[len(hist)-1].ID != r0.ID {
		t.Fatal("历史链末端应为首条记录")
	}
}

func TestBranchIndependence(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, _ := a.Save("s", w)
	latest, _ := a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Moves: []Move{{Character: "hero", To: "yard"}}, Time: 1})
	r1, _ := a.Replace("s", w, latest.ID)

	// 从最早的 r0 分出 b。
	b0, err := a.Branch("s", r0.ID, "b")
	if err != nil {
		t.Fatalf("Branch: %v", err)
	}
	if b0.Parent != r0.ID || !b0.SlotFirst {
		t.Fatalf("分支首记录应以源记录为父: %+v", b0)
	}
	// 目标槽重名拒绝。
	if _, err := a.Branch("s", r0.ID, "b"); err == nil {
		t.Fatal("重名目标槽应拒绝")
	}
	// 不能拿别的槽的记录冒充分支源。
	if _, err := a.Branch("s", b0.ID, "c"); err == nil {
		t.Fatal("不属于源槽历史的记录应拒绝分支")
	}

	// b 复制的是 r0 时刻的状态。
	rec, err := a.Latest("b", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.State.Time != 0 || rec.State.Characters[0].Location != "hall" {
		t.Fatalf("分支未复制源时刻状态: %+v", rec.State)
	}

	// 两槽此后互不影响：在 b 上推进到 9，s 保持自己的链。
	wb, err := NewWorld(InitialData{
		Seed:       rec.State.Seed,
		Rules:      rec.State.Rules,
		Characters: rec.State.Characters,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = wb.Apply(Commit{Time: 9})
	if _, err := a.Replace("b", wb, b0.ID); err != nil {
		t.Fatal(err)
	}

	a2, _ := Open(dir)
	sLatest, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	bLatest, err := a2.Latest("b", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if sLatest.ID != r1.ID || sLatest.State.Time != 1 {
		t.Fatalf("源槽被分支影响: %+v", sLatest)
	}
	if bLatest.State.Time != 9 {
		t.Fatalf("分支槽状态错误: %+v", bLatest.State)
	}
}

func TestReadVersionAndCorruptionRecovery(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, _ := a.Save("s", w)
	cur, _ := a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 1})
	r1, _ := a.Replace("s", w, cur.ID)

	// 版本集合不含任何匹配版本 -> 明确拒绝，且不改写。
	if _, err := a.Latest("s", []string{"v9"}); err == nil {
		t.Fatal("版本不被接受应返回错误")
	} else {
		var vre *VersionRejectedError
		if !errors.As(err, &vre) {
			t.Fatalf("应返回 VersionRejectedError，得到 %T", err)
		}
	}

	// 直接写一条 v2 的最新记录（读取侧永远不能替换记录中的规则，
	// 这里模拟由其他兼容版本写入器产生的数据）。
	v2State := w.Snapshot()
	v2State.Time = 2
	v2State.Rules.Version = "v2"
	r2 := writeRawRecord(t, a, r1.ID, false, v2State)
	updatePointer(t, a, "s", r2)

	// Latest 在仅接受 v1 时拒绝最新记录；恢复读取应越过 v2 找到 r1。
	if _, err := a.Latest("s", []string{"v1"}); err == nil {
		t.Fatal("v2 最新记录应被版本拒绝")
	}
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应恢复到 v1 历史记录: %v", err)
	}
	if rec.ID != r1.ID {
		t.Fatalf("应恢复到最近可接受记录 %s，得到 %s", r1.ID, rec.ID)
	}
	// 恢复读取不改写槽指针与历史。
	ptr, err := a.readLatestLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	if ptr != r2 {
		t.Fatal("恢复读取改写了槽指针")
	}

	// 损坏最新记录：改写其校验和（JSON 仍可解析，父指针仍在）。
	// Latest 报损坏；恢复应越过它，沿父指针找到可接受记录。
	{
		path := recordPath(dir, r2)
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
	}
	if _, err := a.Latest("s", []string{"v1", "v2"}); err == nil {
		t.Fatal("损坏记录应报错")
	} else {
		var ce *CorruptError
		if !errors.As(err, &ce) {
			t.Fatalf("应返回 CorruptError，得到 %T", err)
		}
	}
	rec2, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应越过损坏记录恢复: %v", err)
	}
	if rec2.ID != r1.ID {
		t.Fatalf("恢复结果错误: %s", rec2.ID)
	}

	// 连最后可接受的根也不可接受时 -> 不可恢复。
	_, err = a.RecoverLatest("s", []string{"v9"})
	if !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("应返回 ErrUnrecoverable，得到 %T: %v", err, err)
	}

	// r0 仍完好且可按标识读取（在进一步截断链之前）。
	r0rec, err := a.Record("s", r0.ID, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if r0rec.ID != r0.ID {
		t.Fatal("根记录读取错误")
	}

	// 最新记录被截断到无法解析时，仍应越过它找到本槽更早的可用记录，
	// 不能因为读不出父指针就提前放弃。
	if err := os.Truncate(recordPath(dir, r2), 40); err != nil {
		t.Fatal(err)
	}
	rec3, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("最新记录损坏后应能恢复到更早的可用记录: %v", err)
	}
	if rec3.ID != r1.ID {
		t.Fatalf("截断最新记录后应恢复到 r1，得到 %s", rec3.ID)
	}
}

// TestRecoverLatestDeleteLatestRecord：最新记录文件被删除后，恢复应
// 找到本槽更早的可用记录。
func TestRecoverLatestDeleteLatestRecord(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, _ := a.Save("s", w)
	cur, _ := a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 1})
	r1, _ := a.Replace("s", w, cur.ID)
	cur, _ = a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 2})
	r2, _ := a.Replace("s", w, cur.ID)

	// 删除最新记录文件（槽指针仍指向它）。
	if err := os.Remove(recordPath(dir, r2.ID)); err != nil {
		t.Fatal(err)
	}
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("删除最新记录后应恢复到 r1: %v", err)
	}
	if rec.ID != r1.ID {
		t.Fatalf("应恢复到 r1，得到 %s", rec.ID)
	}
	// 重新打开存档后恢复结果一致。
	a2, _ := Open(dir)
	rec2, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("重新打开后应恢复到 r1: %v", err)
	}
	if rec2.ID != r1.ID {
		t.Fatalf("重新打开后应恢复到 r1，得到 %s", rec2.ID)
	}
	_ = r0
}

// TestRecoverLatestMultipleDamaged：最新记录与若干中间记录一起损坏后，
// 恢复应找到最近一份完好且版本可接受的记录。
func TestRecoverLatestMultipleDamaged(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, _ := a.Save("s", w)
	cur, _ := a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 1})
	r1, _ := a.Replace("s", w, cur.ID)
	cur, _ = a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 2})
	r2, _ := a.Replace("s", w, cur.ID)
	cur, _ = a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 3})
	r3, _ := a.Replace("s", w, cur.ID)

	// 损坏 r3（最新）和 r1（中间），保留 r2 和 r0。
	tamperRecordChecksum(t, dir, r3.ID)
	tamperRecordChecksum(t, dir, r1.ID)

	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("多条记录损坏后应恢复: %v", err)
	}
	if rec.ID != r2.ID {
		t.Fatalf("应恢复到最近的完好记录 r2，得到 %s", rec.ID)
	}
	// r2 的内容完整保持该次保存的状态。
	if rec.State.Time != 2 {
		t.Fatalf("恢复记录的时间片应为 2，得到 %d", rec.State.Time)
	}
	_ = r0
}

// TestRecoverLatestTamperedParent：损坏记录的父标识被改成其他值时，
// 恢复不能被引向别的槽，也不能遮住本槽更早的可用记录。
func TestRecoverLatestTamperedParent(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, _ := a.Save("s", w)
	cur, _ := a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 1})
	r1, _ := a.Replace("s", w, cur.ID)
	cur, _ = a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 2})
	r2, _ := a.Replace("s", w, cur.ID)

	// 先损坏 r2（篡改校验和），再把其父标识改成 r0。
	tamperRecordChecksum(t, dir, r2.ID)
	path := recordPath(dir, r2.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), `"parent": "`+string(r1.ID)+`"`,
		`"parent": "`+string(r0.ID)+`"`, 1)
	if tampered == string(raw) {
		t.Fatal("测试前提：未找到父指针字段")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}

	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("父标识被篡改后应恢复: %v", err)
	}
	if rec.ID != r1.ID {
		t.Fatalf("应恢复到 r1（不被引向 r0），得到 %s", rec.ID)
	}
}

// TestRecoverLatestTamperedSlotFirst：损坏记录的槽首标记被改成 true 时，
// 恢复不能在该记录处提前停止，应继续找到本槽更早的可用记录。
func TestRecoverLatestTamperedSlotFirst(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	_, _ = a.Save("s", w)
	cur, _ := a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 1})
	r1, _ := a.Replace("s", w, cur.ID)
	cur, _ = a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 2})
	r2, _ := a.Replace("s", w, cur.ID)

	// 损坏 r2 并把槽首标记改成 true。
	tamperRecordChecksum(t, dir, r2.ID)
	path := recordPath(dir, r2.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), `"slotFirst": false`,
		`"slotFirst": true`, 1)
	if tampered == string(raw) {
		t.Fatal("测试前提：未找到 slotFirst 字段")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}

	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("槽首标记被篡改后应恢复: %v", err)
	}
	if rec.ID != r1.ID {
		t.Fatalf("应恢复到 r1（不被提前停止），得到 %s", rec.ID)
	}
}

// TestRecoverLatestBranchBoundary：分支只查找分支自身已保存的记录，
// 即使分支首条记录损坏也不越过分支边界；分支全无可用记录时返回
// ErrUnrecoverable，其他槽不能替代。
func TestRecoverLatestBranchBoundary(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, _ := a.Save("s", w)
	cur, _ := a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 1})
	_, _ = a.Replace("s", w, cur.ID)

	// 从 r0 分出分支 b。
	b0, err := a.Branch("s", r0.ID, "b")
	if err != nil {
		t.Fatal(err)
	}
	curB, _ := a.Latest("b", []string{"v1"})
	wb, _ := WorldFromState(curB.State)
	_, _ = wb.Apply(Commit{Time: 5})
	b1, _ := a.Replace("b", wb, curB.ID)

	// 损坏分支首条记录 b0。
	tamperRecordChecksum(t, dir, b0.ID)

	// 恢复分支应找到 b1，不跨入来源槽 s。
	rec, err := a.RecoverLatest("b", []string{"v1"})
	if err != nil {
		t.Fatalf("分支首条记录损坏后应恢复到 b1: %v", err)
	}
	if rec.ID != b1.ID {
		t.Fatalf("应恢复到分支自身的 b1，得到 %s", rec.ID)
	}
	if rec.State.Time != 5 {
		t.Fatalf("分支记录时间片应为 5，得到 %d", rec.State.Time)
	}

	// 损坏分支全部记录。
	tamperRecordChecksum(t, dir, b1.ID)
	_, err = a.RecoverLatest("b", []string{"v1"})
	if !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("分支全无可用记录应返回 ErrUnrecoverable，得到 %T: %v", err, err)
	}
	// 来源槽 s 即使种子和规则相同也不能替代。
	_, err = a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("来源槽 s 应可正常恢复: %v", err)
	}
}

// TestRecoverLatestOldArchiveAdoption：旧存档（指针未携带历史）在第一次
// 成功覆盖后，原有历史与新增记录都获得恢复能力。
func TestRecoverLatestOldArchiveAdoption(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	_, _ = a.Save("s", w)
	cur, _ := a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 1})
	r1, _ := a.Replace("s", w, cur.ID)

	// 模拟旧存档：清除指针内嵌的历史。
	clearPointerHistory(t, a, "s")

	// 第一次成功覆盖：收养原有历史并写入新记录。
	cur, _ = a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 2})
	r2, err := a.Replace("s", w, cur.ID)
	if err != nil {
		t.Fatal(err)
	}

	// 损坏最新记录 r2，恢复应找到 r1。
	tamperRecordChecksum(t, dir, r2.ID)
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("旧存档收养后应能恢复: %v", err)
	}
	if rec.ID != r1.ID {
		t.Fatalf("应恢复到 r1，得到 %s", rec.ID)
	}

	// 重新打开存档后恢复结果一致。
	a2, _ := Open(dir)
	rec2, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("重新打开后应能恢复: %v", err)
	}
	if rec2.ID != r1.ID {
		t.Fatalf("重新打开后应恢复到 r1，得到 %s", rec2.ID)
	}
}

// TestRecoverLatestOrphanNotSelected：只有记录文件写完、尚未完成保存的
// 残留文件不能被选中。
func TestRecoverLatestOrphanNotSelected(t *testing.T) {
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	r0, _ := a.Save("s", w)

	// 写入一条记录文件但不更新槽指针（模拟崩溃残留）。
	_, _ = w.Apply(Commit{Time: 1})
	orphan := writeRawRecord(t, a, r0.ID, false, w.Snapshot())

	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应恢复到 r0: %v", err)
	}
	if rec.ID != r0.ID {
		t.Fatalf("残留记录不应被选中，应恢复到 r0，得到 %s", rec.ID)
	}
	_ = orphan
}

// TestRecoverLatestVersionRejectedSkipped：版本不被接受的记录继续跳过，
// 不自动升级。
func TestRecoverLatestVersionRejectedSkipped(t *testing.T) {
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	_, _ = a.Save("s", w)
	cur, _ := a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 1})
	r1, _ := a.Replace("s", w, cur.ID)

	// 写入一条 v2 的最新记录。
	v2State := w.Snapshot()
	v2State.Time = 2
	v2State.Rules.Version = "v2"
	r2 := writeRawRecord(t, a, r1.ID, false, v2State)
	updatePointer(t, a, "s", r2)

	// 只接受 v1 时应跳过 v2 找到 r1。
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应恢复到 v1 记录: %v", err)
	}
	if rec.ID != r1.ID {
		t.Fatalf("应恢复到 r1，得到 %s", rec.ID)
	}
	if rec.State.Rules.Version != "v1" {
		t.Fatal("恢复不应替换记录中的规则版本")
	}
}

// TestRecoverLatestUpgradeRecord：规则升级产生的记录损坏后，恢复应
// 找到升级前最近一份版本可接受的记录。
func TestRecoverLatestUpgradeRecord(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	info, _ := a.Save("s", w)
	if _, err := a.Upgrade("s", []string{"v1"}, v2Rules(), info.ID); err != nil {
		t.Fatal(err)
	}

	// 损坏升级后的最新记录。
	latest, _ := a.Latest("s", []string{"v1", "v2"})
	tamperRecordChecksum(t, dir, latest.ID)

	// 只接受 v1 时应恢复到升级前的记录。
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("升级记录损坏后应恢复到升级前记录: %v", err)
	}
	if rec.ID != info.ID {
		t.Fatalf("应恢复到升级前记录 %s，得到 %s", info.ID, rec.ID)
	}
	// 恢复记录的种子、规则、角色、物品及时间片完整保持该次保存的内容。
	if rec.State.Seed != 42 || rec.State.Rules.Version != "v1" {
		t.Fatalf("恢复记录内容不完整: %+v", rec.State)
	}
}

func TestChecksumCoversParent(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, _ := a.Save("s", w)
	cur, _ := a.Latest("s", []string{"v1"})
	_, _ = w.Apply(Commit{Time: 1})
	r1, _ := a.Replace("s", w, cur.ID)

	// 篡改 r1 的父指针而不动状态：校验和必须失败。
	path := recordPath(dir, r1.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), `"parent": "`+string(r0.ID)+`"`,
		`"parent": "`+string("r00000000000000000000000000000000")+`"`, 1)
	if tampered == string(raw) {
		t.Fatal("测试前提：未找到父指针字段")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Record("s", r1.ID, []string{"v1"}); err == nil {
		t.Fatal("篡改父指针应导致校验失败")
	} else {
		var ce *CorruptError
		if !errors.As(err, &ce) {
			t.Fatalf("应返回 CorruptError，得到 %T", err)
		}
	}
}

func TestInterruptedWriteLeavesOldRecord(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, _ := a.Save("s", w)

	// 模拟崩溃现场：
	//  1. records/ 下残留一个未完成的 .tmp 文件；
	//  2. 一条新记录已写完，但槽指针还没更新（指针原子改名前崩溃）。
	junk := filepath.Join(dir, recordsName, ".tmp-whatever")
	if err := os.WriteFile(junk, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = w.Apply(Commit{Time: 1})
	orphan := writeRawRecord(t, a, r0.ID, false, w.Snapshot())

	a2, err := Open(dir)
	if err != nil {
		t.Fatalf("崩溃后应能重新打开: %v", err)
	}
	rec, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应读到旧的完整记录: %v", err)
	}
	if rec.ID != r0.ID {
		t.Fatalf("槽指针不应指向未提交的记录 %s", orphan)
	}
}

func TestUnknownSlotAndRecord(t *testing.T) {
	a, _ := newTestArchive(t)
	if _, err := a.Latest("nope", []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("未知槽应 NotFound，得到 %T", err)
	}
	w := baseWorld(t)
	r0, _ := a.Save("s", w)
	if _, err := a.Record("s", r0.ID, []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Record("s", "r00000000000000000000000000000000", []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("未知记录应 NotFound")
	}
}

func TestRejectUnsafeSlotNames(t *testing.T) {
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "/abs", "../escape"} {
		if _, err := a.Save(bad, w); err == nil {
			t.Fatalf("槽名 %q 应被拒绝", bad)
		}
		if _, err := a.Latest(bad, []string{"v1"}); err == nil {
			t.Fatalf("读取槽名 %q 应被拒绝", bad)
		}
	}
}

func TestReloadWorldPreservesTime(t *testing.T) {
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	r0, _ := a.Save("s", w)
	_, _ = w.Apply(Commit{
		Moves:       []Move{{Character: "hero", To: "yard"}},
		ItemChanges: []ItemChange{{Character: "hero", Item: "gold", Delta: 1}},
		Time:        7,
	})
	r1, _ := a.Replace("s", w, r0.ID)

	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	// 读档续玩：时间片与其它状态一起恢复，而不是重置为 0。
	loaded, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.Snapshot()
	if got.Time != 7 || got.Characters[0].Location != "yard" {
		t.Fatalf("读档未保留完整状态: %+v", got)
	}
	// 继续推进只能在已恢复的时间片上向前。
	if _, err := loaded.Apply(Commit{Time: 6}); err == nil {
		t.Fatal("读档后时间倒退应当失败")
	}
	next, err := loaded.Apply(Commit{Time: 8})
	if err != nil {
		t.Fatalf("读档后继续推进失败: %v", err)
	}
	if next.Time != 8 {
		t.Fatalf("推进后时间片错误: %d", next.Time)
	}
	if r1.ID == "" {
		t.Fatal("记录标识不应为空")
	}
}

// writeRawRecord 直接写入一条记录文件并返回其标识，供损坏/版本测试使用。
func writeRawRecord(t *testing.T, a *Archive, parent RecordID, first bool, st State) RecordID {
	t.Helper()
	id, err := newRecordID()
	if err != nil {
		t.Fatal(err)
	}
	env := &envelope{
		Format:    archiveFormatVersion,
		ID:        id,
		Parent:    parent,
		SlotFirst: first,
		State:     st,
	}
	env.Checksum = computeChecksum(env)
	if err := writeRecord(a.dir, env); err != nil {
		t.Fatal(err)
	}
	return id
}

func updatePointer(t *testing.T, a *Archive, slot string, id RecordID) {
	t.Helper()
	p, err := a.readPointerLocked(slot)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.replaceSlotPointer(slot, id, p.History); err != nil {
		t.Fatal(err)
	}
}

// tamperRecordChecksum 篡改一条记录的校验和，使其内容损坏但仍可解析。
func tamperRecordChecksum(t *testing.T, dir string, id RecordID) {
	t.Helper()
	path := recordPath(dir, id)
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
}

// clearPointerHistory 清除槽指针内嵌的历史，模拟旧存档（指针未携带历史）。
func clearPointerHistory(t *testing.T, a *Archive, slot string) {
	t.Helper()
	p, err := a.readPointerLocked(slot)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.replaceSlotPointer(slot, p.Latest, nil); err != nil {
		t.Fatal(err)
	}
}
