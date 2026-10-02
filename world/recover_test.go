package world

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// saveN 在 slot 上连续保存 n+1 条记录（首存 + n 次覆盖），每次把时间片
// 推进 i，返回从最新到最旧的记录标识。
func saveN(t *testing.T, a *Archive, slot string, n int) []RecordID {
	t.Helper()
	w := baseWorld(t)
	info, err := a.Save(slot, w)
	if err != nil {
		t.Fatal(err)
	}
	ids := []RecordID{info.ID}
	cur := info.ID
	for i := 1; i <= n; i++ {
		if _, err := w.Apply(Commit{Time: i}); err != nil {
			t.Fatal(err)
		}
		info, err = a.Replace(slot, w, cur)
		if err != nil {
			t.Fatal(err)
		}
		ids = append([]RecordID{info.ID}, ids...)
		cur = info.ID
	}
	return ids // 最新在前
}

// corruptChecksum 改写记录文件中的校验和但保留其余 JSON（父标识等仍可解析）。
func corruptChecksum(t *testing.T, dir string, id RecordID) {
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
	s = s[:start] + strings.Repeat("f", 64) + s[start+64:]
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

// truncateRecord 把记录文件截断成无法解析的内容。
func truncateRecord(t *testing.T, dir string, id RecordID) {
	t.Helper()
	if err := os.Truncate(recordPath(dir, id), 30); err != nil {
		t.Fatal(err)
	}
}

// deleteRecord 删除记录文件。
func deleteRecord(t *testing.T, dir string, id RecordID) {
	t.Helper()
	if err := os.Remove(recordPath(dir, id)); err != nil {
		t.Fatal(err)
	}
}

// setRecordField 对记录文件做一次字符串替换（用于篡改父标识/槽首标记），
// 不重算校验和。
func setRecordField(t *testing.T, dir string, id RecordID, old, neu string) {
	t.Helper()
	path := recordPath(dir, id)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Replace(string(raw), old, neu, 1)
	if got == string(raw) {
		t.Fatalf("记录 %s 中找不到待替换内容 %q", id, old)
	}
	if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRecoverAfterLatestDeleted 最新世界记录文件被删除后，仍应恢复到最近
// 一份校验通过的记录，删除最新记录后重新打开存档结果一致。
func TestRecoverAfterLatestDeleted(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // ids[0] 最新, ids[3] 首存
	deleteRecord(t, dir, ids[0])

	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("删除最新记录后应可恢复: %v", err)
	}
	if rec.ID != ids[1] {
		t.Fatalf("应恢复到最近可用记录 %s，得到 %s", ids[1], rec.ID)
	}

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec2, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("重开后应可恢复: %v", err)
	}
	if rec2.ID != ids[1] {
		t.Fatalf("重开后恢复结果错误: %s", rec2.ID)
	}
}

// TestRecoverAcrossTruncatedAndMultipleCorrupt 最新记录被截断成无法解析的
// 内容，且若干中间记录损坏，恢复仍应越过全部受损记录找到更早的完好记录，
// 不依赖能否从损坏文件读出父标识。
func TestRecoverAcrossTruncatedAndMultipleCorrupt(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 4) // r4..r0

	truncateRecord(t, dir, ids[0]) // 最新：无法解析
	corruptChecksum(t, dir, ids[1])
	corruptChecksum(t, dir, ids[2])
	// ids[3] 完好，ids[4]（首存）也完好；最近可用的是 ids[3]。

	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应越过截断与多条损坏记录恢复: %v", err)
	}
	if rec.ID != ids[3] {
		t.Fatalf("应恢复到 %s，得到 %s", ids[3], rec.ID)
	}
	// 返回内容完整保持该次保存：时间片为 1（ids[3] 对应第一次覆盖）。
	if rec.State.Time != 1 || rec.State.Seed != 42 {
		t.Fatalf("恢复内容与该次保存不符: %+v", rec.State)
	}
	if rec.State.Rules.Version != "v1" {
		t.Fatalf("规则版本错误: %s", rec.State.Rules.Version)
	}
	if c := rec.State.Characters; len(c) != 2 || c[0].ID != "hero" ||
		len(c[0].Items) != 1 || c[0].Items[0] != (CharacterItem{Item: "gold", Count: 1}) {
		t.Fatalf("角色/物品未完整保持: %+v", c)
	}
}

// TestRecoverTamperedParentDoesNotRedirect 损坏记录里的父标识被改成别的
// 值（包括另一条记录的标识），校验和必然失败；恢复次序由槽指针的历史
// 索引决定，不能被篡改的父标识引向别的记录，也不能遮住更早的可用记录。
func TestRecoverTamperedParentDoesNotRedirect(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0
	// 另存一条与本槽无关的记录，伪装成损坏记录里被改写出的“父”。
	w := baseWorld(t)
	other, err := a.Save("other", w)
	if err != nil {
		t.Fatal(err)
	}
	// 篡改最新与一条中间记录的父标识（校验和随之失败）。
	setRecordField(t, dir, ids[0], `"parent": "`+string(ids[1])+`"`,
		`"parent": "`+string(other.ID)+`"`)
	setRecordField(t, dir, ids[2], `"parent": "`+string(ids[3])+`"`,
		`"parent": "`+string("rdeadbeefdeadbeefdeadbeefdeadbeef")+`"`)

	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应按历史索引越过被篡改记录: %v", err)
	}
	if rec.ID != ids[1] {
		t.Fatalf("最近可用应为 %s，得到 %s", ids[1], rec.ID)
	}

	// 恢复绝不能把别的槽的记录当作本槽结果。
	rec2, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil || rec2.ID == other.ID {
		t.Fatalf("恢复被引向了别的槽: %+v err=%v", rec2, err)
	}
}

// TestRecoverTamperedSlotFirstDoesNotMask 中间记录的槽首标记被改成 true，
// 恢复也不能因此提前停止而遮住本槽更早的可用记录（遍历只看历史索引）。
func TestRecoverTamperedSlotFirstDoesNotMask(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0
	corruptChecksum(t, dir, ids[0])
	// 中间记录被改成“槽首记录”，且不重算校验和（受损）。
	setRecordField(t, dir, ids[1], `"slotFirst": false`, `"slotFirst": true`)
	corruptChecksum(t, dir, ids[1])
	// 最近可用应是 ids[2]，且不会因为伪造的槽首标记而漏掉它。
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("伪造槽首标记不应阻断恢复: %v", err)
	}
	if rec.ID != ids[2] {
		t.Fatalf("应恢复到 %s，得到 %s", ids[2], rec.ID)
	}
}

// TestRecoverOrderIsSaveOrderNotTimeSlice “最近”按该槽保存生效的次序，
// 与世界时间片大小无关：保存次序更新的记录时间片反而更小时，恢复次序
// 仍以保存次序为准。
func TestRecoverOrderIsSaveOrderNotTimeSlice(t *testing.T) {
	a, dir := newTestArchive(t)
	// 首存一个时间片为 5 的世界。
	w0, err := NewWorld(InitialData{
		Seed:       7,
		Rules:      baseRules(),
		Characters: []Character{{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w0.Apply(Commit{Time: 5}); err != nil {
		t.Fatal(err)
	}
	r0, err := a.Save("s", w0)
	if err != nil {
		t.Fatal(err)
	}
	// 覆盖一个时间片只有 1 的独立世界（保存次序更新，时间片更小）。
	w1, err := NewWorld(InitialData{
		Seed:       7,
		Rules:      baseRules(),
		Characters: []Character{{ID: "hero", Location: "yard"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w1.Apply(Commit{Time: 1}); err != nil {
		t.Fatal(err)
	}
	r1, err := a.Replace("s", w1, r0.ID)
	if err != nil {
		t.Fatal(err)
	}

	hist, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if hist[0].ID != r1.ID || hist[1].ID != r0.ID {
		t.Fatalf("历史次序应按保存次序: %+v", hist)
	}

	// 最新（时间片 1）损坏：应恢复到保存次序更早、但时间片更大的记录。
	corruptChecksum(t, dir, r1.ID)
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应按保存次序恢复: %v", err)
	}
	if rec.ID != r0.ID || rec.State.Time != 5 {
		t.Fatalf("应恢复时间片 5 的更早保存记录，得到 %+v", rec.State)
	}
}

// TestRecoverBranchBoundary 分支只在分支自身成功保存的记录中恢复，不把
// 来源槽记录算进候选；分支首条记录损坏也不越过分支边界，其他槽即使种子
// 与规则相同也不能替代。
func TestRecoverBranchBoundary(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	b0, err := a.Branch("s", ids[2], "b")
	if err != nil {
		t.Fatal(err)
	}
	// 分支再成功保存一条。
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

	// 分支历史只含分支自身的两条记录。
	bh, err := a.History("b")
	if err != nil {
		t.Fatal(err)
	}
	if len(bh) != 2 || bh[0].ID != b1.ID || bh[1].ID != b0.ID {
		t.Fatalf("分支历史越界: %+v", bh)
	}

	// 分支最新记录损坏：应恢复到分支自身更早的记录，而不是来源槽记录。
	corruptChecksum(t, dir, b1.ID)
	rec, err := a.RecoverLatest("b", []string{"v1"})
	if err != nil {
		t.Fatalf("应恢复到分支自身更早记录: %v", err)
	}
	if rec.ID != b0.ID {
		t.Fatalf("应恢复分支首记录 %s，得到 %s", b0.ID, rec.ID)
	}

	// 分支首条记录也损坏：分支全无可用记录，即使来源槽 s 种子规则相同
	// 也不能替代 -> ErrUnrecoverable。
	corruptChecksum(t, dir, b0.ID)
	_, err = a.RecoverLatest("b", []string{"v1"})
	if !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("分支无可用记录应 ErrUnrecoverable，得到 %T: %v", err, err)
	}
	// 来源槽依旧可独立恢复，证明两者互不算候选。
	srec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("来源槽应可独立恢复: %v", err)
	}
	if srec.ID != ids[0] {
		t.Fatalf("来源槽恢复结果错误: %s", srec.ID)
	}
}

// TestRecoverBranchOnlyRecordCorrupt 分支首条记录是其唯一记录时损坏，
// 必须返回不可恢复。
func TestRecoverBranchOnlyRecordCorrupt(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 1)
	b0, err := a.Branch("s", ids[1], "b")
	if err != nil {
		t.Fatal(err)
	}
	corruptChecksum(t, dir, b0.ID)
	if _, err := a.RecoverLatest("b", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("分支唯一记录损坏应不可恢复，得到 %T: %v", err, err)
	}
}

// TestRecoverNoAcceptableVersion 所有完好记录的版本都不被接受时，跳过且
// 不自动升级，返回 ErrUnrecoverable。
func TestRecoverNoAcceptableVersion(t *testing.T) {
	a, _ := newTestArchive(t)
	saveN(t, a, "s", 2)
	_, err := a.RecoverLatest("s", []string{"v9"})
	if !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("无版本可接受应 ErrUnrecoverable，得到 %T: %v", err, err)
	}
}

// TestRecoverFirstSaveCorrupt 首存记录是槽内唯一记录时损坏，不可恢复。
func TestRecoverFirstSaveCorrupt(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	corruptChecksum(t, dir, r0.ID)
	if _, err := a.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("唯一首存损坏应不可恢复，得到 %T: %v", err, err)
	}
}

// TestRecoverResidualAndOrphanNotSelected 只有记录文件写完、尚未完成保存
// 的残留文件（.tmp）以及写完但指针未提交的孤儿记录都不能被选中。
func TestRecoverResidualAndOrphanNotSelected(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{Time: 1}); err != nil {
		t.Fatal(err)
	}
	// 孤儿记录：文件完整，但槽指针没有提交到它。
	orphan := writeRawRecord(t, a, r0.ID, false, w.Snapshot())
	// 残留临时文件：无法解析。
	junk := filepath.Join(dir, recordsName, string(r0.ID)+".tmp")
	if err := os.WriteFile(junk, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ { // 重复读取不消耗历史
		rec, err := a.RecoverLatest("s", []string{"v1"})
		if err != nil {
			t.Fatalf("残留/孤儿不应影响恢复: %v", err)
		}
		if rec.ID != r0.ID {
			t.Fatalf("残留或孤儿记录被选中: 得到 %s，应为 %s（孤儿 %s）", rec.ID, r0.ID, orphan)
		}
	}
}

// TestRecoverUpgradeChain 升级产生的记录同样支持恢复：升级后最新记录损坏
// 时，只接受旧版本的恢复读取找到升级前记录；新旧版本都接受时找到升级后
// 之前最近的完好记录。
func TestRecoverUpgradeChain(t *testing.T) {
	a, dir := newTestArchive(t)
	info := saveBase(t, a, "s")
	res, err := a.Upgrade("s", []string{"v1"}, v2Rules(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	up := res.Record.ID
	corruptChecksum(t, dir, up)

	old, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应恢复升级前记录: %v", err)
	}
	if old.ID != info.ID {
		t.Fatalf("应恢复 %s，得到 %s", info.ID, old.ID)
	}
	// 升级记录损坏后，即便接受 v2 也没有 v2 的完好记录 -> 不可恢复，
	// 不会把 v1 记录升级后返回。
	if _, err := a.RecoverLatest("s", []string{"v2"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("接受 v2 但 v2 记录损坏时应不可恢复，得到 %T: %v", err, err)
	}
}

// TestRecoverDoesNotMutate 恢复读取不修改目录内容：槽指针文件字节、records
// 目录条目集合在多次恢复前后保持一致；不修补损坏记录、不改槽当前记录。
func TestRecoverDoesNotMutate(t *testing.T) {
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
	if _, err := a.RecoverLatest("s", []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RecoverLatest("s", []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	ptrAfter, filesAfter := snapshot()

	if ptrBefore != ptrAfter {
		t.Fatal("恢复读取改写了槽指针")
	}
	if len(filesBefore) != len(filesAfter) {
		t.Fatalf("恢复读取改变了 records 目录条目数: %d -> %d", len(filesBefore), len(filesAfter))
	}
	for name, size := range filesBefore {
		if filesAfter[name] != size {
			t.Fatalf("恢复读取改动了文件 %s: %d -> %d", name, size, filesAfter[name])
		}
	}
	// 槽当前记录仍是损坏的最新记录，Latest 依旧报损坏。
	if _, err := a.Latest("s", []string{"v1"}); err == nil {
		t.Fatal("恢复读取不应修补槽当前记录")
	}
}

// TestRecoverCASLoserNotCandidate 并发覆盖同一父记录时竞争失败的写入不
// 得进入恢复候选：恰好一个成功，失败者连记录文件都不会留下；胜出记录
// 损坏后，恢复只能落到被覆盖的父记录。
func TestRecoverCASLoserNotCandidate(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{Time: 1}); err != nil {
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
			_, err = ga.Replace("s", w, r0.ID)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case isConflict(err):
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

	latest, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if latest.Parent != r0.ID {
		t.Fatalf("胜出记录父应为 %s，得到 %s", r0.ID, latest.Parent)
	}
	// 历史只含胜出记录与父记录，竞争失败的写入不在其中。
	hist, err := a.History("s")
	if err != nil || len(hist) != 2 {
		t.Fatalf("历史应只有胜出记录与父记录: %+v err=%v", hist, err)
	}

	// 胜出记录损坏：恢复只能落到父记录，绝不会选中任何竞争失败的写入。
	corruptChecksum(t, dir, latest.ID)
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应恢复到被覆盖的父记录: %v", err)
	}
	if rec.ID != r0.ID {
		t.Fatalf("应恢复父记录 %s，得到 %s", r0.ID, rec.ID)
	}
}

// TestRecoverReopenAfterInterruptedWrite 写入中断后重新打开，只能看到上一
// 次保存完整生效的状态；损坏最新提交记录后，恢复采用的历史次序与之一致，
// 不会选中写完但未提交的孤儿记录。
func TestRecoverReopenAfterInterruptedWrite(t *testing.T) {
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	r0, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{Time: 1}); err != nil {
		t.Fatal(err)
	}
	// 新记录已写完但指针尚未提交（崩溃现场），外加一个残留临时文件。
	orphan := writeRawRecord(t, a, r0.ID, false, w.Snapshot())
	if err := os.WriteFile(filepath.Join(dir, recordsName, "r00000000000000000000000000000000.tmp"),
		[]byte("{x"), 0o600); err != nil {
		t.Fatal(err)
	}

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 指针仍指向上一次完整生效的 r0；把它保持完好，恢复应返回 r0。
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("重开后应恢复上次完整记录: %v", err)
	}
	if rec.ID != r0.ID {
		t.Fatalf("选中了未提交的孤儿记录 %s 或其它: 得到 %s", orphan, rec.ID)
	}

	// 真正提交一条覆盖（r1），随后损坏 r1；恢复仍应越过它回到 r0，
	// 而不是夹在中间的孤儿记录。
	latest, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{Time: 2}); err != nil {
		t.Fatal(err)
	}
	r1, err := a2.Replace("s", w, latest.ID)
	if err != nil {
		t.Fatal(err)
	}
	corruptChecksum(t, dir, r1.ID)
	rec2, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应越过损坏的新提交恢复: %v", err)
	}
	if rec2.ID != r0.ID {
		t.Fatalf("应恢复 %s，得到 %s（孤儿 %s 不应入选）", r0.ID, rec2.ID, orphan)
	}
}

// 模拟已有存档目录中由旧版本写出的槽文件。
func writeLegacyPointer(t *testing.T, a *Archive, slot string, id RecordID) {
	t.Helper()
	if err := a.replaceSlotPointer(slot, id); err != nil {
		t.Fatal(err)
	}
}

// TestRecoverLegacyArchiveOpensAndRecovers 已有（旧格式）存档目录应能继续
// 打开和读取，无需转换数据；裸指针下恢复仍沿校验通过的父链工作，且读取
// 不把指针升级成新格式。
func TestRecoverLegacyArchiveOpensAndRecovers(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	// 把指针降级成旧版本裸指针；记录文件全部完好。
	writeLegacyPointer(t, a, "s", ids[0])

	a2, err := Open(dir)
	if err != nil {
		t.Fatalf("旧格式存档应能打开: %v", err)
	}
	ptrBytes, err := os.ReadFile(a2.slotPath("s"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("裸指针下应可恢复: %v", err)
	}
	if rec.ID != ids[0] {
		t.Fatalf("应恢复最新完好记录 %s，得到 %s", ids[0], rec.ID)
	}
	// 读取不改写指针。
	after, err := os.ReadFile(a2.slotPath("s"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(ptrBytes) {
		t.Fatal("恢复读取把旧指针改写成了新格式")
	}

	// 损坏最新记录后，旧指针没有历史索引，而连接更老记录的唯一父指针
	// 位于这份已不可信的损坏文件内，无法据此确认更老记录仍属于本槽；
	// 也不能扫描共享的 records 目录（会有把别的槽记录拉进来的风险）。
	// 按规格这属于“旧历史断裂且无法确认归属”，明确报不可恢复；旧历史的
	// 完整恢复能力由第一次成功覆盖/升级后获得（见另一个测试）。
	corruptChecksum(t, dir, ids[0])
	if _, err := a2.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("裸指针且最新记录损坏、无法确认更老记录归属时应不可恢复，得到 %T: %v", err, err)
	}
}

// TestRecoverLegacyFirstWriteGainsFullRecovery 旧槽历史原本完整时，第一次
// 成功覆盖后，原有历史与新增记录都获得基于保存次序的恢复能力。
func TestRecoverLegacyFirstWriteGainsFullRecovery(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0（新格式指针）
	writeLegacyPointer(t, a, "s", ids[0])

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 第一次成功覆盖：内存中重建的完整父链与新记录一起被持久化。
	latest, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := WorldFromState(latest.State)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{Time: 3}); err != nil {
		t.Fatal(err)
	}
	r3, err := a2.Replace("s", w, latest.ID)
	if err != nil {
		t.Fatal(err)
	}

	p, err := a2.readSlotPointerLocked("s")
	if err != nil {
		t.Fatal(err)
	}
	want := []RecordID{r3.ID, ids[0], ids[1], ids[2]}
	if len(p.History) != len(want) {
		t.Fatalf("覆盖后指针应持有完整历史 %v，得到 %v", want, p.History)
	}
	for i := range want {
		if p.History[i] != want[i] {
			t.Fatalf("历史次序错误: %v", p.History)
		}
	}

	// 新记录与中间记录同时损坏/删除，仍能恢复到旧历史里的首存记录。
	corruptChecksum(t, dir, r3.ID)
	deleteRecord(t, dir, ids[0])
	truncateRecord(t, dir, ids[1])
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("旧历史获得恢复能力后应找到首存记录: %v", err)
	}
	if rec.ID != ids[2] {
		t.Fatalf("应恢复首存记录 %s，得到 %s", ids[2], rec.ID)
	}
}

// TestRecoverLegacyBrokenHistory 旧历史已经断裂且无法确认归属时，只使用
// 仍能确认属于该槽的记录；没有可用结果时明确报不可恢复。
func TestRecoverLegacyBrokenHistory(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 -> r1 -> r0
	// 删除中间记录 r1，使 r2 的父关系无法验证；指针降级为旧格式。
	deleteRecord(t, dir, ids[1])
	writeLegacyPointer(t, a, "s", ids[0])

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// r2 因父记录缺失无法确认归属，r0 无法经由断裂链确认属于本槽：
	// 没有可用结果。
	if _, err := a2.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("断裂且无法确认归属的旧历史应不可恢复，得到 %T: %v", err, err)
	}
}

// TestRecoverDeletedMiddleBreaksTipParent 中间记录被删除时，最新记录自身
// 内容完好但父记录关系不再成立，按已有校验要求它也算受损；恢复仍应越过
// 它与缺失项，落到父记录仍在、自身完好的更早记录。
func TestRecoverDeletedMiddleBreaksTipParent(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3)   // r3 r2 r1 r0
	deleteRecord(t, dir, ids[1]) // 删除 r2：r3 的父记录关系失效

	// 直接读最新记录应报损坏。
	if _, err := a.Latest("s", []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("最新记录父关系失效应报损坏，得到 %T: %v", err, err)
	}
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应越过父关系失效的最新记录恢复: %v", err)
	}
	if rec.ID != ids[2] {
		t.Fatalf("应恢复父记录仍在的 r1 %s，得到 %s", ids[2], rec.ID)
	}
}

// TestRecoverMissingAndCorruptPointer 槽不存在仍报不存在；槽指针不可读
// 仍报损坏，本次恢复不处理这两种情况。
func TestRecoverMissingAndCorruptPointer(t *testing.T) {
	a, dir := newTestArchive(t)
	if _, err := a.RecoverLatest("nope", []string{"v1"}); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("槽不存在应 NotFound，得到 %T: %v", err, err)
	}
	w := baseWorld(t)
	if _, err := a.Save("s", w); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, slotsName, "s.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RecoverLatest("s", []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("槽指针不可读应 Corrupt，得到 %T: %v", err, err)
	}
}
