package world

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// 仅 Unicode 转义还原后与字段标签同名的 JSON 键写法：JSON 解码后分别是
// "latest" 与 "history"，但文本形态与直接写法不同。
const (
	escLatestKey  = "\\u006catest"
	escHistoryKey = "\\u0068istory"
)

// writePointerRaw 以原始字节直接覆盖槽指针文件，用于手工构造生产写入路径
// 不会产生的歧义指针（重复字段、只出现一次的大小写/转义写法等）。
func writePointerRaw(t *testing.T, a *Archive, slot, raw string) {
	t.Helper()
	if err := os.WriteFile(a.slotPath(slot), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

// quotedRecordIDs 把记录标识序列化成 JSON 数组文本（紧凑形式）。
func quotedRecordIDs(ids []RecordID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%q", id)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// assertPointerDuplicateCorrupt 断言 err 是带槽名、原因明确说明槽指针字段
// 重复的 *CorruptError：不是版本不被接受，不是没有可恢复记录，也不是其他
// 错误类型。
func assertPointerDuplicateCorrupt(t *testing.T, err error, slot string) {
	t.Helper()
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("应为 *CorruptError，得到 %T: %v", err, err)
	}
	if ce.Slot != slot {
		t.Fatalf("CorruptError 应带槽名 %q，得到 %q", slot, ce.Slot)
	}
	if !strings.Contains(ce.Error(), "重复") {
		t.Fatalf("错误应明确说明槽指针字段重复: %v", ce)
	}
	if errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("槽指针字段重复不应报成没有可恢复记录: %v", err)
	}
	if _, ok := err.(*VersionRejectedError); ok {
		t.Fatalf("槽指针字段重复不应报成版本不被接受: %v", err)
	}
}

// TestSlotPointerDuplicateFieldCorrupt 槽指针顶层对象内，只要两个名称会被
// 现有读取方式解释为 latest 或 history 中的同一字段，整个指针即损坏：完全
// 同名、只改大小写、Unicode 转义还原后同名都算。两个值相同、一个为空、
// 后一份历史更完整、所指记录全部完好都不豁免；交换先后顺序结果相同。
// Latest、Record、History、RecoverLatest、PreviewRecovery 全部返回带槽名、
// 说明字段重复的 *CorruptError，不返回记录、历史列表或恢复来源；读取保持
// 只读，不修补指针。
func TestSlotPointerDuplicateFieldCorrupt(t *testing.T) {
	// 不存在的标识：若错误地选用了写它的那一份字段，读取会走到记录缺失
	// 而不是字段重复。
	const missing = RecordID("rdeadbeefdeadbeefdeadbeefdeadbeef")

	cases := []struct {
		name string
		raw  func(latest RecordID, full, short []RecordID) string
	}{
		// ---- latest 重复 ----
		{"latest同名异值假前真后", func(id RecordID, full, _ []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "latest": %q, "history": %s}`, missing, id, quotedRecordIDs(full))
		}},
		{"latest同名异值真前假后", func(id RecordID, full, _ []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "latest": %q, "history": %s}`, id, missing, quotedRecordIDs(full))
		}},
		{"latest同名同值", func(id RecordID, full, _ []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "latest": %q, "history": %s}`, id, id, quotedRecordIDs(full))
		}},
		{"latest空值在前", func(id RecordID, full, _ []RecordID) string {
			return fmt.Sprintf(`{"latest": "", "latest": %q, "history": %s}`, id, quotedRecordIDs(full))
		}},
		{"latest空值在后", func(id RecordID, full, _ []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "latest": "", "history": %s}`, id, quotedRecordIDs(full))
		}},
		{"latest大小写异值大写在前", func(id RecordID, full, short []RecordID) string {
			return fmt.Sprintf(`{"Latest": %q, "latest": %q, "history": %s}`, id, short[0], quotedRecordIDs(full))
		}},
		{"latest大小写异值小写在前", func(id RecordID, full, short []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "Latest": %q, "history": %s}`, short[0], id, quotedRecordIDs(full))
		}},
		{"latest大小写同值", func(id RecordID, full, _ []RecordID) string {
			return fmt.Sprintf(`{"Latest": %q, "latest": %q, "history": %s}`, id, id, quotedRecordIDs(full))
		}},
		// l 还原为小写 l；转义写法在前，直接写法在后，两值不同。
		{"latestUnicode转义在前", func(id RecordID, full, short []RecordID) string {
			return fmt.Sprintf("{\"%s\": %q, \"latest\": %q, \"history\": %s}",
				escLatestKey, id, short[0], quotedRecordIDs(full))
		}},
		{"latestUnicode转义在后", func(id RecordID, full, short []RecordID) string {
			return fmt.Sprintf("{\"latest\": %q, \"%s\": %q, \"history\": %s}",
				short[0], escLatestKey, id, quotedRecordIDs(full))
		}},
		// ---- history 重复 ----
		{"history同名短前全后", func(id RecordID, full, short []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s, "history": %s}`, id, quotedRecordIDs(short), quotedRecordIDs(full))
		}},
		{"history同名全前短后", func(id RecordID, full, short []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s, "history": %s}`, id, quotedRecordIDs(full), quotedRecordIDs(short))
		}},
		{"history同名同一份", func(id RecordID, full, _ []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s, "history": %s}`, id, quotedRecordIDs(full), quotedRecordIDs(full))
		}},
		{"history空数组在前", func(id RecordID, full, _ []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": [], "history": %s}`, id, quotedRecordIDs(full))
		}},
		{"history空数组在后", func(id RecordID, full, _ []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s, "history": []}`, id, quotedRecordIDs(full))
		}},
		{"history大小写短前全后", func(id RecordID, full, short []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "History": %s, "history": %s}`, id, quotedRecordIDs(short), quotedRecordIDs(full))
		}},
		{"history大小写全前短后", func(id RecordID, full, short []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s, "History": %s}`, id, quotedRecordIDs(full), quotedRecordIDs(short))
		}},
		// h 还原为小写 h；转义形式给出短历史，直接写法给出完整历史。
		{"historyUnicode转义短前全后", func(id RecordID, full, short []RecordID) string {
			return fmt.Sprintf("{\"latest\": %q, \"%s\": %s, \"history\": %s}",
				id, escHistoryKey, quotedRecordIDs(short), quotedRecordIDs(full))
		}},
		{"historyUnicode转义全前短后", func(id RecordID, full, short []RecordID) string {
			return fmt.Sprintf("{\"latest\": %q, \"history\": %s, \"%s\": %s}",
				id, quotedRecordIDs(full), escHistoryKey, quotedRecordIDs(short))
		}},
		// 重复字段的值类型不对会令 json.Unmarshal 失败，但歧义仍要明确报
		// 字段重复，而不是笼统的“无法解析”。
		{"latest重复其一值类型错误", func(id RecordID, full, _ []RecordID) string {
			return fmt.Sprintf(`{"latest": ["x"], "latest": %q, "history": %s}`, id, quotedRecordIDs(full))
		}},
		{"history重复其一值类型错误", func(id RecordID, full, _ []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": "x", "history": %s}`, id, quotedRecordIDs(full))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			ids := saveN(t, a, "s", 2) // r2 r1 r0，全部完好
			raw := tc.raw(ids[0], ids, ids[:2])
			writePointerRaw(t, a, "s", raw)

			_, err := a.Latest("s", []string{"v1"})
			assertPointerDuplicateCorrupt(t, err, "s")

			// 版本不被接受也不能掩盖指针损坏：先报损坏而非版本拒绝。
			_, err = a.Latest("s", []string{"other"})
			assertPointerDuplicateCorrupt(t, err, "s")

			// 即使按标识读取的是一份完好历史记录，也必须先确认它在槽历史中，
			// 因而同样拒绝。
			_, err = a.Record("s", ids[0], []string{"v1"})
			assertPointerDuplicateCorrupt(t, err, "s")
			_, err = a.Record("s", ids[1], []string{"v1"})
			assertPointerDuplicateCorrupt(t, err, "s")

			if _, err := a.History("s"); err == nil {
				t.Fatal("历史浏览应返回字段重复的 *CorruptError，而不是历史列表")
			} else {
				assertPointerDuplicateCorrupt(t, err, "s")
			}

			if _, err := a.RecoverLatest("s", []string{"v1"}); err == nil {
				t.Fatal("恢复读取必须拒绝，不能按任意一份 history 继续查找旧记录")
			} else {
				assertPointerDuplicateCorrupt(t, err, "s")
			}

			if _, err := a.PreviewRecovery("s", []string{"v1"}); err == nil {
				t.Fatal("恢复预览必须拒绝，不能返回恢复来源")
			} else {
				assertPointerDuplicateCorrupt(t, err, "s")
			}

			// 只读：指针字节原样保留，不自动修补或删去重复字段。
			if got, err := os.ReadFile(a.slotPath("s")); err != nil || string(got) != raw {
				t.Fatalf("读取改写了含重复字段的槽指针: %q err=%v", string(got), err)
			}
		})
	}
}

// TestSlotPointerDuplicateRejectsWrites 需要依赖槽指针承接当前记录与历史的
// 写入操作，遇到重复字段指针时一律拒绝：不创建新记录，不自动修补字段，
// 指针与已有记录原样保留。
func TestSlotPointerDuplicateRejectsWrites(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)

	// 在损坏指针前先取回一个可用世界与元信息。
	rec, err := a.Record("s", ids[0], []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatal(err)
	}
	good := saveBase(t, a, "good")

	raw := fmt.Sprintf(`{"latest": %q, "latest": %q, "history": %s}`,
		ids[0], ids[1], quotedRecordIDs(ids))
	writePointerRaw(t, a, "s", raw)
	before := recordCount(t, a)

	reject := func(name string, fn func() error) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			err := fn()
			assertPointerDuplicateCorrupt(t, err, "s")
			if got := recordCount(t, a); got != before {
				t.Fatalf("被拒绝的 %s 不应产生新记录: %d -> %d", name, before, got)
			}
			if got, rerr := os.ReadFile(a.slotPath("s")); rerr != nil || string(got) != raw {
				t.Fatalf("%s 改写了含重复字段的槽指针", name)
			}
		})
	}

	reject("Save", func() error {
		_, err := a.Save("s", w)
		return err
	})
	reject("Replace", func() error {
		_, err := a.Replace("s", w, ids[0])
		return err
	})
	reject("CheckUpgrade", func() error {
		_, err := a.CheckUpgrade("s", []string{"v1"}, v2Rules())
		return err
	})
	reject("Upgrade", func() error {
		_, err := a.Upgrade("s", []string{"v1"}, v2Rules(), ids[0])
		return err
	})
	reject("PreviewMigration", func() error {
		_, err := a.PreviewMigration("s", []string{"v1"}, v2Rules(), nil, nil)
		return err
	})
	reject("Migrate", func() error {
		_, err := a.Migrate("s", []string{"v1"}, v2Rules(), nil, nil, ids[0])
		return err
	})
	reject("ConfirmRecovery", func() error {
		_, err := a.ConfirmRecovery("s", ids[0], ids[1], []string{"v1"})
		return err
	})
	reject("Branch源槽", func() error {
		_, err := a.Branch("s", ids[1], "b")
		return err
	})

	// 从正常槽分支到一个指针已损坏的已存在槽：目标槽读取报字段重复，
	// 不能把它当成可创建的空槽。
	t.Run("Branch目标槽", func(t *testing.T) {
		_, err := a.Branch("good", good.ID, "s")
		assertPointerDuplicateCorrupt(t, err, "s")
		if got := recordCount(t, a); got != before {
			t.Fatalf("被拒绝的 Branch 不应产生新记录: %d -> %d", before, got)
		}
		if _, err := a.readLatestLocked("good"); err != nil {
			t.Fatalf("源正常槽不应受影响: %v", err)
		}
	})

	// 重新打开目录本身不受影响；坏槽依旧损坏，正常槽继续可用。
	a2, err := Open(dir)
	if err != nil {
		t.Fatalf("含坏槽的存档目录应能打开: %v", err)
	}
	if _, err := a2.Latest("s", []string{"v1"}); err == nil {
		t.Fatal("重开后坏槽仍应损坏")
	} else {
		assertPointerDuplicateCorrupt(t, err, "s")
	}
	if g, err := a2.Latest("good", []string{"v1"}); err != nil || g.ID != good.ID {
		t.Fatalf("其他正常槽应继续可读: id=%s err=%v", g.ID, err)
	}
}

// TestRecoverDuplicateHistoryNoFallback history 字段重复、两份历史内容不同
// 时，恢复读取与预览不能挑其中一份继续：即使其中一份完整指向全部完好记录、
// 交换两份次序也都得到相同的字段重复损坏结果。
func TestRecoverDuplicateHistoryNoFallback(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	onlyMissing := []RecordID{RecordID("r00000000000000000000000000000000")}

	for _, swapped := range []bool{false, true} {
		var raw string
		if !swapped {
			raw = fmt.Sprintf(`{"latest": %q, "history": %s, "history": %s}`,
				ids[0], quotedRecordIDs(onlyMissing), quotedRecordIDs(ids))
		} else {
			raw = fmt.Sprintf(`{"latest": %q, "history": %s, "history": %s}`,
				ids[0], quotedRecordIDs(ids), quotedRecordIDs(onlyMissing))
		}
		writePointerRaw(t, a, "s", raw)

		if _, err := a.RecoverLatest("s", []string{"v1"}); err == nil {
			t.Fatalf("swapped=%v: 不能按完整的那份 history 恢复出完好记录", swapped)
		} else {
			assertPointerDuplicateCorrupt(t, err, "s")
		}
		if pv, err := a.PreviewRecovery("s", []string{"v1"}); err == nil {
			t.Fatalf("swapped=%v: 预览不能返回 current=%s source=%s", swapped, pv.Current, pv.Source)
		} else {
			assertPointerDuplicateCorrupt(t, err, "s")
		}
	}
}

// TestSlotPointerSingleVariantAndLegacyReadable 字段名称只出现一次时，既有
// 可接受的大小写与 Unicode 转义写法继续可读；旧版本仅含 latest 的裸指针
// 继续可用，缺少 history 不算重复。
func TestSlotPointerSingleVariantAndLegacyReadable(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	v1 := []string{"v1"}

	variants := []string{
		// 两个字段都改成首字母大写写法，各出现一次。
		fmt.Sprintf(`{"Latest": %q, "History": %s}`, ids[0], quotedRecordIDs(ids)),
		// latest 以 Unicode 转义书写（l -> l），只出现一次。
		fmt.Sprintf("{\"%s\": %q, \"history\": %s}", escLatestKey, ids[0], quotedRecordIDs(ids)),
		// history 以 Unicode 转义书写（h -> h），只出现一次。
		fmt.Sprintf("{\"latest\": %q, \"%s\": %s}", ids[0], escHistoryKey, quotedRecordIDs(ids)),
		// 旧版本裸指针：没有 history 字段，沿校验通过的父链重建历史。
		fmt.Sprintf(`{"latest": %q}`, ids[0]),
		// 不映射到任何固定字段的多余键即使彼此仅大小写不同，也不被解释为
		// latest/history，继续按原方式忽略。
		fmt.Sprintf(`{"latest": %q, "history": %s, "extra": 1, "Extra": 2}`,
			ids[0], quotedRecordIDs(ids)),
	}
	for i, raw := range variants {
		writePointerRaw(t, a, "s", raw)

		rec, err := a.Latest("s", v1)
		if err != nil {
			t.Fatalf("变体 %d 只出现一次的可接受写法应继续可读: %v\n%s", i, err, raw)
		}
		if rec.ID != ids[0] {
			t.Fatalf("变体 %d 当前标识错误: %s", i, rec.ID)
		}
		infos, err := a.History("s")
		if err != nil {
			t.Fatalf("变体 %d 历史应可读（裸指针可重建）: %v", i, err)
		}
		if len(infos) != len(ids) || infos[0].ID != ids[0] || infos[len(ids)-1].ID != ids[2] {
			t.Fatalf("变体 %d 历史次序错误: %+v", i, infos)
		}
	}

	// 重开后旧裸指针仍只读可用。
	writePointerRaw(t, a, "s", fmt.Sprintf(`{"latest": %q}`, ids[0]))
	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rec, err := a2.RecoverLatest("s", v1); err != nil || rec.ID != ids[0] {
		t.Fatalf("旧裸指针恢复读取应继续可用: %+v err=%v", rec, err)
	}
}

// TestSlotPointerDuplicateRecordIDInHistoryUnchanged history 数组里同一记录
// 标识出现多次，与字段名称写两次是不同情况：浏览继续按首次出现次序去重，
// Latest、Record 与恢复读取照常工作，不能误报字段重复。
func TestSlotPointerDuplicateRecordIDInHistoryUnchanged(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	withDup := append([]RecordID{ids[0]}, ids...) // r2, r2, r1, r0
	writePointerRaw(t, a, "s",
		fmt.Sprintf(`{"latest": %q, "history": %s}`, ids[0], quotedRecordIDs(withDup)))

	if _, err := a.Latest("s", []string{"v1"}); err != nil {
		t.Fatalf("history 数组内标识重复不是字段重复，Latest 应可读: %v", err)
	}
	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("history 数组内标识重复不应让浏览失败: %v", err)
	}
	if len(infos) != len(ids) {
		t.Fatalf("应按首次出现次序去重: %+v", infos)
	}
	for i, want := range ids {
		if infos[i].ID != want {
			t.Fatalf("去重后次序应保持不变: %+v", infos)
		}
	}
	if _, err := a.Record("s", ids[1], []string{"v1"}); err != nil {
		t.Fatalf("历史记录应可读: %v", err)
	}
	if rec, err := a.RecoverLatest("s", []string{"v1"}); err != nil || rec.ID != ids[0] {
		t.Fatalf("恢复读取应正常: rec=%+v err=%v", rec, err)
	}
}

// TestSlotPointerCorruptDoesNotAffectOtherSlots 一个槽的指针字段重复不影响
// 打开存档目录、列出槽位或使用其他正常槽；损坏槽也不会被 Slots 悄悄隐藏。
func TestSlotPointerCorruptDoesNotAffectOtherSlots(t *testing.T) {
	a, dir := newTestArchive(t)
	bad := saveBase(t, a, "bad")
	good := saveBase(t, a, "good")
	writePointerRaw(t, a, "bad",
		fmt.Sprintf(`{"latest": %q, "latest": %q}`, bad.ID, good.ID))

	a2, err := Open(dir)
	if err != nil {
		t.Fatalf("存档目录应能打开: %v", err)
	}
	names, err := a2.Slots()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	if !seen["bad"] || !seen["good"] {
		t.Fatalf("两个槽都应仍被列出: %v", names)
	}
	if _, err := a2.Latest("good", []string{"v1"}); err != nil {
		t.Fatalf("正常槽应继续可读: %v", err)
	}
	if _, err := a2.History("good"); err != nil {
		t.Fatalf("正常槽历史应继续可浏览: %v", err)
	}
	if _, err := a2.Latest("bad", []string{"v1"}); err == nil {
		t.Fatal("坏槽应继续报损坏")
	} else {
		assertPointerDuplicateCorrupt(t, err, "bad")
	}
}
