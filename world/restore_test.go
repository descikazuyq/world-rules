package world

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// TestPreviewRecoveryHealthySelectsCurrent 槽当前记录完好且版本可接受时，
// 预览的当前标识与来源标识都是最新记录，并返回其完整状态。
func TestPreviewRecoveryHealthySelectsCurrent(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0

	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if pv.Current != ids[0] || pv.Source != ids[0] {
		t.Fatalf("完好槽应选当前记录: current=%s source=%s latest=%s",
			pv.Current, pv.Source, ids[0])
	}
	if pv.State.Time != 2 || pv.State.Seed != 42 || pv.State.Rules.Version != "v1" {
		t.Fatalf("预览状态与最新记录不符: %+v", pv.State)
	}
}

// TestPreviewRecoveryAcrossDamagedRecords 最新记录损坏/缺失时，当前标识仍是
// 损坏记录本身，来源按保存次序越过受损记录落到最近完好记录，并返回其完整
// 世界状态。
func TestPreviewRecoveryAcrossDamagedRecords(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 4) // r4 r3 r2 r1 r0
	truncateRecord(t, dir, ids[0])
	corruptChecksum(t, dir, ids[1])

	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应越过受损记录预览: %v", err)
	}
	if pv.Current != ids[0] {
		t.Fatalf("当前标识应为受损的 %s，得到 %s", ids[0], pv.Current)
	}
	if pv.Source != ids[2] {
		t.Fatalf("来源应为 %s，得到 %s", ids[2], pv.Source)
	}
	// ids[2] 对应时间片 2，内容完整。
	if pv.State.Time != 2 || pv.State.Seed != 42 {
		t.Fatalf("来源状态错误: %+v", pv.State)
	}
	chars := pv.State.Characters
	if len(chars) != 2 || chars[0].ID != "hero" ||
		!reflect.DeepEqual(chars[0].Items, []CharacterItem{{Item: "gold", Count: 1}}) {
		t.Fatalf("角色/物品未完整返回: %+v", chars)
	}

	// 最新记录被直接删除时，当前标识仍是已删除的那个。
	deleteRecord(t, dir, ids[2])
	pv2, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("删除来源后应继续回溯: %v", err)
	}
	if pv2.Current != ids[0] || pv2.Source != ids[3] {
		t.Fatalf("当前仍应是 %s、来源应落到 %s，得到 current=%s source=%s",
			ids[0], ids[3], pv2.Current, pv2.Source)
	}
}

// TestPreviewRecoveryReadOnly 预览不改写任何数据：槽指针字节与 records
// 目录条目集合在多次预览前后完全一致，槽当前的损坏记录也不会被修补。
func TestPreviewRecoveryReadOnly(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3)
	corruptChecksum(t, dir, ids[0])

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
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	// 改预览返回的状态不影响存档。
	pv.State.Seed = 999
	pv.State.Characters[0].Location = "cave"
	if _, err := a.PreviewRecovery("s", []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	ptrAfter, filesAfter := snapshot()
	if ptrBefore != ptrAfter {
		t.Fatal("预览改写了槽指针")
	}
	if !reflect.DeepEqual(filesBefore, filesAfter) {
		t.Fatalf("预览改变了 records 目录: %v vs %v", filesBefore, filesAfter)
	}
	// 槽当前仍指向损坏记录，Latest 依旧报损坏。
	if _, err := a.Latest("s", []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("预览不应修补当前记录，得到 %T: %v", err, err)
	}
}

// TestPreviewRecoveryErrors 槽不存在报不存在；槽指针无法解析报损坏；没有
// 任何校验通过且版本可接受的记录报不可恢复。
func TestPreviewRecoveryErrors(t *testing.T) {
	a, dir := newTestArchive(t)

	if _, err := a.PreviewRecovery("nope", []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("槽不存在应 NotFound，得到 %T: %v", err, err)
	}

	ids := saveN(t, a, "s", 2)
	// 指针损坏。
	if err := os.WriteFile(filepath.Join(dir, slotsName, "s.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PreviewRecovery("s", []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("指针无法解析应 Corrupt，得到 %T: %v", err, err)
	}

	// 恢复一个可读指针，再损坏全部记录 / 不给可接受版本。
	fixPointer(t, a, "s", ids[0])
	corruptChecksum(t, dir, ids[0])
	corruptChecksum(t, dir, ids[1])
	corruptChecksum(t, dir, ids[2])
	if _, err := a.PreviewRecovery("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("全无可用记录应 ErrUnrecoverable，得到 %T: %v", err, err)
	}
}

// TestPreviewRecoveryVersionSkip 最新记录版本不被接受时越过它选更早可接受
// 版本的记录；没有任何可接受版本时不可恢复。
func TestPreviewRecoveryVersionSkip(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	up, err := a.Upgrade("s", []string{"v1"}, v2Rules(), info.ID)
	if err != nil {
		t.Fatal(err)
	}

	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("只接受 v1 时应越过 v2 记录: %v", err)
	}
	if pv.Current != up.Record.ID || pv.Source != info.ID {
		t.Fatalf("current=%s source=%s", pv.Current, pv.Source)
	}
	if pv.State.Rules.Version != "v1" {
		t.Fatalf("应返回 v1 来源状态，得到 %s", pv.State.Rules.Version)
	}
	if _, err := a.PreviewRecovery("s", []string{"v9"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("无接受版本应不可恢复，得到 %T: %v", err, err)
	}
}

// TestPreviewRecoveryBranchBoundary 分支只在自己的历史中预览，最新记录损坏
// 时选分支自身更早记录；分支全无可用记录时不可恢复，不借用来源槽。
func TestPreviewRecoveryBranchBoundary(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	b0, err := a.Branch("s", ids[2], "b")
	if err != nil {
		t.Fatal(err)
	}
	brec, err := a.Latest("b", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	wb, err := WorldFromState(brec.State)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wb.Apply(Commit{Time: 9}); err != nil {
		t.Fatal(err)
	}
	b1, err := a.Replace("b", wb, b0.ID)
	if err != nil {
		t.Fatal(err)
	}
	corruptChecksum(t, dir, b1.ID)

	pv, err := a.PreviewRecovery("b", []string{"v1"})
	if err != nil {
		t.Fatalf("应在分支自身历史中预览: %v", err)
	}
	if pv.Current != b1.ID || pv.Source != b0.ID {
		t.Fatalf("应预览分支首记录 %s，得到 %+v", b0.ID, pv)
	}
	corruptChecksum(t, dir, b0.ID)
	if _, err := a.PreviewRecovery("b", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("分支无可用记录应不可恢复，得到 %T: %v", err, err)
	}
}

// TestConfirmRecoveryCopiesSourceAndRewritesSlot 确认后产生以来源为父的独立
// 新记录，槽指向它；种子、完整规则、时间片、角色与物品的内容和排列原样
// 复制；旧历史按原次序保留，新记录在最前；旧记录仍可读取与分支。
func TestConfirmRecoveryCopiesSourceAndRewritesSlot(t *testing.T) {
	a, dir := newTestArchive(t)

	// 构造一条带多物品、特定排列的来源记录，再覆盖出更新的损坏记录。
	w, err := NewWorld(InitialData{
		Seed:  77,
		Rules: baseRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{
				{Item: "key", Count: 1},
				{Item: "gold", Count: 2},
			}},
			{ID: "mage", Location: "yard"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{Time: 4}); err != nil {
		t.Fatal(err)
	}
	r0, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{
		Moves: []Move{{Character: "hero", To: "yard"}},
		Time:  5,
	}); err != nil {
		t.Fatal(err)
	}
	r1, err := a.Replace("s", w, r0.ID)
	if err != nil {
		t.Fatal(err)
	}
	corruptChecksum(t, dir, r1.ID)

	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Current != r1.ID || pv.Source != r0.ID {
		t.Fatalf("预览标识错误: %+v", pv)
	}

	info, err := a.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("确认恢复失败: %v", err)
	}
	if info.ID == "" || info.ID == r0.ID || info.ID == r1.ID {
		t.Fatalf("确认应产生独立的新记录标识，得到 %s", info.ID)
	}
	if info.Parent != r0.ID {
		t.Fatalf("新记录父应为来源 %s，得到 %s", r0.ID, info.Parent)
	}
	if info.SlotFirst {
		t.Fatal("恢复产生的新记录不应标记为槽首记录")
	}
	if info.Version != "v1" {
		t.Fatalf("版本应为来源版本 v1，得到 %s", info.Version)
	}

	// 槽当前指向新记录且完好可读，内容与来源逐字一致（时间片回到 4）。
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("确认后应能直接读取新记录: %v", err)
	}
	if rec.ID != info.ID || rec.Parent != r0.ID {
		t.Fatalf("Latest 结果错误: %+v", rec)
	}
	want := pv.State
	if rec.State.Seed != want.Seed || rec.State.Time != 4 ||
		rec.State.Rules.Version != "v1" {
		t.Fatalf("新记录状态与来源不符: %+v", rec.State)
	}
	hero := rec.State.Characters[0]
	if hero.ID != "hero" || hero.Location != "hall" ||
		!reflect.DeepEqual(hero.Items, []CharacterItem{
			{Item: "key", Count: 1},
			{Item: "gold", Count: 2},
		}) {
		t.Fatalf("角色位置与物品内容/排列未原样复制: %+v", hero)
	}

	// 历史次序：新记录最前，其后旧历史按原保存次序保留（损坏记录仍在索引）。
	hist, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	wantHist := []RecordID{info.ID, r1.ID, r0.ID}
	if len(hist) != len(wantHist) {
		t.Fatalf("历史长度错误: %+v", hist)
	}
	for i := range wantHist {
		if hist[i].ID != wantHist[i] {
			t.Fatalf("历史次序错误: %+v，期望 %v", hist, wantHist)
		}
	}

	// 来源之后仍完好的旧记录可照常读取；从它分出新分支也不受影响。
	old, err := a.Record("s", r0.ID, []string{"v1"})
	if err != nil || old.ID != r0.ID {
		t.Fatalf("来源旧记录应仍可读取: %v", err)
	}
	b0, err := a.Branch("s", r0.ID, "b")
	if err != nil {
		t.Fatalf("应能从来源旧记录分支: %v", err)
	}
	if b0.Parent != r0.ID {
		t.Fatalf("分支父记录错误: %+v", b0)
	}
}

// TestConfirmRecoveryIntactCurrentStillCreatesNewRecord 当前记录完好可读且
// 版本可接受时，预览可以选它；确认仍生成独立的新记录，以当前记录为父。
func TestConfirmRecoveryIntactCurrentStillCreatesNewRecord(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1) // r1 r0

	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Current != ids[0] || pv.Source != ids[0] {
		t.Fatalf("完好槽预览应选当前记录: %+v", pv)
	}
	info, err := a.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("确认失败: %v", err)
	}
	if info.ID == ids[0] || info.Parent != ids[0] {
		t.Fatalf("应以当前记录为父生成独立新记录: %+v", info)
	}
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil || rec.ID != info.ID {
		t.Fatalf("槽应指向新记录: %+v err=%v", rec, err)
	}
}

// TestConfirmRecoveryAllowsTimeToGoBack 确认允许世界时间片回到来源时刻，
// 之后可从该时刻继续提交（只能向前）。
func TestConfirmRecoveryAllowsTimeToGoBack(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3(time3) r2(time2) r1(time1) r0(time0)

	info, err := a.ConfirmRecovery("s", ids[0], ids[2], []string{"v1"})
	if err != nil {
		t.Fatalf("确认失败: %v", err)
	}
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.State.Time != 1 {
		t.Fatalf("时间片应回到来源时刻 1，得到 %d", rec.State.Time)
	}
	// 从时刻 1 继续。
	w, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{Time: 1}); err != nil {
		t.Fatalf("同时间片提交应允许: %v", err)
	}
	if _, err := w.Apply(Commit{Time: 0}); err == nil {
		t.Fatal("时间倒退应被世界规则拒绝")
	}
	if _, err := w.Apply(Commit{Time: 2}); err != nil {
		t.Fatalf("从恢复时刻继续推进失败: %v", err)
	}
	if _, err := a.Replace("s", w, info.ID); err != nil {
		t.Fatalf("恢复后应能继续覆盖: %v", err)
	}
}

// TestConfirmRecoveryEmptyIDsConflict 当前标识或来源标识为空时返回冲突。
func TestConfirmRecoveryEmptyIDsConflict(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1)

	if _, err := a.ConfirmRecovery("s", "", ids[1], []string{"v1"}); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("空当前标识应冲突，得到 %T: %v", err, err)
	}
	if _, err := a.ConfirmRecovery("s", ids[0], "", []string{"v1"}); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("空来源标识应冲突，得到 %T: %v", err, err)
	}
}

// TestConfirmRecoverySlotMissingAndPointerCorrupt 槽不存在报不存在；槽指针
// 无法解析报损坏。
func TestConfirmRecoverySlotMissingAndPointerCorrupt(t *testing.T) {
	a, dir := newTestArchive(t)
	if _, err := a.ConfirmRecovery("nope", "rx", "ry", []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("槽不存在应 NotFound，得到 %T: %v", err, err)
	}
	ids := saveN(t, a, "s", 1)
	if err := os.WriteFile(filepath.Join(dir, slotsName, "s.json"), []byte("{xx"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmRecovery("s", ids[0], ids[1], []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("指针无法解析应 Corrupt，得到 %T: %v", err, err)
	}
}

// TestConfirmRecoveryConflictWhenSlotMoved 预览之后槽被普通覆盖或升级移走，
// 确认基于旧的当前标识必须冲突；用同一旧标识的并发确认最多一个成功。
func TestConfirmRecoveryConflictWhenSlotMoved(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 3)
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	// 并发方用最新记录做一次普通覆盖。
	w := baseWorld(t)
	if _, err := w.Apply(Commit{Time: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Replace("s", w, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"}); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("槽已移走应冲突，得到 %T: %v", err, err)
	}
	// 被拒绝的确认不应增加历史。
	hist, err := a.History("s")
	if err != nil || len(hist) != 5 { // 原有 4 条 + 覆盖 1 条
		t.Fatalf("冲突确认不应改变历史: %+v err=%v", hist, err)
	}
}

// TestConfirmRecoveryCurrentMayBeCorruptOrDeleted 即使槽当前指向的记录已损坏
// 或被删除，只要调用方给出的当前标识与之一致，就不能妨碍从本槽完好旧记录
// 恢复。
func TestConfirmRecoveryCurrentMayBeCorruptOrDeleted(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0

	// 情形一：当前记录损坏。
	corruptChecksum(t, dir, ids[0])
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Current != ids[0] || pv.Source != ids[1] {
		t.Fatalf("预览错误: %+v", pv)
	}
	info, err := a.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("当前损坏不应妨碍恢复: %v", err)
	}
	if info.Parent != ids[1] {
		t.Fatalf("父应为完好旧记录 %s，得到 %s", ids[1], info.Parent)
	}

	// 情形二：另起一槽，当前记录被删除。
	del := saveN(t, a, "d", 2)
	deleteRecord(t, dir, del[0])
	pv2, err := a.PreviewRecovery("d", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if pv2.Current != del[0] || pv2.Source != del[1] {
		t.Fatalf("预览错误: %+v", pv2)
	}
	info2, err := a.ConfirmRecovery("d", pv2.Current, pv2.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("当前被删除不应妨碍恢复: %v", err)
	}
	if rec, err := a.Latest("d", []string{"v1"}); err != nil || rec.ID != info2.ID {
		t.Fatalf("恢复后槽指向错误: %+v err=%v", rec, err)
	}
}

// TestConfirmRecoverySourceValidation 来源的归属、内容与版本在确认时重新
// 判断，且不改选其他来源，也不使用调用方修改过的预览状态。
func TestConfirmRecoverySourceValidation(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3)

	// 其他槽的记录：按记录不存在拒绝。
	other := saveBase(t, a, "o")
	if _, err := a.ConfirmRecovery("s", ids[0], other.ID, []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("他槽记录应 NotFound，得到 %T: %v", err, err)
	}

	// 未生效的孤儿记录：文件完好但不在本槽历史中，按不存在拒绝。
	w := baseWorld(t)
	if _, err := w.Apply(Commit{Time: 8}); err != nil {
		t.Fatal(err)
	}
	orphan := writeRawRecord(t, a, ids[0], false, w.Snapshot())
	if _, err := a.ConfirmRecovery("s", ids[0], orphan, []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("孤儿记录应 NotFound，得到 %T: %v", err, err)
	}

	// 来源后来被删除：不存在，且不自动改选别的来源。
	deleteRecord(t, dir, ids[2])
	if _, err := a.ConfirmRecovery("s", ids[0], ids[2], []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("来源被删应 NotFound，得到 %T: %v", err, err)
	}

	// 来源后来损坏：损坏。
	corruptChecksum(t, dir, ids[1])
	if _, err := a.ConfirmRecovery("s", ids[0], ids[1], []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("来源损坏应 Corrupt，得到 %T: %v", err, err)
	}

	// 来源版本不被确认方接受：版本拒绝。
	if _, err := a.ConfirmRecovery("s", ids[0], ids[3], []string{"v9"}); !errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("来源版本不被接受应 VersionRejected，得到 %T: %v", err, err)
	}

	// 来源失效时槽指向与历史保持不变。
	latest, err := a.readLatestLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	if latest != ids[0] {
		t.Fatalf("失败确认不应移动槽指针: %s != %s", latest, ids[0])
	}
}

// TestConfirmRecoveryIgnoresTamperedPreviewState 调用方篡改预览返回的状态
// 不影响确认结果：新记录内容始终以磁盘上重新校验过的来源为准。
func TestConfirmRecoveryIgnoresTamperedPreviewState(t *testing.T) {
	a, _ := newTestArchive(t)
	saveN(t, a, "s", 2)
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	// 即便调用方把预览状态改得面目全非，确认只接收标识，无法被污染。
	pv.State.Seed = -1
	pv.State.Time = 999
	info, err := a.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("确认失败: %v", err)
	}
	rec, err := a.Record("s", info.ID, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.State.Seed != 42 || rec.State.Time != 2 {
		t.Fatalf("新记录应以磁盘来源为准，却被调用方状态污染: %+v", rec.State)
	}
}

// TestConfirmRecoveryConcurrent 多个实例基于同一当前标识并发确认，恰好一个
// 成功，其余冲突；再用同一旧当前标识确认仍冲突。
func TestConfirmRecoveryConcurrent(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	corruptChecksum(t, dir, ids[0])

	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}

	const n = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, conflicts := 0, 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ga, err := Open(dir)
			if err != nil {
				t.Error(err)
				return
			}
			_, err = ga.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.As(err, new(*ConflictError)):
				conflicts++
			default:
				t.Errorf("非冲突错误: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 || conflicts != n-1 {
		t.Fatalf("期望 1 成功 %d 冲突，得到 %d 成功 %d 冲突", n-1, wins, conflicts)
	}

	// 同一当前标识再确认必然冲突。
	if _, err := a.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"}); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("同一当前标识上第二次确认应冲突，得到 %T: %v", err, err)
	}
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Parent != pv.Source {
		t.Fatalf("胜出恢复记录父应为 %s，得到 %s", pv.Source, rec.Parent)
	}
}

// TestConfirmRecoveryRacesWithReplace 确认与普通覆盖竞争同一当前标识：
// 最多一个成功。覆盖先得手时确认冲突；确认先得手时覆盖冲突。
func TestConfirmRecoveryRacesWithReplace(t *testing.T) {
	a, _ := newTestArchive(t)
	saveN(t, a, "s", 1)
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}

	// 覆盖先成功：确认冲突，槽停在覆盖记录上。
	w := baseWorld(t)
	if _, err := w.Apply(Commit{Time: 5}); err != nil {
		t.Fatal(err)
	}
	r2, err := a.Replace("s", w, pv.Current)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"}); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("覆盖抢先时确认应冲突，得到 %T: %v", err, err)
	}

	// 确认先成功（基于 r2）：再用 r2 覆盖应冲突。
	pv2, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := a.ConfirmRecovery("s", pv2.Current, pv2.Source, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Replace("s", w, r2.ID); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("确认抢先时旧覆盖应冲突，得到 %T: %v", err, err)
	}
	latest, err := a.Latest("s", []string{"v1"})
	if err != nil || latest.ID != info.ID {
		t.Fatalf("槽应停在恢复记录上: %+v err=%v", latest, err)
	}
}

// TestConfirmRecoveryReopenAndContinue 重开存档后可读到恢复的新记录，并能
// 继续提交、覆盖与升级。
func TestConfirmRecoveryReopenAndContinue(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	corruptChecksum(t, dir, ids[0])
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := a.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("重开后应读到新记录: %v", err)
	}
	if rec.ID != info.ID || rec.State.Time != 1 {
		t.Fatalf("重开读取错误: %+v", rec)
	}
	// 继续提交 + 覆盖。
	w, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{Time: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := a2.Replace("s", w, info.ID); err != nil {
		t.Fatalf("恢复后覆盖失败: %v", err)
	}
	// 升级也照常工作。
	latest, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a2.Upgrade("s", []string{"v1"}, v2Rules(), latest.ID); err != nil {
		t.Fatalf("恢复后升级失败: %v", err)
	}
}

// TestConfirmRecoveryExistingBranchesUnaffected 恢复前已分出的分支在恢复后
// 仍独立可读、可继续保存，不受本槽恢复影响。
func TestConfirmRecoveryExistingBranchesUnaffected(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3)
	b0, err := a.Branch("s", ids[3], "b")
	if err != nil {
		t.Fatal(err)
	}
	corruptChecksum(t, dir, ids[0])
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"}); err != nil {
		t.Fatal(err)
	}

	brec, err := a.Latest("b", []string{"v1"})
	if err != nil || brec.ID != b0.ID || brec.State.Time != 0 {
		t.Fatalf("已有分支受恢复影响: %+v err=%v", brec, err)
	}
	wb, err := WorldFromState(brec.State)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wb.Apply(Commit{Time: 6}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Replace("b", wb, b0.ID); err != nil {
		t.Fatalf("分支继续保存失败: %v", err)
	}
}

// TestConfirmRecoveryLegacyPointer 旧格式裸指针沿用已有的历史归属限制：
// 当前完好时可预览并确认，确认后重建的旧历史与新记录一并持久化，从此获得
// 完整恢复能力；断裂链无法确认归属的旧记录仍被拒绝。
func TestConfirmRecoveryLegacyPointer(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	writeLegacyPointer(t, a, "s", ids[0])

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	pv, err := a2.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("裸指针下应可预览: %v", err)
	}
	if pv.Current != ids[0] || pv.Source != ids[0] {
		t.Fatalf("预览错误: %+v", pv)
	}
	info, err := a2.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("裸指针下确认失败: %v", err)
	}

	// 指针现在持有完整历史（新记录 + 重建出的旧链）。
	p, err := a2.readSlotPointerLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	want := []RecordID{info.ID, ids[0], ids[1], ids[2]}
	if len(p.History) != len(want) {
		t.Fatalf("历史应为 %v，得到 %v", want, p.History)
	}
	for i := range want {
		if p.History[i] != want[i] {
			t.Fatalf("历史次序错误: %v", p.History)
		}
	}

	// 新记录、旧最新与中间记录都失效时，仍能恢复到最早的首存记录——旧
	// 历史已随确认获得完整索引，不再受单条父链断裂限制。
	corruptChecksum(t, dir, info.ID)
	deleteRecord(t, dir, ids[0])
	truncateRecord(t, dir, ids[1])
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应恢复到首存记录: %v", err)
	}
	if rec.ID != ids[2] {
		t.Fatalf("应恢复 %s，得到 %s", ids[2], rec.ID)
	}

	// 断裂旧链：删除中间记录并降级成裸指针，无法经断裂处确认归属的旧
	// 记录不能作为来源（按不存在拒绝），预览也不可恢复。
	b, _ := newTestArchive(t)
	bids := saveN(t, b, "x", 2)
	deleteRecord(t, b.dir, bids[1])
	writeLegacyPointer(t, b, "x", bids[0])
	if _, err := b.PreviewRecovery("x", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("断裂裸指针应不可恢复，得到 %T: %v", err, err)
	}
	if _, err := b.ConfirmRecovery("x", bids[0], bids[2], []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("无法确认归属的旧记录应 NotFound，得到 %T: %v", err, err)
	}
}

// TestConfirmRecoveryInterruptedWrite 写入中断后重开：槽保持确认前指向与
// 历史，写完但未生效的新记录不能进入恢复候选、也不能作为来源；之后一次
// 真正的确认照常成功。
func TestConfirmRecoveryInterruptedWrite(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 1)
	w := baseWorld(t)
	if _, err := w.Apply(Commit{Time: 2}); err != nil {
		t.Fatal(err)
	}
	// 模拟确认在“记录已写完、指针尚未改名”时崩溃。
	orphan := writeRawRecord(t, a, ids[1] /*来源取首存*/, false, w.Snapshot())
	junk := filepath.Join(dir, recordsName, ".tmp-unfinished")
	if err := os.WriteFile(junk, []byte("{x"), 0o600); err != nil {
		t.Fatal(err)
	}

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 指针仍指向确认前的 r1，预览选 r1，孤儿记录既不是来源也不在历史中。
	pv, err := a2.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Current != ids[0] || pv.Source != ids[0] {
		t.Fatalf("中断后预览应停在确认前状态: %+v", pv)
	}
	if _, err := a2.ConfirmRecovery("s", ids[0], orphan, []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("未生效的新记录不能作为来源，得到 %T: %v", err, err)
	}
	// 一次真正的确认成功，历史只含新记录与原有两条，孤儿不在其中。
	info, err := a2.ConfirmRecovery("s", ids[0], ids[1], []string{"v1"})
	if err != nil {
		t.Fatalf("正式确认应成功: %v", err)
	}
	hist, err := a2.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 {
		t.Fatalf("历史不应包含孤儿记录: %+v", hist)
	}
	for _, h := range hist {
		if h.ID == orphan {
			t.Fatal("未生效的孤儿记录进入了历史")
		}
	}
	if hist[0].ID != info.ID {
		t.Fatalf("新记录应排在最前: %+v", hist)
	}
}

// fixPointer 在测试中把槽指针重写为仅指向 id 的完好指针（用于从“指针损坏”
// 场景恢复测试前提）。
func fixPointer(t *testing.T, a *Archive, slot string, id RecordID) {
	t.Helper()
	lock, err := acquireLock(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	if err := a.persistSlotPointerLocked(slot, slotPointer{
		Latest:  id,
		History: []RecordID{id},
	}); err != nil {
		t.Fatal(err)
	}
}
