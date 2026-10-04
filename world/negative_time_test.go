package world

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rewriteRecordTime 把已有记录的时间片改写为给定值并重算内容校验和，
// 模拟一份可正常解析、校验和匹配、父记录存在但时间片非法的存档记录。
func rewriteRecordTime(t *testing.T, a *Archive, id RecordID, time int) {
	t.Helper()
	path := recordPath(a.dir, id)
	env, err := loadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	env.State.Time = time
	env.Checksum = computeChecksum(env)
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// forgeNegativeTimeLatest 写入一条校验和正确但时间片为负的记录（父记录为
// 槽当前最新记录），并把它按正常保存路径提交为槽的最新记录。
func forgeNegativeTimeLatest(t *testing.T, a *Archive, slot string, time int) RecordID {
	t.Helper()
	p, err := a.readSlotPointerLocked(slot)
	if err != nil {
		t.Fatal(err)
	}
	st := baseWorld(t).Snapshot()
	st.Time = time
	id := writeRawRecord(t, a, p.Latest, false, st)
	updatePointer(t, a, slot, id)
	return id
}

// assertNegativeTimeCorrupt 断言 err 是保留了被拒绝记录标识、且说明时间片
// 为负的 *CorruptError（而不是版本拒绝或其它错误）。
func assertNegativeTimeCorrupt(t *testing.T, err error, id RecordID) {
	t.Helper()
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("应为 *CorruptError，得到 %T: %v", err, err)
	}
	if ce.Record != id {
		t.Fatalf("CorruptError 应保留被拒绝的记录标识 %s，得到 %q", id, ce.Record)
	}
	if !strings.Contains(ce.Error(), "时间片为负") {
		t.Fatalf("错误应说明时间片为负: %v", ce)
	}
	if errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("负时间片不应报成版本拒绝: %v", err)
	}
}

// recordFileBytes 读取记录文件的原始字节。
func recordFileBytes(t *testing.T, a *Archive, id RecordID) []byte {
	t.Helper()
	data, err := os.ReadFile(recordPath(a.dir, id))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// recordCount 返回 records 目录中的记录文件数。
func recordCount(t *testing.T, a *Archive) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(a.dir, recordsName))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// TestLatestAndRecordNegativeTimeCorrupt 校验和匹配、父记录存在、版本也在
// 可接受集合内，但时间片为负的记录仍属损坏：Latest 与 Record 都返回保留
// 记录标识并说明时间片为负的 *CorruptError，不按版本拒绝处理，也不把
// 时间改成零；读取保持只读，不修补这份记录。
func TestLatestAndRecordNegativeTimeCorrupt(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	neg := forgeNegativeTimeLatest(t, a, "s", -3)

	before := recordFileBytes(t, a, neg)

	_, err := a.Latest("s", []string{"v1"})
	assertNegativeTimeCorrupt(t, err, neg)

	_, err = a.Record("s", neg, []string{"v1"})
	assertNegativeTimeCorrupt(t, err, neg)

	// 重复读取仍报损坏：记录没有被悄悄修补，文件字节保持原样。
	if _, err := a.Latest("s", []string{"v1"}); err == nil {
		t.Fatal("损坏记录不应被读取修补")
	}
	if got := recordFileBytes(t, a, neg); string(got) != string(before) {
		t.Fatal("读取改写了损坏记录")
	}

	// 本槽更早的合法记录不受影响，仍可正常读取。
	rec, err := a.Record("s", ids[0], []string{"v1"})
	if err != nil {
		t.Fatalf("合法历史记录应可读: %v", err)
	}
	if rec.State.Time != 2 {
		t.Fatalf("合法记录时间片应为 2，得到 %d", rec.State.Time)
	}
}

// TestRecoverSkipsNegativeTime 恢复读取与恢复预览按本槽保存次序越过时间片
// 为负的记录，选中最近一份时间合法、其余校验通过且版本可接受的记录；预览
// 的当前标识仍是槽实际指向的（损坏）记录，来源标识对应选中的完好记录。
func TestRecoverSkipsNegativeTime(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2(time 2) r1 r0
	neg := forgeNegativeTimeLatest(t, a, "s", -1)

	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应越过负时间片记录恢复: %v", err)
	}
	if rec.ID != ids[0] || rec.State.Time != 2 {
		t.Fatalf("应恢复 %s（时间片 2），得到 %s（时间片 %d）", ids[0], rec.ID, rec.State.Time)
	}

	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("预览应越过负时间片记录: %v", err)
	}
	if pv.Current != neg {
		t.Fatalf("预览当前标识应是槽实际指向的 %s，得到 %s", neg, pv.Current)
	}
	if pv.Source != ids[0] || pv.State.Time != 2 {
		t.Fatalf("预览来源应是 %s（时间片 2），得到 %s（时间片 %d）", ids[0], pv.Source, pv.State.Time)
	}

	// 负时间片记录没有被修补：再次恢复结果一致，Latest 仍报损坏。
	if _, err := a.Latest("s", []string{"v1"}); err == nil {
		t.Fatal("恢复/预览不应修补损坏记录")
	}
	rec2, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil || rec2.ID != ids[0] {
		t.Fatalf("重复恢复结果应一致: %+v err=%v", rec2, err)
	}
}

// TestRecoverAllNegativeTimeUnrecoverable 本槽没有任何时间合法的完好记录
// 时，恢复读取与恢复预览都返回可由 errors.Is 判断的 ErrUnrecoverable。
func TestRecoverAllNegativeTimeUnrecoverable(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	rewriteRecordTime(t, a, info.ID, -7)

	if _, err := a.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("唯一记录时间片为负应不可恢复，得到 %T: %v", err, err)
	}
	if _, err := a.PreviewRecovery("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("预览同样应不可恢复，得到 %T: %v", err, err)
	}
}

// TestBranchNegativeTimeSourceRejected 以时间片为负的记录作分支来源时按
// 损坏来源拒绝：不创建目标槽或新记录，源槽指向与历史保持原样，也不换用
// 其它来源完成请求。
func TestBranchNegativeTimeSourceRejected(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	neg := forgeNegativeTimeLatest(t, a, "s", -2)
	before := recordCount(t, a)

	_, err := a.Branch("s", neg, "b")
	assertNegativeTimeCorrupt(t, err, neg)

	if _, err := a.readLatestLocked("b"); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("不应创建目标槽，得到 %v", err)
	}
	if got := recordCount(t, a); got != before {
		t.Fatalf("被拒绝的分支不应产生新记录: %d -> %d", before, got)
	}
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != neg {
		t.Fatalf("源槽指向不应改变: %+v err=%v", p, err)
	}

	// 同一槽中时间合法的记录仍可正常作为分支来源。
	if _, err := a.Branch("s", ids[1], "ok"); err != nil {
		t.Fatalf("合法来源应可分支: %v", err)
	}
}

// TestConfirmRecoveryNegativeTimeSourceRejected 确认恢复指定的来源时间片
// 为负且其余前提（当前标识一致、来源在本槽历史中）都满足时，按损坏来源
// 拒绝：槽指向与历史不变，不产生新记录，不改选其它来源。
func TestConfirmRecoveryNegativeTimeSourceRejected(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1) // r1(time 1) r0(time 0)

	// 先在合法记录之上正常覆盖一条合法记录作为当前记录，再把历史中间的
	// 记录改写成负时间片，使它成为本槽历史中的损坏中间记录（普通覆盖
	// 本身会拒绝以损坏记录为父，不能直接用它来构造这一场景）。
	rec, err := a.Record("s", ids[0], []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{Time: 5}); err != nil {
		t.Fatal(err)
	}
	cur, err := a.Replace("s", w, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	neg := ids[0]
	rewriteRecordTime(t, a, neg, -4)
	before := recordCount(t, a)

	_, err = a.ConfirmRecovery("s", cur.ID, neg, []string{"v1"})
	assertNegativeTimeCorrupt(t, err, neg)

	if got := recordCount(t, a); got != before {
		t.Fatalf("被拒绝的确认不应产生新记录: %d -> %d", before, got)
	}
	p, err := a.readSlotPointerLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	if p.Latest != cur.ID {
		t.Fatalf("槽指向不应改变: %s -> %s", cur.ID, p.Latest)
	}
	wantHist := []RecordID{cur.ID, neg, ids[1]}
	if len(p.History) != len(wantHist) {
		t.Fatalf("历史不应改变: %v", p.History)
	}
	for i := range wantHist {
		if p.History[i] != wantHist[i] {
			t.Fatalf("历史不应改变: %v", p.History)
		}
	}

	// 确认到较早但非负的时间片仍然允许（本槽历史中的合法记录）。
	if _, err := a.ConfirmRecovery("s", cur.ID, ids[1], []string{"v1"}); err != nil {
		t.Fatalf("确认到非负的更早时间片应允许: %v", err)
	}
}

// TestUpgradeNegativeTimeRejected 槽最新记录时间片为负时，升级检查与正式
// 升级都按损坏来源拒绝，不产生新记录、不改变槽指向。
func TestUpgradeNegativeTimeRejected(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	neg := forgeNegativeTimeLatest(t, a, "s", -1)
	before := recordCount(t, a)

	_, err := a.CheckUpgrade("s", []string{"v1"}, v2Rules())
	assertNegativeTimeCorrupt(t, err, neg)

	_, err = a.Upgrade("s", []string{"v1"}, v2Rules(), neg)
	assertNegativeTimeCorrupt(t, err, neg)

	if got := recordCount(t, a); got != before {
		t.Fatalf("被拒绝的升级不应产生新记录: %d -> %d", before, got)
	}
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != neg {
		t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
	}
}

// TestMigrateNegativeTimeRejected 槽最新记录时间片为负时，迁移预览与正式
// 迁移都按损坏来源拒绝，不产生新记录、不改变槽指向。
func TestMigrateNegativeTimeRejected(t *testing.T) {
	a, _ := newTestArchive(t)
	saveBase(t, a, "s")
	neg := forgeNegativeTimeLatest(t, a, "s", -1)
	before := recordCount(t, a)

	_, err := a.PreviewMigration("s", []string{"v1"}, v2Rules(), nil, nil)
	assertNegativeTimeCorrupt(t, err, neg)

	_, err = a.Migrate("s", []string{"v1"}, v2Rules(), nil, nil, neg)
	assertNegativeTimeCorrupt(t, err, neg)

	if got := recordCount(t, a); got != before {
		t.Fatalf("被拒绝的迁移不应产生新记录: %d -> %d", before, got)
	}
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != neg {
		t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
	}
}

// TestNegativeTimeLegacyPointerHistory 旧格式裸指针沿父链重建历史时，负
// 时间片记录同样校验不过：重建停在那里，无法确认归属的更老记录不进入
// 恢复候选。
func TestNegativeTimeLegacyPointerHistory(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3)          // r3 r2 r1 r0
	rewriteRecordTime(t, a, ids[2], -1) // r1 时间片为负
	writeLegacyPointer(t, a, "s", ids[0])

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 最新与次新完好：恢复仍命中最新记录。
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("负时间片记录之前的完好记录应可恢复: %v", err)
	}
	if rec.ID != ids[0] {
		t.Fatalf("应恢复最新记录 %s，得到 %s", ids[0], rec.ID)
	}

	// 最新与次新也损坏后，链在负时间片记录处断裂，首存记录无法确认归属。
	corruptChecksum(t, dir, ids[0])
	corruptChecksum(t, dir, ids[1])
	if _, err := a2.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("断裂处更老的记录不应进入候选，得到 %T: %v", err, err)
	}
}

// TestZeroAndPositiveTimeRecordAccepted 时间片为零或正数、其余校验通过的
// 记录继续正常使用，不受负时间片拒绝规则影响。
func TestZeroAndPositiveTimeRecordAccepted(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")

	// 时间片为零。
	rewriteRecordTime(t, a, info.ID, 0)
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("时间片为零应可读: %v", err)
	}
	if rec.State.Time != 0 {
		t.Fatalf("时间片应保持 0，得到 %d", rec.State.Time)
	}

	// 时间片为正数。
	rewriteRecordTime(t, a, info.ID, 9)
	rec, err = a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("时间片为正应可读: %v", err)
	}
	if rec.State.Time != 9 {
		t.Fatalf("时间片应保持 9，得到 %d", rec.State.Time)
	}
	w, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatalf("正时间片记录应可重建世界: %v", err)
	}
	if _, err := w.Apply(Commit{Time: 10}); err != nil {
		t.Fatalf("重建后应能继续推进: %v", err)
	}
}
