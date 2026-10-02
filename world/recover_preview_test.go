package world

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestPreviewRecoverBasic 预览返回槽当前指向的记录标识、选中的来源记录
// 标识及其完整世界状态，且不改变存档。
func TestPreviewRecoverBasic(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0

	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatalf("PreviewRecover: %v", err)
	}
	// 当前记录完好且版本可接受时，预览可以选它。
	if preview.Current != ids[0] {
		t.Fatalf("当前记录标识应为 %s，得到 %s", ids[0], preview.Current)
	}
	if preview.Source != ids[0] {
		t.Fatalf("来源记录标识应为 %s，得到 %s", ids[0], preview.Source)
	}
	if preview.State.Time != 2 || preview.State.Seed != 42 {
		t.Fatalf("预览状态与该次保存不符: %+v", preview.State)
	}
	if preview.State.Rules.Version != "v1" {
		t.Fatalf("规则版本错误: %s", preview.State.Rules.Version)
	}

	// 预览不改变存档：指针与历史保持原样。
	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != ids[0] {
		t.Fatal("预览改变了槽当前记录")
	}
	hist, err := a2.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 {
		t.Fatalf("预览改变了历史长度: %d", len(hist))
	}
}

// TestPreviewRecoverSkipsCorrupt 最新记录损坏时，预览越过它选中最近一份
// 校验通过且版本可接受的更早记录；当前记录标识仍是损坏的最新记录。
func TestPreviewRecoverSkipsCorrupt(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0
	corruptChecksum(t, dir, ids[0])
	truncateRecord(t, dir, ids[1])

	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应越过损坏记录预览: %v", err)
	}
	if preview.Current != ids[0] {
		t.Fatalf("当前记录标识应仍是损坏的 %s，得到 %s", ids[0], preview.Current)
	}
	if preview.Source != ids[2] {
		t.Fatalf("应选中最近可用记录 %s，得到 %s", ids[2], preview.Source)
	}
	if preview.State.Time != 1 {
		t.Fatalf("来源状态时间片应为 1，得到 %d", preview.State.Time)
	}
}

// TestPreviewRecoverNoAcceptableVersion 所有完好记录版本都不被接受时，
// 返回 ErrUnrecoverable。
func TestPreviewRecoverNoAcceptableVersion(t *testing.T) {
	a, _ := newTestArchive(t)
	saveN(t, a, "s", 2)
	if _, err := a.PreviewRecover("s", []string{"v9"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("无版本可接受应 ErrUnrecoverable，得到 %T: %v", err, err)
	}
}

// TestPreviewRecoverMissingAndCorruptPointer 槽不存在报不存在；槽指针
// 不可读报损坏。
func TestPreviewRecoverMissingAndCorruptPointer(t *testing.T) {
	a, dir := newTestArchive(t)
	if _, err := a.PreviewRecover("nope", []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("槽不存在应 NotFound，得到 %T: %v", err, err)
	}
	w := baseWorld(t)
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, slotsName, "s.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PreviewRecover("s", []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("槽指针不可读应 Corrupt，得到 %T: %v", err, err)
	}
}

// TestPreviewRecoverBranchBoundary 分支只在分支自身历史中查找来源。
func TestPreviewRecoverBranchBoundary(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	b0, err := a.Branch("s", ids[2], "b")
	if err != nil {
		t.Fatal(err)
	}
	// 分支首条记录损坏：预览应报不可恢复，而不是拿来源槽记录替代。
	corruptChecksum(t, dir, b0.ID)
	if _, err := a.PreviewRecover("b", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("分支无可用记录应 ErrUnrecoverable，得到 %T: %v", err, err)
	}
}

// TestPreviewRecoverDoesNotMutate 预览不修改目录内容。
func TestPreviewRecoverDoesNotMutate(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3)
	corruptChecksum(t, dir, ids[0])
	truncateRecord(t, dir, ids[1])

	snapshot := func() (string, map[string]int64) {
		ptr, err := os.ReadFile(a.slotPath("s"))
		if err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(filepath.Join(dir, recordsName))
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]int64{}
		for _, e := range entries {
			fi, err := e.Info()
			if err != nil {
				t.Fatal(err)
			}
			m[e.Name()] = fi.Size()
		}
		return string(ptr), m
	}

	ptrBefore, filesBefore := snapshot()
	if _, err := a.PreviewRecover("s", []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PreviewRecover("s", []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	ptrAfter, filesAfter := snapshot()
	if ptrBefore != ptrAfter {
		t.Fatal("预览改写了槽指针")
	}
	if len(filesBefore) != len(filesAfter) {
		t.Fatalf("预览改变了 records 目录条目数: %d -> %d", len(filesBefore), len(filesAfter))
	}
	for name, size := range filesBefore {
		if filesAfter[name] != size {
			t.Fatalf("预览改动了文件 %s: %d -> %d", name, size, filesAfter[name])
		}
	}
}

// TestConfirmRecoverBasic 确认恢复成功：槽指向新记录，父记录是选中的
// 来源，状态原样复制，旧历史按原次序保留、新记录排在最前。
func TestConfirmRecoverBasic(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0
	// 最新记录损坏，预览选中 r1。
	corruptChecksum(t, dir, ids[0])

	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := a.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("ConfirmRecover: %v", err)
	}
	if info.ID == "" || info.ID == preview.Current || info.ID == preview.Source {
		t.Fatal("确认应生成独立的新记录标识")
	}
	if info.Parent != preview.Source {
		t.Fatalf("新记录父应为来源 %s，得到 %s", preview.Source, info.Parent)
	}
	if info.Version != "v1" {
		t.Fatalf("新记录版本应为 v1，得到 %s", info.Version)
	}

	// 重新打开：新记录完整可读，状态与来源一致。
	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != info.ID {
		t.Fatal("槽指针应指向确认后的新记录")
	}
	if rec.Parent != preview.Source {
		t.Fatalf("新记录父应为来源 %s，得到 %s", preview.Source, rec.Parent)
	}
	if rec.State.Time != preview.State.Time || rec.State.Seed != preview.State.Seed {
		t.Fatalf("新记录状态应与来源一致: %+v vs %+v", rec.State, preview.State)
	}
	if rec.State.Rules.Version != "v1" {
		t.Fatalf("新记录规则版本错误: %s", rec.State.Rules.Version)
	}

	// 旧历史按原保存次序保留，新记录排在最前；来源之后仍完好的旧记录
	// 可照常读取。
	hist, err := a2.History("s")
	if err != nil {
		t.Fatal(err)
	}
	want := []RecordID{info.ID, ids[0], ids[1], ids[2], ids[3]}
	if len(hist) != len(want) {
		t.Fatalf("历史长度错误: %+v", hist)
	}
	for i := range want {
		if hist[i].ID != want[i] {
			t.Fatalf("历史次序错误: %+v", hist)
		}
	}
	// 来源记录仍可按标识读取。
	srcRec, err := a2.Record("s", preview.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("来源记录应仍可读取: %v", err)
	}
	if srcRec.ID != preview.Source {
		t.Fatal("来源记录标识错误")
	}
}

// TestConfirmRecoverCurrentIntact 即使当前记录完好且版本可接受，确认仍
// 生成独立的新记录（父为当前记录本身）。
func TestConfirmRecoverCurrentIntact(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1) // r1 r0
	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Source != ids[0] {
		t.Fatalf("当前记录完好时应选中当前记录，得到 %s", preview.Source)
	}
	info, err := a.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Parent != ids[0] {
		t.Fatalf("新记录父应为当前记录 %s，得到 %s", ids[0], info.Parent)
	}
	if info.ID == ids[0] {
		t.Fatal("确认应生成独立的新记录")
	}
}

// TestConfirmRecoverEmptyIDs 任一标识为空都返回现有冲突错误。
func TestConfirmRecoverEmptyIDs(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1)
	if _, err := a.ConfirmRecover("s", "", ids[0], []string{"v1"}); !isConflict(err) {
		t.Fatalf("当前标识为空应 Conflict，得到 %T: %v", err, err)
	}
	if _, err := a.ConfirmRecover("s", ids[0], "", []string{"v1"}); !isConflict(err) {
		t.Fatalf("来源标识为空应 Conflict，得到 %T: %v", err, err)
	}
}

// TestConfirmRecoverStaleCurrent 槽已经指向其他记录时返回冲突。
func TestConfirmRecoverStaleCurrent(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	// 用过时的 r1 作为当前记录确认。
	if _, err := a.ConfirmRecover("s", ids[1], ids[2], []string{"v1"}); !isConflict(err) {
		t.Fatalf("槽已指向其他记录应 Conflict，得到 %T: %v", err, err)
	}
}

// TestConfirmRecoverCurrentCorruptDoesNotBlock 槽当前记录已损坏或被删除，
// 也不能妨碍从本槽完好旧记录恢复：CAS 比较针对槽所指记录本身。
func TestConfirmRecoverCurrentCorruptDoesNotBlock(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	corruptChecksum(t, dir, ids[0])

	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Current != ids[0] || preview.Source != ids[1] {
		t.Fatalf("预览结果错误: current=%s source=%s", preview.Current, preview.Source)
	}
	// 当前记录损坏，确认仍应成功。
	info, err := a.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("当前记录损坏不应妨碍恢复: %v", err)
	}
	if info.Parent != ids[1] {
		t.Fatalf("新记录父应为 %s，得到 %s", ids[1], info.Parent)
	}

	// 当前记录被删除也一样。
	a2, dir2 := newTestArchive(t)
	ids2 := saveN(t, a2, "s", 2)
	deleteRecord(t, dir2, ids2[0])
	preview2, err := a2.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if preview2.Current != ids2[0] || preview2.Source != ids2[1] {
		t.Fatalf("预览结果错误: current=%s source=%s", preview2.Current, preview2.Source)
	}
	if _, err := a2.ConfirmRecover("s", preview2.Current, preview2.Source, []string{"v1"}); err != nil {
		t.Fatalf("当前记录被删除不应妨碍恢复: %v", err)
	}
}

// TestConfirmRecoverSourceDeleted 来源后来被删除，返回已有的不存在错误，
// 不改选其他来源。
func TestConfirmRecoverSourceDeleted(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	corruptChecksum(t, dir, ids[0])
	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	deleteRecord(t, dir, preview.Source)
	if _, err := a.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("来源被删除应 NotFound，得到 %T: %v", err, err)
	}
	// 槽指针未变。
	if latest, _ := a.readLatestLocked("s"); latest != ids[0] {
		t.Fatal("被拒绝的确认不应改变槽指针")
	}
}

// TestConfirmRecoverSourceCorrupted 来源后来损坏，返回已有的损坏错误，
// 不改选其他来源。
func TestConfirmRecoverSourceCorrupted(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	corruptChecksum(t, dir, ids[0])
	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	corruptChecksum(t, dir, preview.Source)
	if _, err := a.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("来源损坏应 Corrupt，得到 %T: %v", err, err)
	}
}

// TestConfirmRecoverSourceVersionRejected 来源版本不被接受时返回已有的
// 版本拒绝错误，不改选其他来源。
func TestConfirmRecoverSourceVersionRejected(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	// 升级到 v2。
	res, err := a.Upgrade("s", []string{"v1"}, v2Rules(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 预览接受 v1、v2，选中 v2 最新记录。
	preview, err := a.PreviewRecover("s", []string{"v1", "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Source != res.Record.ID {
		t.Fatalf("应选中 v2 记录，得到 %s", preview.Source)
	}
	// 确认只接受 v1：来源版本被拒绝。
	if _, err := a.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"}); !errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("来源版本不被接受应 VersionRejected，得到 %T: %v", err, err)
	}
}

// TestConfirmRecoverSourceFromOtherSlot 其他槽的记录按记录不存在拒绝。
func TestConfirmRecoverSourceFromOtherSlot(t *testing.T) {
	a, _ := newTestArchive(t)
	saveN(t, a, "s", 1)
	other := saveN(t, a, "other", 1)
	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmRecover("s", preview.Current, other[0], []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("其他槽记录应 NotFound，得到 %T: %v", err, err)
	}
}

// TestConfirmRecoverOrphanNotInHistory 未生效的残留记录按记录不存在拒绝。
func TestConfirmRecoverOrphanNotInHistory(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1)
	// 写一条孤儿记录：文件完整，但槽指针与历史都不包含它。
	orphan := writeRawRecord(t, a, ids[0], false, baseWorld(t).Snapshot())
	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmRecover("s", preview.Current, orphan, []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("未生效残留记录应 NotFound，得到 %T: %v", err, err)
	}
}

// TestConfirmRecoverTimeRollbackAllowed 允许时间片回到来源时刻：来源时间片
// 比当前小，确认后新记录时间片为来源时刻。
func TestConfirmRecoverTimeRollbackAllowed(t *testing.T) {
	a, _ := newTestArchive(t)
	// 首存时间片 0；覆盖到时间片 5。
	w0 := baseWorld(t)
	r0, err := a.Save("s", w0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w0.Apply(Commit{Time: 5}); err != nil {
		t.Fatal(err)
	}
	r1, err := a.Replace("s", w0, r0.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 最新（时间片 5）损坏，预览选中时间片 0 的首存。
	corruptChecksum(t, a.dir, r1.ID)
	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Source != r0.ID || preview.State.Time != 0 {
		t.Fatalf("应选中时间片 0 的首存，得到 source=%s time=%d", preview.Source, preview.State.Time)
	}
	info, err := a.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != info.ID || rec.State.Time != 0 {
		t.Fatalf("确认后时间片应回到来源时刻 0，得到 id=%s time=%d", rec.ID, rec.State.Time)
	}
}

// TestConfirmRecoverReopenAndContinue 确认后重新打开存档，能读新记录并
// 继续提交、覆盖。
func TestConfirmRecoverReopenAndContinue(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	corruptChecksum(t, dir, ids[0])
	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"}); err != nil {
		t.Fatal(err)
	}

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatal(err)
	}
	// 继续提交。
	if _, err := w.Apply(Commit{Time: 9}); err != nil {
		t.Fatalf("确认后继续提交失败: %v", err)
	}
	// 继续覆盖。
	if _, err := a2.Replace("s", w, rec.ID); err != nil {
		t.Fatalf("确认后继续覆盖失败: %v", err)
	}
}

// TestConfirmRecoverBranchUnaffected 已存在的分支不受确认影响。
func TestConfirmRecoverBranchUnaffected(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	b0, err := a.Branch("s", ids[2], "b")
	if err != nil {
		t.Fatal(err)
	}
	// 损坏 s 的最新记录并确认恢复到 r1。
	corruptChecksum(t, dir, ids[0])
	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"}); err != nil {
		t.Fatal(err)
	}

	// 分支仍指向分支首记录，历史不变。
	brec, err := a.Latest("b", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if brec.ID != b0.ID {
		t.Fatalf("分支受确认影响: %s", brec.ID)
	}
	bhist, err := a.History("b")
	if err != nil {
		t.Fatal(err)
	}
	if len(bhist) != 1 || bhist[0].ID != b0.ID {
		t.Fatalf("分支历史受确认影响: %+v", bhist)
	}
}

// TestConfirmRecoverConcurrent 并发确认同一当前标识，最多一个成功。
func TestConfirmRecoverConcurrent(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	corruptChecksum(t, dir, ids[0])

	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}

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
			_, cerr := ga.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case cerr == nil:
				wins++
			case isConflict(cerr):
				conflicts++
			default:
				errs = append(errs, cerr)
			}
		}()
	}
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("并发确认出现非冲突错误: %v", errs)
	}
	if wins != 1 || conflicts != n-1 {
		t.Fatalf("期望恰好 1 成功 %d 冲突，得到 %d 成功 %d 冲突", n-1, wins, conflicts)
	}
}

// TestConfirmRecoverVersusReplaceConcurrent 确认与普通覆盖竞争同一当前
// 标识，最多一个成功。
func TestConfirmRecoverVersusReplaceConcurrent(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	corruptChecksum(t, dir, ids[0])

	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, conflicts := 0, 0
	errs := make([]error, 0)
	for i := 0; i < 6; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ga, _ := Open(dir)
			_, err := ga.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"})
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
			_, err := ga.Replace("s", w, preview.Current)
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

// TestConfirmRecoverLegacyPointer 旧格式裸指针下确认恢复仍可工作：确认
// 后指针持有完整历史，新记录排在最前。
func TestConfirmRecoverLegacyPointer(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	writeLegacyPointer(t, a, "s", ids[0])

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := a2.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Current != ids[0] || preview.Source != ids[0] {
		t.Fatalf("旧指针下预览结果错误: current=%s source=%s", preview.Current, preview.Source)
	}
	info, err := a2.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("旧指针下确认恢复失败: %v", err)
	}

	// 确认后指针持有完整历史。
	p, err := a2.readSlotPointerLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	want := []RecordID{info.ID, ids[0], ids[1], ids[2]}
	if len(p.History) != len(want) {
		t.Fatalf("确认后指针应持有完整历史 %v，得到 %v", want, p.History)
	}
	for i := range want {
		if p.History[i] != want[i] {
			t.Fatalf("历史次序错误: %v", p.History)
		}
	}

	// 新记录损坏后，恢复能找到旧历史里的记录。
	corruptChecksum(t, dir, info.ID)
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("确认后应能恢复: %v", err)
	}
	if rec.ID != ids[0] {
		t.Fatalf("应恢复 %s，得到 %s", ids[0], rec.ID)
	}
}

// TestConfirmRecoverReopenAfterInterruptedWrite 写入中断后重开，槽只能
// 保持确认前的指向和历史，或同时看到新记录与更新后的历史；尚未生效的
// 新记录不能进入恢复候选。
func TestConfirmRecoverReopenAfterInterruptedWrite(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	corruptChecksum(t, dir, ids[0])

	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟崩溃现场：新记录已写完，但槽指针尚未提交。
	orphan := writeRawRecord(t, a, preview.Source, false, preview.State)

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 指针仍指向确认前的当前记录；孤儿记录不进入恢复候选。
	if latest, _ := a2.readLatestLocked("s"); latest != ids[0] {
		t.Fatalf("重开后槽指针应保持确认前指向 %s，得到 %s", ids[0], latest)
	}
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("重开后应能恢复: %v", err)
	}
	if rec.ID != preview.Source {
		t.Fatalf("孤儿记录不应进入恢复候选，得到 %s（孤儿 %s）", rec.ID, orphan)
	}

	// 真正执行确认恢复。
	if _, err := a2.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	// 确认后槽指向新记录，历史更新；孤儿仍不在候选中。
	hist, err := a2.History("s")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hist {
		if h.ID == orphan {
			t.Fatal("孤儿记录不应出现在历史中")
		}
	}
	rec2, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec2.ID == orphan {
		t.Fatal("孤儿记录不应进入恢复候选")
	}
}

// TestConfirmRecoverCallerMutationIsolation 确认不使用调用方修改过的预览
// 状态：新记录的状态来自磁盘上的来源记录。
func TestConfirmRecoverCallerMutationIsolation(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1)
	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	// 调用方修改预览状态。
	preview.State.Characters[0].Location = "MUTATED"
	preview.State.Rules.Version = "MUTATED"
	preview.State.Time = 999
	if _, err := a.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.State.Characters[0].Location != "hall" {
		t.Fatalf("新记录状态被调用方修改影响: %+v", rec.State.Characters[0])
	}
	if rec.State.Rules.Version != "v1" {
		t.Fatalf("新记录规则版本被调用方修改影响: %s", rec.State.Rules.Version)
	}
	if rec.State.Time != 1 {
		t.Fatalf("新记录时间片被调用方修改影响: %d", rec.State.Time)
	}
	if ids[0] == "" {
		t.Fatal("记录标识不应为空")
	}
}

// TestConfirmRecoverSourceMiddleRecordBrokenParent 来源记录自身完好但父
// 关系断裂（中间记录被删除）时，按损坏拒绝。
func TestConfirmRecoverSourceMiddleRecordBrokenParent(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0
	// 删除 r2，使 r3 的父关系失效；最新记录损坏。
	deleteRecord(t, dir, ids[1])
	corruptChecksum(t, dir, ids[0])

	preview, err := a.PreviewRecover("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	// 预览应越过父关系失效的 r3，选中 r1（其父 r0 仍在）。
	if preview.Source != ids[2] {
		t.Fatalf("应选中父关系仍在的 %s，得到 %s", ids[2], preview.Source)
	}
	// 确认成功。
	if _, err := a.ConfirmRecover("s", preview.Current, preview.Source, []string{"v1"}); err != nil {
		t.Fatalf("来源父关系完好时确认应成功: %v", err)
	}
}

// TestConfirmRecoverSlotMissing 槽不存在报不存在。
func TestConfirmRecoverSlotMissing(t *testing.T) {
	a, _ := newTestArchive(t)
	if _, err := a.ConfirmRecover("nope", "r1", "r2", []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("槽不存在应 NotFound，得到 %T: %v", err, err)
	}
}
