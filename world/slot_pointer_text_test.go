package world

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// assertPointerTextCorrupt 断言 err 是带槽名、原因明确说明文本编码或
// Unicode 转义损坏的 *CorruptError：不是版本不被接受，不是没有可恢复
// 记录，也不是其他错误类型。
func assertPointerTextCorrupt(t *testing.T, err error, slot string) {
	t.Helper()
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("应为 *CorruptError，得到 %T: %v", err, err)
	}
	if ce.Slot != slot {
		t.Fatalf("CorruptError 应带槽名 %q，得到 %q", slot, ce.Slot)
	}
	if !strings.Contains(ce.Reason, "文本编码损坏") && !strings.Contains(ce.Reason, "Unicode 转义损坏") {
		t.Fatalf("错误应说明是文本编码或 Unicode 转义损坏: %v", ce)
	}
	if errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("槽指针文本损坏不应报成没有可恢复记录: %v", err)
	}
	if _, ok := err.(*VersionRejectedError); ok {
		t.Fatalf("槽指针文本损坏不应报成版本不被接受: %v", err)
	}
}

// 损坏指针的构造片段：无效 UTF-8 字节与各类未配对代理项转义。
const (
	badByte       = "\xff"          // 无效 UTF-8 字节
	loneLow       = `\uDC00`        // 孤立的低位代理项
	loneHigh      = `\uD800`        // 高位代理项后没有低位代理项
	highThenText  = `\uD800x`       // 高位代理项后接普通文字
	highThenHigh  = `\uD800\uD801`  // 高位代理项后仍不是低位代理项
	highThenEscBs = `\uD800\\uDC00` // 高位代理项后是已转义的反斜杠，不是低位转义
	paired        = `\uD800\uDC00`  // 正确配对的代理项转义（合法）
	escBackslash  = `\\uD800`       // 已转义的反斜杠后跟普通文字 uD800（合法）
)

// TestSlotPointerTextCorrupt 槽指针原始文本含无效 UTF-8 字节或未配对的
// Unicode 代理项转义时，整份指针按损坏拒绝——无论问题出现在字段名、当前
// 标识、历史标识还是其他字符串中。Latest、Record、History、RecoverLatest、
// PreviewRecovery 全部返回带槽名、说明文本损坏的 *CorruptError，不返回
// 记录、历史列表或恢复来源；即使所指记录全部完好、规则版本也被接受，仍按
// 指针损坏处理，版本不被接受时也先报损坏。读取保持只读，不修补指针。
func TestSlotPointerTextCorrupt(t *testing.T) {
	cases := []struct {
		name string
		raw  func(latest RecordID, ids []RecordID) string
	}{
		// ---- 无效 UTF-8 字节 ----
		{"字段名含无效字节", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf("{\"latest%s\": %q, \"history\": %s}", badByte, id, quotedRecordIDs(ids))
		}},
		{"当前标识含无效字节", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf(`{"latest": "%s`+badByte+`", "history": %s}`, id, quotedRecordIDs(ids))
		}},
		{"历史标识含无效字节", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": ["%s`+badByte+`"]}`, id, ids[0])
		}},
		{"多余字符串字段含无效字节", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s, "extra": "%s`+badByte+`"}`, id, quotedRecordIDs(ids), "x")
		}},
		// ---- 未配对代理项转义 ----
		{"字段名含孤立低位代理项", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf(`{"latest`+loneLow+`": %q, "history": %s}`, id, quotedRecordIDs(ids))
		}},
		{"当前标识含孤立低位代理项", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf(`{"latest": "%s`+loneLow+`", "history": %s}`, id, quotedRecordIDs(ids))
		}},
		{"历史标识含孤立低位代理项", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": ["%s`+loneLow+`"]}`, id, ids[0])
		}},
		{"当前标识含孤立高位代理项", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf(`{"latest": "%s`+loneHigh+`", "history": %s}`, id, quotedRecordIDs(ids))
		}},
		{"历史标识含孤立高位代理项", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": ["%s`+loneHigh+`"]}`, id, ids[0])
		}},
		{"高位代理项后接普通文字", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": ["%s`+highThenText+`"]}`, id, ids[0])
		}},
		{"高位代理项后仍是高位", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": ["%s`+highThenHigh+`"]}`, id, ids[0])
		}},
		{"高位代理项后是转义反斜杠", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": ["%s`+highThenEscBs+`"]}`, id, ids[0])
		}},
		{"多余字符串字段含孤立代理项", func(id RecordID, ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s, "extra": "%s"}`, id, quotedRecordIDs(ids), loneLow)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			ids := saveN(t, a, "s", 2) // 全部记录完好
			raw := tc.raw(ids[0], ids)
			writePointerRaw(t, a, "s", raw)

			_, err := a.Latest("s", []string{"v1"})
			assertPointerTextCorrupt(t, err, "s")

			// 版本不被接受也不能掩盖指针文本损坏：先报损坏而非版本拒绝。
			_, err = a.Latest("s", []string{"other"})
			assertPointerTextCorrupt(t, err, "s")

			// 即使按标识读取的记录本身完好，也必须先读指针确认归属，同样拒绝。
			_, err = a.Record("s", ids[0], []string{"v1"})
			assertPointerTextCorrupt(t, err, "s")
			_, err = a.Record("s", ids[1], []string{"v1"})
			assertPointerTextCorrupt(t, err, "s")

			if _, err := a.History("s"); err == nil {
				t.Fatal("历史浏览应返回文本损坏的 *CorruptError，而不是历史列表")
			} else {
				assertPointerTextCorrupt(t, err, "s")
			}

			if _, err := a.RecoverLatest("s", []string{"v1"}); err == nil {
				t.Fatal("恢复读取必须拒绝，不能按剩余历史继续寻找旧记录")
			} else {
				assertPointerTextCorrupt(t, err, "s")
			}

			if _, err := a.PreviewRecovery("s", []string{"v1"}); err == nil {
				t.Fatal("恢复预览必须拒绝，不能返回恢复来源")
			} else {
				assertPointerTextCorrupt(t, err, "s")
			}

			// 只读：指针字节原样保留，不自动补成替换字符，也不删改。
			if got, err := os.ReadFile(a.slotPath("s")); err != nil || string(got) != raw {
				t.Fatalf("读取改写了文本损坏的槽指针: %q err=%v", string(got), err)
			}
		})
	}
}

// TestSlotPointerTextCorruptRejectsWrites 依赖槽指针的保存操作遇到文本损坏
// 的指针时一律拒绝：坏槽不能被当成空槽重新创建，覆盖与确认恢复不能改写它，
// 从坏槽分支或向坏槽建立分支也不能产生新记录；指针与已有记录原样保留。
func TestSlotPointerTextCorruptRejectsWrites(t *testing.T) {
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

	// 历史标识中夹一个孤立低位代理项转义。
	raw := fmt.Sprintf(`{"latest": %q, "history": [%q, "%s`+loneLow+`", %q]}`,
		ids[0], ids[0], ids[1], ids[2])
	writePointerRaw(t, a, "s", raw)
	before := recordCount(t, a)

	reject := func(name string, fn func() error) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			err := fn()
			assertPointerTextCorrupt(t, err, "s")
			if got := recordCount(t, a); got != before {
				t.Fatalf("被拒绝的 %s 不应产生新记录: %d -> %d", name, before, got)
			}
			if got, rerr := os.ReadFile(a.slotPath("s")); rerr != nil || string(got) != raw {
				t.Fatalf("%s 改写了文本损坏的槽指针", name)
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

	// 从正常槽分支到一个指针文本已损坏的已存在槽：不能把它当成可创建的空槽。
	t.Run("Branch目标槽", func(t *testing.T) {
		_, err := a.Branch("good", good.ID, "s")
		assertPointerTextCorrupt(t, err, "s")
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
		assertPointerTextCorrupt(t, err, "s")
	}
	if g, err := a2.Latest("good", []string{"v1"}); err != nil || g.ID != good.ID {
		t.Fatalf("其他正常槽应继续可读: id=%s err=%v", g.ID, err)
	}
}

// TestSlotPointerValidTextReadable 合法文本不受文本编码检查影响：真实写入
// 的“�”、中文、正确配对的代理项转义，以及转义后的反斜杠后跟普通文字
// uD800，都不是文本损坏，指针照常读取。
func TestSlotPointerValidTextReadable(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	v1 := []string{"v1"}

	variants := []string{
		// 多余字符串字段含真实的“�”与中文。
		fmt.Sprintf(`{"latest": %q, "history": %s, "note": "替换符�与中文"}`, ids[0], quotedRecordIDs(ids)),
		// 多余字符串字段含正确配对的代理项转义（还原后是同一个字符）。
		fmt.Sprintf(`{"latest": %q, "history": %s, "note": "%s"}`, ids[0], quotedRecordIDs(ids), paired),
		// 转义后的反斜杠后跟普通文字 uD800，不是代理项转义。
		fmt.Sprintf(`{"latest": %q, "history": %s, "note": "%s"}`, ids[0], quotedRecordIDs(ids), escBackslash),
	}
	for i, raw := range variants {
		writePointerRaw(t, a, "s", raw)

		rec, err := a.Latest("s", v1)
		if err != nil {
			t.Fatalf("变体 %d 合法文本应继续可读: %v\n%s", i, err, raw)
		}
		if rec.ID != ids[0] {
			t.Fatalf("变体 %d 当前标识错误: %s", i, rec.ID)
		}
		infos, err := a.History("s")
		if err != nil {
			t.Fatalf("变体 %d 历史应可浏览: %v", i, err)
		}
		if len(infos) != len(ids) || infos[0].ID != ids[0] {
			t.Fatalf("变体 %d 历史内容错误: %+v", i, infos)
		}
		if rec, err := a.RecoverLatest("s", v1); err != nil || rec.ID != ids[0] {
			t.Fatalf("变体 %d 恢复读取应正常: rec=%+v err=%v", i, rec, err)
		}
	}
}
