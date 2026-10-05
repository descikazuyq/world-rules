package world

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// rewriteRecordFormat 把已有记录的格式编号改写为给定值并重算内容校验和，
// 模拟一份可正常解析、校验和匹配、世界状态合法但格式编号不受支持的记录。
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

// dropRecordFormatField 删除记录文件中的 format 字段（其余内容保留），
// 并按缺失（即零值）重算校验和，模拟一条没有格式编号的记录。
func dropRecordFormatField(t *testing.T, a *Archive, id RecordID) {
	t.Helper()
	path := recordPath(a.dir, id)
	env, err := loadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	env.Format = 0
	env.Checksum = computeChecksum(env)
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "format")
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// assertUnsupportedFormatCorrupt 断言 err 是保留了被拒绝记录标识、且说明
// 格式编号不受支持的 *CorruptError（而不是版本拒绝或其它错误）。
func assertUnsupportedFormatCorrupt(t *testing.T, err error, id RecordID, format int) {
	t.Helper()
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("应为 *CorruptError，得到 %T: %v", err, err)
	}
	if ce.Record != id {
		t.Fatalf("CorruptError 应保留被拒绝的记录标识 %s，得到 %q", id, ce.Record)
	}
	if !strings.Contains(ce.Error(), "格式") || !strings.Contains(ce.Error(), fmt.Sprintf("%d", format)) {
		t.Fatalf("错误应指出不支持的格式编号 %d: %v", format, ce)
	}
	if errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("格式问题不应报成版本拒绝: %v", err)
	}
}

// TestLatestAndRecordUnsupportedFormatCorrupt 校验和匹配、世界状态合法、
// 规则版本也在可接受集合内，但格式编号不是 1 的记录仍属损坏：Latest 与
// Record 都返回保留记录标识并指出格式编号的 *CorruptError，不按版本拒绝
// 处理；读取保持只读，不修补这份记录。
func TestLatestAndRecordUnsupportedFormatCorrupt(t *testing.T) {
	for _, format := range []int{2, 0, -1, 7} {
		t.Run(fmt.Sprintf("format=%d", format), func(t *testing.T) {
			a, _ := newTestArchive(t)
			ids := saveN(t, a, "s", 1) // r1 r0
			rewriteRecordFormat(t, a, ids[0], format)
			before := recordFileBytes(t, a, ids[0])

			_, err := a.Latest("s", []string{"v1"})
			assertUnsupportedFormatCorrupt(t, err, ids[0], format)

			_, err = a.Record("s", ids[0], []string{"v1"})
			assertUnsupportedFormatCorrupt(t, err, ids[0], format)

			// 记录没有被悄悄修补或删除，槽指向不变。
			if got := recordFileBytes(t, a, ids[0]); string(got) != string(before) {
				t.Fatal("读取改写了未知格式记录")
			}
			if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != ids[0] {
				t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
			}

			// 本槽格式 1 的完好记录不受影响，仍可正常读取。
			if _, err := a.Record("s", ids[1], []string{"v1"}); err != nil {
				t.Fatalf("格式 1 的历史记录应可读: %v", err)
			}
		})
	}
}

// TestMissingFormatFieldRejected 记录文件缺少 format 字段时按零值处理，
// 即使校验和（按零值重算）匹配、世界状态合法也拒绝读取。
func TestMissingFormatFieldRejected(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveBase(t, a, "s")
	dropRecordFormatField(t, a, info.ID)

	_, err := a.Latest("s", []string{"v1"})
	assertUnsupportedFormatCorrupt(t, err, info.ID, 0)
}

// TestFormatCheckIndependentOfVersion 格式编号与规则版本分别判断：格式 1
// 的完好记录仍按版本集合决定能否读取；格式不受支持的记录即使版本不被
// 接受也报损坏而非版本拒绝，且读取不改写记录中的规则。
func TestFormatCheckIndependentOfVersion(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1)

	// 格式 1 完好记录：版本不在集合内仍是版本拒绝。
	if _, err := a.Latest("s", []string{"other"}); !errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("格式 1 完好记录应仍按版本集合判断，得到 %T: %v", err, err)
	}

	// 格式不受支持且版本也不被接受：报损坏，不报版本拒绝。
	rewriteRecordFormat(t, a, ids[0], 2)
	_, err := a.Latest("s", []string{"other"})
	assertUnsupportedFormatCorrupt(t, err, ids[0], 2)

	// 记录中的规则保持原样，未被读取改写。
	env, err := loadRecord(recordPath(a.dir, ids[0]))
	if err != nil {
		t.Fatal(err)
	}
	if env.State.Rules.Version != "v1" || env.Format != 2 {
		t.Fatalf("读取不应改写记录: format=%d version=%s", env.Format, env.State.Rules.Version)
	}
}

// TestRecoverSkipsUnsupportedFormat 恢复读取与恢复预览按本槽保存次序越过
// 格式不受支持的记录；连续多份未知格式记录也不会遮住其后格式 1 的完好
// 记录。预览的当前标识仍是槽实际指向的记录，来源是选中的完好记录；恢复
// 不修补格式、不删除文件、不改变槽指向。
func TestRecoverSkipsUnsupportedFormat(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2(time 2) r1(time 1) r0(time 0)
	rewriteRecordFormat(t, a, ids[0], 2)
	rewriteRecordFormat(t, a, ids[1], 3)

	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应越过未知格式记录恢复: %v", err)
	}
	if rec.ID != ids[2] || rec.State.Time != 0 {
		t.Fatalf("应恢复 %s（时间片 0），得到 %s（时间片 %d）", ids[2], rec.ID, rec.State.Time)
	}

	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("预览应越过未知格式记录: %v", err)
	}
	if pv.Current != ids[0] {
		t.Fatalf("预览当前标识应是槽实际指向的 %s，得到 %s", ids[0], pv.Current)
	}
	if pv.Source != ids[2] {
		t.Fatalf("预览来源应是 %s，得到 %s", ids[2], pv.Source)
	}

	// 未知格式记录没有被修补或删除，槽指向不变，重复恢复结果一致。
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != ids[0] {
		t.Fatalf("恢复不应改变槽指向: %+v err=%v", p, err)
	}
	if _, err := a.Latest("s", []string{"v1"}); err == nil {
		t.Fatal("恢复/预览不应修补未知格式记录")
	}
	rec2, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil || rec2.ID != ids[2] {
		t.Fatalf("重复恢复结果应一致: %+v err=%v", rec2, err)
	}
}

// TestRecoverAllUnsupportedFormatUnrecoverable 本槽没有任何格式受支持的
// 完好记录时，恢复读取与恢复预览都返回可由 errors.Is 判断的
// ErrUnrecoverable；分支槽只用自己的历史，不跨入来源槽。
func TestRecoverAllUnsupportedFormatUnrecoverable(t *testing.T) {
	a, _ := newTestArchive(t)
	src := saveBase(t, a, "src")
	if _, err := a.Branch("src", src.ID, "b"); err != nil {
		t.Fatal(err)
	}
	p, err := a.readSlotPointerLocked("b")
	if err != nil {
		t.Fatal(err)
	}
	rewriteRecordFormat(t, a, p.Latest, 2)

	// 分支唯一记录格式不受支持：不可恢复，且不会回退到来源槽的完好记录。
	if _, err := a.RecoverLatest("b", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("分支不应跨入来源槽，得到 %T: %v", err, err)
	}
	if _, err := a.PreviewRecovery("b", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("预览同样应不可恢复，得到 %T: %v", err, err)
	}

	// 来源槽自己的完好记录不受影响。
	if _, err := a.RecoverLatest("src", []string{"v1"}); err != nil {
		t.Fatalf("来源槽应可正常恢复: %v", err)
	}
}

// TestUnsupportedFormatLegacyPointerHistory 旧格式裸指针沿父链重建历史时，
// 未知格式记录同样校验不过：重建停在那里，不依靠未知格式记录中的父标识
// 继续查找，无法确认归属的更老记录不进入恢复候选。
func TestUnsupportedFormatLegacyPointerHistory(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0
	rewriteRecordFormat(t, a, ids[2], 2)
	writeLegacyPointer(t, a, "s", ids[0])

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 最新与次新完好：恢复仍命中最新记录。
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("未知格式记录之前的完好记录应可恢复: %v", err)
	}
	if rec.ID != ids[0] {
		t.Fatalf("应恢复最新记录 %s，得到 %s", ids[0], rec.ID)
	}

	// 最新与次新也损坏后，链在未知格式记录处断裂，首存记录无法确认归属。
	corruptChecksum(t, dir, ids[0])
	corruptChecksum(t, dir, ids[1])
	if _, err := a2.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("断裂处更老的记录不应进入候选，得到 %T: %v", err, err)
	}
}

// TestSaveSourcesUnsupportedFormatRejected 把未知格式记录用作保存来源时
// 一律按损坏拒绝：分支、覆盖、升级、迁移与确认恢复都不产生新记录，不改变
// 槽指向或已有历史，也不把未知格式内容重新保存成格式 1。
func TestSaveSourcesUnsupportedFormatRejected(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	rewriteRecordFormat(t, a, ids[0], 2)
	before := recordCount(t, a)

	// 分支：来源未知格式。
	_, err := a.Branch("s", ids[0], "b")
	assertUnsupportedFormatCorrupt(t, err, ids[0], 2)
	if _, err := a.readLatestLocked("b"); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("不应创建目标槽，得到 %v", err)
	}

	// 覆盖：当前记录未知格式。
	rec, err := a.Record("s", ids[1], []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{Time: 9}); err != nil {
		t.Fatal(err)
	}
	_, err = a.Replace("s", w, ids[0])
	assertUnsupportedFormatCorrupt(t, err, ids[0], 2)

	// 升级：检查与正式升级都拒绝。
	_, err = a.CheckUpgrade("s", []string{"v1"}, v2Rules())
	assertUnsupportedFormatCorrupt(t, err, ids[0], 2)
	_, err = a.Upgrade("s", []string{"v1"}, v2Rules(), ids[0])
	assertUnsupportedFormatCorrupt(t, err, ids[0], 2)

	// 迁移：预览与正式迁移都拒绝。
	_, err = a.PreviewMigration("s", []string{"v1"}, v2Rules(), nil, nil)
	assertUnsupportedFormatCorrupt(t, err, ids[0], 2)
	_, err = a.Migrate("s", []string{"v1"}, v2Rules(), nil, nil, ids[0])
	assertUnsupportedFormatCorrupt(t, err, ids[0], 2)

	// 确认恢复：来源未知格式。
	_, err = a.ConfirmRecovery("s", ids[0], ids[0], []string{"v1"})
	assertUnsupportedFormatCorrupt(t, err, ids[0], 2)

	// 全部拒绝：不产生新记录，槽指向与历史保持原样。
	if got := recordCount(t, a); got != before {
		t.Fatalf("被拒绝的保存不应产生新记录: %d -> %d", before, got)
	}
	p, err := a.readSlotPointerLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	if p.Latest != ids[0] {
		t.Fatalf("槽指向不应改变: %s -> %s", ids[0], p.Latest)
	}
	if len(p.History) != 3 {
		t.Fatalf("历史不应改变: %v", p.History)
	}

	// 本槽格式 1 的完好记录仍可正常作为分支来源。
	if _, err := a.Branch("s", ids[1], "ok"); err != nil {
		t.Fatalf("格式 1 的来源应可分支: %v", err)
	}
}

// TestConfirmRecoveryFromFormat1WhenCurrentUnsupported 只有当前记录格式
// 不受支持时，仍可按已有恢复流程从本槽另一份格式 1 的完好记录确认恢复：
// 预览选中完好的旧记录，确认后产生格式 1 的新记录并指向它。
func TestConfirmRecoveryFromFormat1WhenCurrentUnsupported(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1) // r1(time 1) r0(time 0)
	rewriteRecordFormat(t, a, ids[0], 2)

	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("预览应选中格式 1 的旧记录: %v", err)
	}
	if pv.Current != ids[0] || pv.Source != ids[1] {
		t.Fatalf("预览应为 current=%s source=%s，得到 current=%s source=%s",
			ids[0], ids[1], pv.Current, pv.Source)
	}

	info, err := a.ConfirmRecovery("s", pv.Current, pv.Source, []string{"v1"})
	if err != nil {
		t.Fatalf("从格式 1 的完好记录确认恢复应成功: %v", err)
	}
	if info.Parent != ids[1] {
		t.Fatalf("新记录应以来源为父: %s", info.Parent)
	}

	// 新记录是格式 1 的完好记录，槽指向它，旧历史原样保留。
	env, err := loadRecord(recordPath(a.dir, info.ID))
	if err != nil {
		t.Fatal(err)
	}
	if env.Format != recordFormatVersion {
		t.Fatalf("确认恢复产生的新记录应为格式 1，得到 %d", env.Format)
	}
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil || rec.ID != info.ID || rec.State.Time != 0 {
		t.Fatalf("恢复后最新记录应可读且内容来自来源: %+v err=%v", rec, err)
	}
	if _, err := a.Record("s", ids[1], []string{"v1"}); err != nil {
		t.Fatalf("来源记录应仍可读取: %v", err)
	}
}
