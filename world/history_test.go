package world

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// assertHistoryIDs 断言历史元信息的标识序列与 want 完全一致。
func assertHistoryIDs(t *testing.T, hist []RecordInfo, want ...RecordID) {
	t.Helper()
	if len(hist) != len(want) {
		got := make([]RecordID, len(hist))
		for i, h := range hist {
			got[i] = h.ID
		}
		t.Fatalf("历史应为 %v，得到 %v", want, got)
	}
	for i := range want {
		if hist[i].ID != want[i] {
			t.Fatalf("历史次序错误: 第 %d 项应为 %s，得到 %s（整体 %+v）",
				i, want[i], hist[i].ID, hist)
		}
	}
}

// TestHistorySkipsTamperedLatestRecord 题述场景：连续保存三次后，仅把最新
// 记录的规则版本改成另一个值而不更新校验和，History 留下前两次保存的记录，
// 不展示被改动的版本；同一条记录用 Record 读取同样被拒绝。
func TestHistorySkipsTamperedLatestRecord(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0，均为 v1

	// 只改最新记录里的规则版本，不重算校验和。
	setRecordField(t, dir, ids[0], `"Version": "v1"`, `"Version": "v9"`)

	hist, err := a.History("s")
	if err != nil {
		t.Fatalf("最新记录被改动不应让整个列表失败: %v", err)
	}
	assertHistoryIDs(t, hist, ids[1], ids[2])
	for _, h := range hist {
		if h.Version != "v1" {
			t.Fatalf("完好记录的版本元信息应为 v1，得到 %+v", h)
		}
	}

	// 即使调用方恰好接受 v9，校验和失败仍使 Record 拒绝被改动的记录。
	if _, err := a.Record("s", ids[0], []string{"v9"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("被改动记录应按损坏拒绝，得到 %T: %v", err, err)
	}
}

// TestHistorySkipsMissingAndUnparseableRecords 最新记录缺失、中间记录无法
// 解析时，其余合格记录仍按保存次序返回，列表整体不失败。
func TestHistorySkipsMissingAndUnparseableRecords(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	deleteRecord(t, dir, ids[0])
	truncateRecord(t, dir, ids[1])

	hist, err := a.History("s")
	if err != nil {
		t.Fatalf("单条记录缺失或无法解析不应让列表失败: %v", err)
	}
	assertHistoryIDs(t, hist, ids[2])
}

// TestHistoryCorruptParentFileKeepsChild 父文件存在但内容损坏时，不能仅因此
// 隐藏自身通过检查的子记录；损坏的父记录自身被略过。
func TestHistoryCorruptParentFileKeepsChild(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	corruptChecksum(t, dir, ids[1])

	hist, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	// r2 自身完好，父文件 r1 仍存在，照常列出；r1 损坏被略过；r0 保留。
	assertHistoryIDs(t, hist, ids[0], ids[2])
}

// TestHistoryDeletedParentSkipsChild 父文件被删除使子记录不满足既有的父
// 关系存在性要求时，该子记录一并略过，其他可确认归属的完好记录保留。
func TestHistoryDeletedParentSkipsChild(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0
	deleteRecord(t, dir, ids[1])

	hist, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	// r3 以 r2 为父，父文件缺失 -> 略过；r2 文件已删除 -> 略过；
	// r1、r0 完好且父关系成立，按保存次序保留。
	assertHistoryIDs(t, hist, ids[2], ids[3])
}

// TestHistoryAllCorruptReturnsEmpty 槽存在且历史可确定、但所有记录都被
// 略过时，返回空列表和成功结果。
func TestHistoryAllCorruptReturnsEmpty(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	for _, id := range ids {
		corruptChecksum(t, dir, id)
	}
	hist, err := a.History("s")
	if err != nil {
		t.Fatalf("全部损坏应返回空列表与 nil 错误，得到 %v", err)
	}
	if len(hist) != 0 {
		t.Fatalf("应没有可展示的记录，得到 %+v", hist)
	}
}

// TestHistoryOrderIsSaveOrder 列表按保存生效次序排列，不被世界时间片大小
// 或文件修改时间重排；略过中间损坏记录后次序依旧。
func TestHistoryOrderIsSaveOrder(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0

	// 把最老记录的文件修改时间拨到将来，不能让它排到前面。
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(recordPath(dir, ids[3]), future, future); err != nil {
		t.Fatal(err)
	}
	hist, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryIDs(t, hist, ids...)

	// 中间记录损坏后，剩余记录仍保持原保存次序，不重排也不改变范围。
	corruptChecksum(t, dir, ids[1])
	hist, err = a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryIDs(t, hist, ids[0], ids[2], ids[3])
}

// rewriteRecordFieldsNoChecksum 用 load+marshal 改写记录的父标识与槽首
// 标记，但保留原校验和，制造一条字段被篡改且校验不过的损坏记录。
func rewriteRecordFieldsNoChecksum(t *testing.T, dir string, id RecordID, parent RecordID, first bool) {
	t.Helper()
	env, err := loadRecord(recordPath(dir, id))
	if err != nil {
		t.Fatal(err)
	}
	env.Parent = parent
	env.SlotFirst = first
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath(dir, id), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestHistoryDoesNotTrustTamperedFields 不根据损坏记录中的父标识或槽首标记
// 改变查询范围：遍历次序完全来自槽指针内嵌的历史索引。
func TestHistoryDoesNotTrustTamperedFields(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0

	// 把中间记录的父标识改到一个不属于本槽历史的标识、并伪造槽首标记，
	// 同时保留原校验和使其校验不过。
	rewriteRecordFieldsNoChecksum(t, dir, ids[1],
		RecordID("r"+strings.Repeat("00", 16)), true)

	hist, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	// 伪造的父标识没有把任何外部记录补进列表，伪造的槽首标记也没有截断
	// 更老记录：范围仍是槽指针索引，损坏项只是被略过。
	assertHistoryIDs(t, hist, ids[0], ids[2])
}

// TestHistoryMetadataMatchesVerifiedRecord 每项元信息都对应一份校验通过的
// 实际记录：父标识、槽首标记与规则版本与记录内容一致。
func TestHistoryMetadataMatchesVerifiedRecord(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0

	hist, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryIDs(t, hist, ids...)
	if hist[0].Parent != ids[1] || hist[0].SlotFirst || hist[0].Version != "v1" {
		t.Fatalf("最新记录元信息错误: %+v", hist[0])
	}
	if hist[1].Parent != ids[2] || hist[1].SlotFirst || hist[1].Version != "v1" {
		t.Fatalf("中间记录元信息错误: %+v", hist[1])
	}
	if hist[2].Parent != "" || !hist[2].SlotFirst || hist[2].Version != "v1" {
		t.Fatalf("槽首记录元信息错误: %+v", hist[2])
	}
}

// TestHistoryListsIntactOldRuleVersion 完整性与规则版本是否适合调用方是两
// 回事：History 没有可接受版本参数，完好的旧规则版本记录仍正常列出；同一
// 条记录用 Record 读取时仍可因版本不被接受而被拒绝。
func TestHistoryListsIntactOldRuleVersion(t *testing.T) {
	a, _ := newTestArchive(t)
	r0 := saveBase(t, a, "s") // v1

	// 用只改了版本号的合法世界覆盖出一条 v2 记录（校验和正确、状态合法）。
	v2 := baseRules()
	v2.Version = "v2"
	w, err := NewWorld(InitialData{
		Seed:       42,
		Rules:      v2,
		Characters: []Character{{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r1, err := a.Replace("s", w, r0.ID)
	if err != nil {
		t.Fatal(err)
	}

	// 不接受任何特定版本：两条不同规则版本的完好记录都列出。
	hist, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryIDs(t, hist, r1.ID, r0.ID)
	if hist[0].Version != "v2" || hist[1].Version != "v1" {
		t.Fatalf("版本元信息应取自记录本身: %+v", hist)
	}

	// Record 仍按调用方给出版本集合判断：只接受 v1 时 v2 记录被拒绝。
	if _, err := a.Record("s", r1.ID, []string{"v1"}); !errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("v2 记录对只接受 v1 的调用方应版本拒绝，得到 %T: %v", err, err)
	}
	if _, err := a.Record("s", r1.ID, []string{"v2"}); err != nil {
		t.Fatalf("接受 v2 时应能读取: %v", err)
	}
}

// TestHistoryBranchBoundary 分支只列出分支自身保存的记录，不把来源槽的父
// 记录补进列表；来源槽的历史不含分支记录。
func TestHistoryBranchBoundary(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1) // r1 r0
	b0, err := a.Branch("s", ids[1], "b")
	if err != nil {
		t.Fatal(err)
	}

	bhist, err := a.History("b")
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryIDs(t, bhist, b0.ID)
	// 分支首记录以来源为父、标记槽首，但来源槽记录不进入分支列表。
	if bhist[0].Parent != ids[1] || !bhist[0].SlotFirst {
		t.Fatalf("分支首记录元信息错误: %+v", bhist[0])
	}

	shist, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryIDs(t, shist, ids[0], ids[1])
}

// TestHistoryOrphanRecordNotListed 没有进入本槽历史的记录不能因为文件完好
// 就被加入列表（写完但未生效的孤儿记录）。
func TestHistoryOrphanRecordNotListed(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1) // r1 r0
	orphan := writeRawRecord(t, a, ids[0], false, baseWorld(t).Snapshot())

	hist, err := a.History("s")
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryIDs(t, hist, ids[0], ids[1])
	for _, h := range hist {
		if h.ID == orphan {
			t.Fatalf("未生效的孤儿记录 %s 不应进入历史", orphan)
		}
	}
}

// TestHistoryLegacyPointerIntactChain 旧格式裸指针沿用已有历史归属范围：
// 沿校验通过的父链重建后，完好记录全部按保存次序列出。
func TestHistoryLegacyPointerIntactChain(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	writeLegacyPointer(t, a, "s", ids[0])

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := a2.History("s")
	if err != nil {
		t.Fatalf("旧格式槽指针下应能列出完好历史: %v", err)
	}
	assertHistoryIDs(t, hist, ids...)
}

// TestHistoryLegacyPointerBrokenScope 旧格式裸指针在断裂处停止确认归属：
// 断裂之后的更老记录即使文件完好也不列出；已确认归属的完好记录保留。
func TestHistoryLegacyPointerBrokenScope(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	// 删除中间记录 r1：旧格式裸指针重建在 r2 处失败（父文件缺失），
	// r0 无法经由断裂链确认归属，即使它文件完好也不列出。
	deleteRecord(t, dir, ids[1])
	writeLegacyPointer(t, a, "s", ids[0])

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := a2.History("s")
	if err != nil {
		t.Fatalf("归属无法确认应返回空列表而非错误: %v", err)
	}
	assertHistoryIDs(t, hist /* 空 */)
}

// TestHistorySlotMissingAndPointerCorrupt 槽不存在继续返回不存在错误；槽
// 指针无法解析继续返回损坏错误，这两种情况不退化成空列表。
func TestHistorySlotMissingAndPointerCorrupt(t *testing.T) {
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

// TestHistoryIsReadOnly 查询不得修补或删除记录、改变槽当前指向或改写历史
// 与世界规则：在含损坏记录的槽上反复查询后，槽指针文件逐字节不变，记录
// 文件也保持查询前内容。
func TestHistoryIsReadOnly(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	corruptChecksum(t, dir, ids[1])

	slotFile := a.slotPath("s")
	pointerBefore, err := os.ReadFile(slotFile)
	if err != nil {
		t.Fatal(err)
	}
	badRecordBefore, err := os.ReadFile(recordPath(dir, ids[1]))
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		hist, err := a.History("s")
		if err != nil {
			t.Fatalf("History: %v", err)
		}
		assertHistoryIDs(t, hist, ids[0], ids[2])
	}

	pointerAfter, err := os.ReadFile(slotFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(pointerAfter) != string(pointerBefore) {
		t.Fatalf("槽指针被查询改写:\nbefore=%s\nafter=%s", pointerBefore, pointerAfter)
	}
	badRecordAfter, err := os.ReadFile(recordPath(dir, ids[1]))
	if err != nil {
		t.Fatal(err)
	}
	if string(badRecordAfter) != string(badRecordBefore) {
		t.Fatal("损坏记录文件被查询修补或删除")
	}
}
