package world

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// historyIDs 提取历史元信息中的记录标识。
func historyIDs(t *testing.T, infos []RecordInfo) []RecordID {
	t.Helper()
	ids := make([]RecordID, len(infos))
	for i, info := range infos {
		ids[i] = info.ID
	}
	return ids
}

// assertHistoryIDs 断言历史元信息的标识序列与期望（保存生效次序）一致。
func assertHistoryIDs(t *testing.T, infos []RecordInfo, want []RecordID) {
	t.Helper()
	if got := historyIDs(t, infos); len(got) != len(want) {
		t.Fatalf("历史应为 %v，得到 %v", want, got)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("历史次序错误: %v，期望 %v", got, want)
			}
		}
	}
}

// rewriteRulesVersion 改写记录中的规则版本并重算校验和，模拟一份完好但
// 规则版本较旧（或调用方未必接受）的记录。
func rewriteRulesVersion(t *testing.T, a *Archive, id RecordID, version string) {
	t.Helper()
	path := recordPath(a.dir, id)
	env, err := loadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	env.State.Rules.Version = version
	env.Checksum = computeChecksum(env)
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestHistoryTamperedLatestRuleVersion 本槽连续保存三次（首存 + 两次覆盖）
// 后，仅把最新记录的规则版本改成另一个值而不更新校验和：该记录用 Record
// 读取会因校验失败被拒绝，History 也应略过它，只留下前两次保存的记录；
// 其余元信息字段与记录内容一致，次序不变。
func TestHistoryTamperedLatestRuleVersion(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0

	// 只改最新记录里的规则版本，不重算校验和。
	setRecordField(t, dir, ids[0], `"Version": "v1"`, `"Version": "v7"`)

	// 同一记录用 Record 读取按损坏拒绝，证明列表与读取采用同一完整性标准。
	if _, err := a.Record("s", ids[0], []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("被改动的记录应被 Record 拒绝，得到 %T: %v", err, err)
	}

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("最新记录损坏不应让整个列表失败: %v", err)
	}
	assertHistoryIDs(t, infos, []RecordID{ids[1], ids[2]})
	if infos[0].Version != "v1" || infos[1].Version != "v1" {
		t.Fatalf("完好记录的版本元信息应来自记录本身: %+v", infos)
	}
	if infos[0].Parent != ids[2] || infos[0].SlotFirst {
		t.Fatalf("中间记录元信息错误: %+v", infos[0])
	}
	if infos[1].Parent != "" || !infos[1].SlotFirst {
		t.Fatalf("槽首记录元信息错误: %+v", infos[1])
	}
}

// TestHistoryTamperedParentSkipped 中间记录的父标识被改动且不更新校验和时，
// 该记录被略过，最新记录（自身完好、直接父文件仍在）与更早记录继续保留。
func TestHistoryTamperedParentSkipped(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0

	setRecordField(t, dir, ids[2], `"parent": "`+string(ids[3])+`"`,
		`"parent": "r00000000000000000000000000000000"`)

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	assertHistoryIDs(t, infos, []RecordID{ids[0], ids[1], ids[3]})
}

// TestHistorySkipsMissingTruncatedAndChecksumBad 最新记录校验和损坏、中间
// 记录被写成无法解析的内容（文件仍在）、另一条被截断：逐条略过，其余合格
// 记录按原保存次序返回。损坏文件仍占着位置，不影响其直接子记录的父文件
// 存在性检查；父文件被删除导致的连带略过另见专门用例。
func TestHistorySkipsMissingTruncatedAndChecksumBad(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 4) // r4 r3 r2 r1 r0

	corruptChecksum(t, dir, ids[0])
	truncateRecord(t, dir, ids[2])
	if err := os.WriteFile(recordPath(dir, ids[3]), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	// r3(ids[1]) 的直接父 r2 文件仍在（只是内容截断），r3 自身完好，保留；
	// 槽首 r0(ids[4]) 保留。
	assertHistoryIDs(t, infos, []RecordID{ids[1], ids[4]})
}

// TestHistoryAllSkippedReturnsEmpty 槽存在且历史可确定、但所有记录都被略
// 过时，返回空列表和成功结果。
func TestHistoryAllSkippedReturnsEmpty(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	for _, id := range ids {
		corruptChecksum(t, dir, id)
	}

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("全部损坏也应成功返回: %v", err)
	}
	if infos == nil {
		t.Fatal("应返回非 nil 的空列表")
	}
	if len(infos) != 0 {
		t.Fatalf("应返回空列表，得到 %+v", infos)
	}
}

// TestHistoryIntegrityFailuresAllSkipped 即便文件可解析、校验和也匹配，
// 格式不受支持或时间片为负的记录仍不完整，History 一律略过。
func TestHistoryIntegrityFailuresAllSkipped(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0

	// 最新记录：格式编号不受支持（已重算校验和）。
	rewriteRecordFormat(t, a, ids[0], 2)
	// 中间记录：时间片为负（已重算校验和）。
	rewriteRecordTime(t, a, ids[1], -1)

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	assertHistoryIDs(t, infos, []RecordID{ids[2]})
}

// TestHistoryListsIntactOldRuleVersion 完整性与规则版本是否适合调用方使用
// 是两回事：History 没有可接受版本参数，完好但规则版本较旧（或陌生）的
// 记录仍正常列出，元信息中的版本与记录一致。
func TestHistoryListsIntactOldRuleVersion(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1) // r1 r0

	// 最新记录完好、校验和正确，只是规则版本对某些调用方不可接受。
	rewriteRulesVersion(t, a, ids[0], "v0-ancient")

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("完好的旧版本记录应正常列出: %+v", infos)
	}
	if infos[0].ID != ids[0] || infos[0].Version != "v0-ancient" {
		t.Fatalf("旧版本记录元信息错误: %+v", infos[0])
	}
}

// TestHistoryMissingSlotAndCorruptPointer 槽不存在继续返回不存在错误；槽
// 指针无法解析继续返回损坏错误，不把它们当成空历史。
func TestHistoryMissingSlotAndCorruptPointer(t *testing.T) {
	a, dir := newTestArchive(t)

	if _, err := a.History("nope"); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("槽不存在应返回 *NotFoundError，得到 %T: %v", err, err)
	}

	saveN(t, a, "s", 1)
	if err := os.WriteFile(filepath.Join(dir, slotsName, "s.json"), []byte("{xx"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.History("s"); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("槽指针无法解析应返回 *CorruptError，得到 %T: %v", err, err)
	}
}

// TestHistoryBranchListsOnlyOwnRecords 分支槽只列分支自身保存生效的记录，
// 不把来源槽的父记录补进列表，即使分支首记录以来源记录为直接父记录。
func TestHistoryBranchListsOnlyOwnRecords(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0

	b0, err := a.Branch("s", ids[1], "b")
	if err != nil {
		t.Fatal(err)
	}
	brec, err := a.Latest("b", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := WorldFromState(brec.State)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(Commit{Time: 9}); err != nil {
		t.Fatal(err)
	}
	b1, err := a.Replace("b", w, b0.ID)
	if err != nil {
		t.Fatal(err)
	}

	bh, err := a.History("b")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(bh) != 2 || bh[0].ID != b1.ID || bh[1].ID != b0.ID {
		t.Fatalf("分支历史应只含分支自身记录: %+v", bh)
	}
	// 分支首记录以来源槽记录为父，但来源记录不得被补进列表。
	if bh[1].Parent != ids[1] || !bh[1].SlotFirst {
		t.Fatalf("分支首记录元信息错误: %+v", bh[1])
	}
	for _, info := range bh {
		if info.ID == ids[0] || info.ID == ids[1] || info.ID == ids[2] {
			t.Fatalf("来源槽记录不应进入分支历史: %+v", info)
		}
	}

	// 来源槽自己的历史不受分支影响。
	sh, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	assertHistoryIDs(t, sh, ids)
}

// TestHistoryParentCorruptButExistsDoesNotHideChild 父文件存在但内容损坏时，
// 不能仅因此隐藏自身通过检查的子记录；损坏的父记录本身被略过。
func TestHistoryParentCorruptButExistsDoesNotHideChild(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0

	corruptChecksum(t, dir, ids[1])

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	// ids[0] 的直接父文件 ids[1] 仍在，ids[0] 自身完好，照常列出。
	assertHistoryIDs(t, infos, []RecordID{ids[0], ids[2]})
}

// TestHistoryParentDeletedSkipsDirectChild 父文件被删除、使直接子记录不满足
// 既有父关系要求时，该子记录一并略过；其他可确认归属的完好记录保留。
func TestHistoryParentDeletedSkipsDirectChild(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0

	deleteRecord(t, dir, ids[1])

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	// ids[0] 的直接父 ids[1] 已删除 -> ids[0] 略过；槽首 ids[2] 保留。
	assertHistoryIDs(t, infos, []RecordID{ids[2]})
}

// TestHistoryLegacyPointerKeepsExistingScope 旧格式裸指针继续沿用已有的
// 历史归属范围：沿校验通过的父链重建，链在损坏记录处停止时，无法确认
// 归属的更老完好记录不进入列表。
func TestHistoryLegacyPointerKeepsExistingScope(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0

	// 全部完好时：裸指针重建出完整链，次序与槽首标记均来自校验通过的记录。
	writeLegacyPointer(t, a, "s", ids[0])
	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	infos, err := a2.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	assertHistoryIDs(t, infos, ids)
	if !infos[len(infos)-1].SlotFirst || infos[0].SlotFirst {
		t.Fatalf("槽首标记应只在重建链末端: %+v", infos)
	}

	// 中间记录损坏：重建停在它之前，其后更老的完好记录不被列出。
	corruptChecksum(t, dir, ids[1])
	infos, err = a2.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	assertHistoryIDs(t, infos, []RecordID{ids[0]})
}

// TestHistoryDoesNotMutateArchive 查询不得修补或删除记录、改变槽当前指向
// 或改写历史与世界规则：在含有损坏、缺失记录的槽上查询前后，槽指针与全
// 部记录文件字节一致。
func TestHistoryDoesNotMutateArchive(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0
	corruptChecksum(t, dir, ids[0])
	deleteRecord(t, dir, ids[2])

	slotBytes, err := os.ReadFile(a.slotPath("s"))
	if err != nil {
		t.Fatal(err)
	}
	recordBytes := map[RecordID][]byte{}
	for _, id := range []RecordID{ids[0], ids[1], ids[3]} {
		recordBytes[id] = recordFileBytes(t, a, id)
	}
	entries, err := os.ReadDir(filepath.Join(dir, recordsName))
	if err != nil {
		t.Fatal(err)
	}
	countBefore := len(entries)

	if _, err := a.History("s"); err != nil {
		t.Fatalf("History: %v", err)
	}
	if _, err := a.History("s"); err != nil {
		t.Fatalf("History: %v", err)
	}

	after, err := os.ReadFile(a.slotPath("s"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(slotBytes) {
		t.Fatal("History 改写了槽指针")
	}
	for id, want := range recordBytes {
		if got := recordFileBytes(t, a, id); string(got) != string(want) {
			t.Fatalf("History 改写了记录文件 %s", id)
		}
	}
	entries, err = os.ReadDir(filepath.Join(dir, recordsName))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != countBefore {
		t.Fatalf("History 改变了记录文件数量: %d -> %d", countBefore, len(entries))
	}
	if latest, err := a.readLatestLocked("s"); err != nil || latest != ids[0] {
		t.Fatalf("槽当前指向被改变: %s err=%v", latest, err)
	}
}

// TestHistoryHealthyChainFields 正常情况下每项元信息都与对应实际记录一致：
// 标识与文件名一致，父标识、槽首标记、规则版本均取自校验通过的记录。
func TestHistoryHealthyChainFields(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	assertHistoryIDs(t, infos, ids)
	want := []RecordInfo{
		{ID: ids[0], Parent: ids[1], SlotFirst: false, Version: "v1"},
		{ID: ids[1], Parent: ids[2], SlotFirst: false, Version: "v1"},
		{ID: ids[2], Parent: "", SlotFirst: true, Version: "v1"},
	}
	for i := range want {
		if infos[i] != want[i] {
			t.Fatalf("第 %d 项元信息错误: 得到 %+v，期望 %+v", i, infos[i], want[i])
		}
	}
}
