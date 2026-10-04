package world

import (
	"errors"
	"reflect"
	"testing"
)

// assertReplaceRejected 断言一次普通覆盖被拒绝：返回预期类型的错误、槽仍
// 指向原标识、历史次序不变、records 目录不新增文件。
func assertReplaceRejected(t *testing.T, a *Archive, slot string, beforeCount int, pointer slotPointer, info RecordInfo, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("当前记录不可读时覆盖应被拒绝，却生成了 %+v", info)
	}
	if got := recordCount(t, a); got != beforeCount {
		t.Fatalf("被拒绝的覆盖不应产生新记录: %d -> %d", beforeCount, got)
	}
	p, perr := a.readSlotPointerLocked(slot)
	if perr != nil {
		t.Fatal(perr)
	}
	if p.Latest != pointer.Latest {
		t.Fatalf("槽指向不应改变: %s -> %s", pointer.Latest, p.Latest)
	}
	if !reflect.DeepEqual(p.History, pointer.History) {
		t.Fatalf("历史不应改变: %v -> %v", pointer.History, p.History)
	}
}

// TestReplaceCurrentMissingRejected 槽仍指向的当前记录文件被删除时，普通
// 覆盖按记录不存在拒绝，不写新记录、不移动槽指针；错误保留记录标识，
// 调用方可继续用已有错误类型判断原因。
func TestReplaceCurrentMissingRejected(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 1) // r1 r0
	w := baseWorld(t)
	if _, err := w.Apply(Commit{Time: 5}); err != nil {
		t.Fatal(err)
	}
	wantWorld := w.Snapshot()
	pointer, err := a.readSlotPointerLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	deleteRecord(t, dir, ids[0])
	before := recordCount(t, a)

	info, err := a.Replace("s", w, ids[0])
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("当前记录缺失应返回 NotFoundError，得到 %T: %v", err, err)
	}
	if nf.Record != ids[0] {
		t.Fatalf("错误应指出受影响记录 %s，得到 %q", ids[0], nf.Record)
	}
	assertReplaceRejected(t, a, "s", before, pointer, info, err)
	// 调用方传入的世界保持原有状态。
	if got := w.Snapshot(); !reflect.DeepEqual(got, wantWorld) {
		t.Fatalf("拒绝覆盖不应改变传入世界: %+v -> %+v", wantWorld, got)
	}
}

// TestReplaceCurrentCorruptRejected 当前记录被截断、校验和不匹配、时间片
// 非法或父记录缺失时，普通覆盖一律按损坏拒绝，错误同时带槽名与记录标识；
// 槽指向、历史与记录文件保持原样，不另选更老记录作父。
func TestReplaceCurrentCorruptRejected(t *testing.T) {
	a, dir := newTestArchive(t)

	// 情形一：当前记录被截断到无法解析。
	ids := saveN(t, a, "trunc", 1)
	pointer, err := a.readSlotPointerLocked("trunc")
	if err != nil {
		t.Fatal(err)
	}
	before := recordCount(t, a)
	truncateRecord(t, dir, ids[0])
	info, err := a.Replace("trunc", baseWorld(t), ids[0])
	assertCorruptRejected(t, a, "trunc", ids[0], before, pointer, info, err)

	// 情形二：校验和不匹配。
	cs := saveN(t, a, "cs", 1)
	pointer, _ = a.readSlotPointerLocked("cs")
	before = recordCount(t, a)
	corruptChecksum(t, dir, cs[0])
	info, err = a.Replace("cs", baseWorld(t), cs[0])
	assertCorruptRejected(t, a, "cs", cs[0], before, pointer, info, err)

	// 情形三：校验和匹配但世界时间片为负。
	nt := saveN(t, a, "neg", 1)
	neg := forgeNegativeTimeLatest(t, a, "neg", -3)
	pointer, _ = a.readSlotPointerLocked("neg")
	before = recordCount(t, a)
	info, err = a.Replace("neg", baseWorld(t), neg)
	assertCorruptRejected(t, a, "neg", neg, before, pointer, info, err)
	if nt[0] == neg {
		t.Fatal("测试前提：负时间片记录应是新提交的最新记录")
	}

	// 情形四：当前记录自身完好，但它引用的父记录已被删除。
	pm := saveN(t, a, "pm", 1) // r1(父 r0) r0
	pointer, _ = a.readSlotPointerLocked("pm")
	deleteRecord(t, dir, pm[1])
	before = recordCount(t, a)
	info, err = a.Replace("pm", baseWorld(t), pm[0])
	assertCorruptRejected(t, a, "pm", pm[0], before, pointer, info, err)
}

// assertCorruptRejected 断言覆盖返回带槽名与记录标识的 *CorruptError，且
// 不写入、不移动槽指针与历史。
func assertCorruptRejected(t *testing.T, a *Archive, slot string, id RecordID, beforeCount int, pointer slotPointer, info RecordInfo, err error) {
	t.Helper()
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("槽 %s 当前记录损坏应返回 CorruptError，得到 %T: %v", slot, err, err)
	}
	if ce.Record != id {
		t.Fatalf("槽 %s 的错误应指出记录 %s，得到 %q", slot, id, ce.Record)
	}
	if ce.Slot != slot {
		t.Fatalf("错误应指出受影响槽 %q，得到 %q", slot, ce.Slot)
	}
	if errors.As(err, new(*ConflictError)) {
		t.Fatalf("当前记录损坏不应报成冲突: %v", err)
	}
	assertReplaceRejected(t, a, slot, beforeCount, pointer, info, err)
}

// TestReplaceStaleExpectedConflictPrecedence 预期标识已过期时一律冲突：
// 即使过期标识对应的旧文件恰好也已损坏或被删除，也不能把这次过期请求
// 改报为文件错误。
func TestReplaceStaleExpectedConflictPrecedence(t *testing.T) {
	a, dir := newTestArchive(t)

	// 旧标识对应的文件已被删除。
	ids := saveN(t, a, "del", 1) // r1 r0，当前 r1
	deleteRecord(t, dir, ids[1])
	if _, err := a.Replace("del", baseWorld(t), ids[1]); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("过期标识即使文件已删也应冲突，得到 %T: %v", err, err)
	}

	// 旧标识对应的文件已损坏。
	cs := saveN(t, a, "cs", 2) // r2 r1 r0，当前 r2，用过期的 r1
	corruptChecksum(t, dir, cs[1])
	if _, err := a.Replace("cs", baseWorld(t), cs[1]); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("过期标识即使文件损坏也应冲突，得到 %T: %v", err, err)
	}

	// 空预期标识仍是冲突，且不触碰存档。
	pointer, err := a.readSlotPointerLocked("cs")
	if err != nil {
		t.Fatal(err)
	}
	before := recordCount(t, a)
	if _, err := a.Replace("cs", baseWorld(t), ""); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("空预期标识应冲突，得到 %T: %v", err, err)
	}
	p, _ := a.readSlotPointerLocked("cs")
	if p.Latest != pointer.Latest || recordCount(t, a) != before {
		t.Fatal("被冲突拒绝的覆盖不应写入任何内容")
	}
}

// TestReplaceMissingSlot 槽不存在继续报不存在。
func TestReplaceMissingSlot(t *testing.T) {
	a, _ := newTestArchive(t)
	if _, err := a.Replace("nope", baseWorld(t), "r00000000000000000000000000000000"); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("槽不存在应 NotFound，得到 %T: %v", err, err)
	}
}

// TestReplaceRejectedThenPreviewAndRecover 覆盖因当前记录缺失被拒绝后，槽
// 仍指向原标识、历史与记录文件原样；用户随后经恢复预览选中一份完好历史
// 记录时，仍可沿用正式确认恢复完成恢复——确认只比较当前标识，当前记录
// 已删除不妨碍从合法来源恢复。
func TestReplaceRejectedThenPreviewAndRecover(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	w := baseWorld(t)
	if _, err := w.Apply(Commit{Time: 9}); err != nil {
		t.Fatal(err)
	}

	// 当前记录 r2 被删除，普通覆盖被拒绝。
	deleteRecord(t, dir, ids[0])
	if _, err := a.Replace("s", w, ids[0]); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("当前记录缺失应拒绝覆盖: %v", err)
	}

	// 恢复预览仍以已删除的 r2 为当前标识，越过它选中完好的 r1。
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应可预览恢复: %v", err)
	}
	if pv.Current != ids[0] || pv.Source != ids[1] {
		t.Fatalf("预览错误: current=%s source=%s", pv.Current, pv.Source)
	}
	// 确认恢复只比较当前标识，当前记录已删除仍可确认。
	info, err := a.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("确认恢复不应受当前记录缺失影响: %v", err)
	}
	if info.Parent != ids[1] {
		t.Fatalf("新记录应以选中的完好记录 %s 为父，得到 %s", ids[1], info.Parent)
	}
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("恢复后应能读取新记录: %v", err)
	}
	if rec.ID != info.ID {
		t.Fatalf("槽应指向恢复记录 %s，得到 %s", info.ID, rec.ID)
	}
}

// TestReplaceNoVersionGate 普通覆盖不要求调用方提供可接受规则版本集合：
// 当前记录完好即可作为父，新记录保存的是传入世界的规则与状态，既不拒绝
// 外来版本，也不采用来源记录的规则或状态。
func TestReplaceNoVersionGate(t *testing.T) {
	a, _ := newTestArchive(t)
	r0 := saveBase(t, a, "s")

	// 直接提交一条 v9 规则（校验和正确、父记录存在、状态合法）作为当前
	// 记录，模拟其他兼容版本写入器的数据。
	v9 := baseWorld(t).Snapshot()
	v9.Rules.Version = "v9"
	cur := writeRawRecord(t, a, r0.ID, false, v9)
	updatePointer(t, a, "s", cur)

	// 用一个时间片推进过的 v1 世界覆盖：不带任何可接受版本集合即可成功，
	// 新记录内容完全来自传入世界。
	w := baseWorld(t)
	if _, err := w.Apply(Commit{Time: 6}); err != nil {
		t.Fatal(err)
	}
	info, err := a.Replace("s", w, cur)
	if err != nil {
		t.Fatalf("当前记录为其他规则版本时覆盖不应被拒绝: %v", err)
	}
	if info.Parent != cur || info.Version != "v1" {
		t.Fatalf("新记录应以 %s 为父并保存传入世界的 v1 规则: %+v", cur, info)
	}
	rec, err := a.Record("s", info.ID, []string{"v1", "v9"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.State.Time != 6 || rec.State.Rules.Version != "v1" {
		t.Fatalf("应保存传入世界的状态，而不是来源记录状态: %+v", rec.State)
	}
}

// TestReplaceIntactCurrentSucceeds 当前记录完好且标识匹配时，覆盖正常保存
// 传入世界，新记录以被覆盖记录为父并按保存次序前置进历史。
func TestReplaceIntactCurrentSucceeds(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	w := baseWorld(t)
	if _, err := w.Apply(Commit{Time: 7}); err != nil {
		t.Fatal(err)
	}
	info, err := a.Replace("s", w, ids[0])
	if err != nil {
		t.Fatalf("当前记录完好时覆盖应成功: %v", err)
	}
	if info.Parent != ids[0] {
		t.Fatalf("新记录父应为被覆盖记录 %s，得到 %s", ids[0], info.Parent)
	}
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil || rec.ID != info.ID || rec.State.Time != 7 {
		t.Fatalf("覆盖后槽状态错误: %+v err=%v", rec, err)
	}
	hist, err := a.History("s")
	if err != nil || len(hist) != 4 || hist[0].ID != info.ID {
		t.Fatalf("新记录应按保存次序排在历史最前: %+v err=%v", hist, err)
	}
}
