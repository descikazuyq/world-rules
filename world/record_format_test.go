package world

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
)

// rewriteRecordFormat 把已有记录的格式编号改写为给定值并重算内容校验和，
// 模拟一份可正常解析、校验和匹配、父记录存在且世界状态合法，但使用了
// 当前不支持的记录格式编号的存档。
func rewriteRecordFormat(t *testing.T, a *Archive, id RecordID, format int) {
	t.Helper()
	path := recordPath(a.dir, id)
	env, err := loadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	env.Format = format
	env.Checksum = computeChecksum(env)
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// rewriteRecordDropFormat 把记录文件中的 format 字段整体删除（其余字段与
// 校验和保持自洽，校验和按格式编号零值计算），模拟字段缺失的旧记录。
func rewriteRecordDropFormat(t *testing.T, a *Archive, id RecordID) {
	t.Helper()
	path := recordPath(a.dir, id)
	env, err := loadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	// 字段缺失经反序列化得到零值 0；校验和也按 0 计算，保证记录除格式
	// 编号外完全自洽。
	env.Format = 0
	env.Checksum = computeChecksum(env)
	var m map[string]any
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "format")
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// forgeFormatLatest 写入一条使用指定格式编号（其余内容完好、校验和匹配、
// 父记录为槽当前最新记录）的记录，并按正常保存路径提交为槽的最新记录。
func forgeFormatLatest(t *testing.T, a *Archive, slot string, format int) RecordID {
	t.Helper()
	p, err := a.readSlotPointerLocked(slot)
	if err != nil {
		t.Fatal(err)
	}
	id, err := newRecordID()
	if err != nil {
		t.Fatal(err)
	}
	env := &envelope{
		Format:    format,
		ID:        id,
		Parent:    p.Latest,
		SlotFirst: false,
		State:     baseWorld(t).Snapshot(),
	}
	env.Checksum = computeChecksum(env)
	if err := writeRecord(a.dir, env); err != nil {
		t.Fatal(err)
	}
	updatePointer(t, a, slot, id)
	return id
}

// assertUnsupportedFormatCorrupt 断言 err 是保留被拒绝记录标识、并指出
// 不支持格式编号的 *CorruptError，而不是版本拒绝或其它错误。
func assertUnsupportedFormatCorrupt(t *testing.T, err error, id RecordID, format int) {
	t.Helper()
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("应为 *CorruptError，得到 %T: %v", err, err)
	}
	if ce.Record != id {
		t.Fatalf("CorruptError 应保留被拒绝的记录标识 %s，得到 %q", id, ce.Record)
	}
	if !strings.Contains(ce.Error(), "不支持的格式编号") {
		t.Fatalf("错误应指出不支持的格式编号: %v", ce)
	}
	if !strings.Contains(ce.Error(), strconv.Itoa(format)) {
		t.Fatalf("错误应给出不支持的格式编号 %d: %v", format, ce)
	}
	var ve *VersionRejectedError
	if errors.As(err, &ve) {
		t.Fatalf("格式问题不应报成版本拒绝: %v", err)
	}
}

// TestLatestAndRecordUnsupportedFormatCorrupt 记录可解析、校验和匹配、
// 父记录存在、世界状态合法、规则版本也在可接受集合内，但 format 不是
// 当前唯一支持的 1 时，Latest 与 Record 都返回保留记录标识并指出格式
// 编号的 *CorruptError；字段缺失（零值）、零、负值与其他正整数都拒绝，
// 读取保持只读，本槽更早的格式 1 记录不受影响。
func TestLatestAndRecordUnsupportedFormatCorrupt(t *testing.T) {
	cases := []struct {
		name   string
		format int
		drop   bool
	}{
		{"编号 2", 2, false},
		{"其他正整数 3", 3, false},
		{"编号 0", 0, false},
		{"负值 -1", -1, false},
		{"字段缺失", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			ids := saveN(t, a, "s", 2) // r2 r1 r0
			if tc.drop {
				rewriteRecordDropFormat(t, a, ids[0])
			} else {
				rewriteRecordFormat(t, a, ids[0], tc.format)
			}
			before := recordFileBytes(t, a, ids[0])

			_, err := a.Latest("s", []string{"v1"})
			assertUnsupportedFormatCorrupt(t, err, ids[0], tc.format)

			_, err = a.Record("s", ids[0], []string{"v1"})
			assertUnsupportedFormatCorrupt(t, err, ids[0], tc.format)

			// 重复读取仍拒绝，且记录文件字节保持原样：读取不修补格式。
			if _, err := a.Latest("s", []string{"v1"}); err == nil {
				t.Fatal("未知格式记录不应被读取修补")
			}
			if got := recordFileBytes(t, a, ids[0]); string(got) != string(before) {
				t.Fatal("读取改写了未知格式记录")
			}

			// 本槽更早的格式 1 记录仍可正常读取，版本集合判断不受影响。
			rec, err := a.Record("s", ids[1], []string{"v1"})
			if err != nil {
				t.Fatalf("格式 1 的历史记录应可读: %v", err)
			}
			if rec.ID != ids[1] {
				t.Fatalf("读回了错误的记录: %s", rec.ID)
			}
		})
	}
}

// TestUnsupportedFormatDistinctFromVersion 格式编号与世界规则版本分别
// 判断：格式 2 的记录即使规则版本在可接受集合内也按损坏拒绝；格式 1、
// 但规则版本不在集合内的记录仍按 VersionRejectedError 报告，不把两类
// 问题混淆。
func TestUnsupportedFormatDistinctFromVersion(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1) // r1 r0

	rewriteRecordFormat(t, a, ids[0], 2)
	var ce *CorruptError
	if _, err := a.Latest("s", []string{"v1"}); !errors.As(err, &ce) {
		t.Fatalf("格式 2 即使版本可接受也应报损坏，得到 %T: %v", err, err)
	}

	// 恢复成格式 1，但只接受别的规则版本：应报版本拒绝而非损坏。
	rewriteRecordFormat(t, a, ids[0], recordFormatVersion)
	var ve *VersionRejectedError
	if _, err := a.Latest("s", []string{"v9"}); !errors.As(err, &ve) {
		t.Fatalf("格式 1 但版本不被接受应报版本拒绝，得到 %T: %v", err, err)
	}
}

// TestRecoverSkipsUnsupportedFormat 恢复读取与恢复预览越过不支持格式的
// 记录，按本槽保存次序选中最近一份格式 1、校验通过且版本可接受的记录；
// 连续多份未知格式也不能遮住后面的合法记录；预览当前标识仍是槽实际
// 指向的记录。全部记录格式都不受支持时仍返回 ErrUnrecoverable。
func TestRecoverSkipsUnsupportedFormat(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0

	bad1 := forgeFormatLatest(t, a, "s", 2)
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应越过格式 2 记录恢复: %v", err)
	}
	if rec.ID != ids[0] {
		t.Fatalf("应恢复 %s，得到 %s", ids[0], rec.ID)
	}
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("预览应越过格式 2 记录: %v", err)
	}
	if pv.Current != bad1 || pv.Source != ids[0] {
		t.Fatalf("预览 current/source 错误: %s/%s", pv.Current, pv.Source)
	}

	// 连续第二份未知格式（不同编号）仍不能遮住后面的合法记录。
	rewriteRecordFormat(t, a, ids[0], 7)
	rec, err = a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应连续越过未知格式记录恢复: %v", err)
	}
	if rec.ID != ids[1] {
		t.Fatalf("应恢复 %s，得到 %s", ids[1], rec.ID)
	}
	pv, err = a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("预览同样应越过连续未知格式: %v", err)
	}
	if pv.Current != bad1 || pv.Source != ids[1] {
		t.Fatalf("预览 current/source 错误: %s/%s", pv.Current, pv.Source)
	}

	// 恢复结果完整来自选中记录，槽指向与未知格式文件都不被改动。
	if rec.State.Time != 2 {
		t.Fatalf("恢复结果应完整来自选中记录（时间片 2），得到 %d", rec.State.Time)
	}
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != bad1 {
		t.Fatalf("恢复不应改变槽指向: %+v err=%v", p, err)
	}

	// 剩余记录全部改成未知格式后不可恢复。
	for _, id := range ids[1:] {
		rewriteRecordFormat(t, a, id, 2)
	}
	if _, err := a.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("没有格式受支持的记录应不可恢复，得到 %T: %v", err, err)
	}
	if _, err := a.PreviewRecovery("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("预览同样应不可恢复，得到 %T: %v", err, err)
	}
}

// TestRecoverLegacyPointerUnsupportedFormat 旧裸指针没有历史索引时保留
// 既有归属限制：最新记录为未知格式时沿父链重建立即停止，不借助其中的
// 父标识继续查找；未知格式位于链中段时不影响它之前的完好记录，但会
// 阻断更老记录的归属确认。
func TestRecoverLegacyPointerUnsupportedFormat(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0

	// 最新记录直接是格式 2：即使文件内带着格式 1 父记录 r3 的标识，也
	// 不能沿它继续找回更早的记录。
	bad := forgeFormatLatest(t, a, "s", 2)
	writeLegacyPointer(t, a, "s", bad)
	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a2.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("裸指针指向未知格式记录时应不可恢复，得到 %T: %v", err, err)
	}
	if _, err := a2.PreviewRecovery("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("预览同样应不可恢复，得到 %T: %v", err, err)
	}

	// 未知格式位于链中段：之前的完好记录仍可恢复，但不能穿过它找到 r0。
	writeLegacyPointer(t, a, "s", ids[0])
	rewriteRecordFormat(t, a, ids[2], 2) // r1 改成格式 2
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("未知格式之前的完好记录应可恢复: %v", err)
	}
	if rec.ID != ids[0] {
		t.Fatalf("应恢复最新记录 %s，得到 %s", ids[0], rec.ID)
	}
	corruptChecksum(t, dir, ids[0])
	corruptChecksum(t, dir, ids[1])
	if _, err := a2.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("不应借助未知格式记录中的父标识找回 r0，得到 %T: %v", err, err)
	}
}

// TestUnsupportedFormatCannotBeResaved 把已有记录用作保存来源的各条路径
// 都遵守格式限制：分支、覆盖、升级、迁移与确认恢复不能把未知格式内容
// 重新保存成格式 1。拒绝时不产生新记录、不改变槽指向或已有历史；只有
// 当前记录格式不受支持时，仍可从本槽另一份格式 1 的完好记录确认恢复。
func TestUnsupportedFormatCannotBeResaved(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	bad := forgeFormatLatest(t, a, "s", 2)
	before := recordCount(t, a)
	w := baseWorld(t)

	// 覆盖：以未知格式的当前记录为父被拒绝。
	if _, err := a.Replace("s", w, bad); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("覆盖未知格式记录应报损坏，得到 %T: %v", err, err)
	}
	assertNoResaveSideEffects(t, a, "s", bad, before)

	// 分支：未知格式记录不能作为来源。
	if _, err := a.Branch("s", bad, "b"); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("从未知格式记录分支应报损坏，得到 %T: %v", err, err)
	}
	if _, err := a.readLatestLocked("b"); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("被拒绝的分支不应创建目标槽，得到 %v", err)
	}
	assertNoResaveSideEffects(t, a, "s", bad, before)

	// 升级检查与正式升级。
	if _, err := a.CheckUpgrade("s", []string{"v1"}, v2Rules()); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("升级检查未知格式记录应报损坏，得到 %T: %v", err, err)
	}
	if _, err := a.Upgrade("s", []string{"v1"}, v2Rules(), bad); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("升级未知格式记录应报损坏，得到 %T: %v", err, err)
	}
	assertNoResaveSideEffects(t, a, "s", bad, before)

	// 迁移预览与正式迁移。
	if _, err := a.PreviewMigration("s", []string{"v1"}, v2Rules(), nil, nil); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("迁移预览未知格式记录应报损坏，得到 %T: %v", err, err)
	}
	if _, err := a.Migrate("s", []string{"v1"}, v2Rules(), nil, nil, bad); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("迁移未知格式记录应报损坏，得到 %T: %v", err, err)
	}
	assertNoResaveSideEffects(t, a, "s", bad, before)

	// 确认恢复：显式以未知格式记录为来源同样拒绝，不重存其内容。
	if _, err := a.ConfirmRecovery("s", bad, bad, []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("确认恢复未知格式来源应报损坏，得到 %T: %v", err, err)
	}
	assertNoResaveSideEffects(t, a, "s", bad, before)

	// 当前记录格式不受支持时，仍可确认恢复到本槽另一份格式 1 的完好
	// 记录；新记录完整来自该格式 1 记录（时间片 2），此后槽恢复可读。
	info, err := a.ConfirmRecovery("s", bad, ids[0], []string{"v1"})
	if err != nil {
		t.Fatalf("从格式 1 完好记录确认恢复应成功: %v", err)
	}
	if info.Parent != ids[0] {
		t.Fatalf("新记录应以选中的格式 1 记录为父，得到 %s", info.Parent)
	}
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("确认恢复后槽应可正常读取: %v", err)
	}
	if rec.ID != info.ID || rec.State.Time != 2 {
		t.Fatalf("最新记录应是恢复出的新记录且状态完整来自来源，得到 %s 时间片 %d", rec.ID, rec.State.Time)
	}
	// 原未知格式记录没有被删除或修补，仍保留在历史中且仍按损坏拒绝。
	if _, err := a.Record("s", bad, []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("原未知格式记录应原样保留且仍被拒绝，得到 %T: %v", err, err)
	}
}

// assertNoResaveSideEffects 断言被拒绝的保存路径没有产生新记录、没有
// 改变槽指向（仍指向 bad），且未知格式记录文件没有被修补或删除。
func assertNoResaveSideEffects(t *testing.T, a *Archive, slot string, bad RecordID, before int) {
	t.Helper()
	if got := recordCount(t, a); got != before {
		t.Fatalf("被拒绝的操作不应产生新记录: %d -> %d", before, got)
	}
	p, err := a.readSlotPointerLocked(slot)
	if err != nil {
		t.Fatal(err)
	}
	if p.Latest != bad {
		t.Fatalf("槽指向不应改变: %s -> %s", bad, p.Latest)
	}
	if _, err := loadRecord(recordPath(a.dir, bad)); err != nil {
		t.Fatalf("未知格式记录文件不应被删除: %v", err)
	}
}

// TestFormatOneRecordsStillAccepted 正常写出的格式 1 记录行为不变。
func TestFormatOneRecordsStillAccepted(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2)

	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("格式 1 最新记录应可读: %v", err)
	}
	if rec.ID != ids[0] {
		t.Fatalf("应读到最新记录 %s，得到 %s", ids[0], rec.ID)
	}
	rec, err = a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("格式 1 记录应可恢复: %v", err)
	}
	if rec.ID != ids[0] {
		t.Fatalf("恢复应命中最新记录 %s，得到 %s", ids[0], rec.ID)
	}
}
