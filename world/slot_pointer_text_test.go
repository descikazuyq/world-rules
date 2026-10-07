package world

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// assertPointerTextCorrupt 断言 err 是带槽名、原因明确说明槽指针文本编码或
// Unicode 转义损坏的 *CorruptError：不是版本不被接受，不是没有可恢复记录，
// 也不是字段重复或其他错误。
func assertPointerTextCorrupt(t *testing.T, err error, slot, keyword string) {
	t.Helper()
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("应为 *CorruptError，得到 %T: %v", err, err)
	}
	if ce.Slot != slot {
		t.Fatalf("CorruptError 应带槽名 %q，得到 %q", slot, ce.Slot)
	}
	if !strings.Contains(ce.Error(), keyword) {
		t.Fatalf("错误应说明损坏类别 %q: %v", keyword, ce)
	}
	if !strings.Contains(ce.Error(), "槽指针") {
		t.Fatalf("错误应指出损坏的是槽指针: %v", ce)
	}
	if errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("槽指针文本损坏不应报成没有可恢复记录: %v", err)
	}
	if _, ok := err.(*VersionRejectedError); ok {
		t.Fatalf("槽指针文本损坏不应报成版本不被接受: %v", err)
	}
}

// TestSlotPointerTextCorruptRejectsReads 槽指针原始字节中只要含无效 UTF-8，
// 或字符串里的 Unicode 代理项转义没有正确配对——无论出现在字段名、当前标识、
// 历史标识还是其他字符串中——整份指针都按损坏拒绝：Latest、Record、History、
// RecoverLatest、PreviewRecovery 都不返回记录、历史列表或可确认的恢复来源。
// 即使所指记录全部完好、规则版本也被接受，仍报带槽名、说明文本编码或
// Unicode 转义损坏的 *CorruptError；版本不被接受时也不报版本拒绝或
// ErrUnrecoverable。读取保持只读，坏字节原样保留。
func TestSlotPointerTextCorruptRejectsReads(t *testing.T) {
	// 含真实非法字节的 JSON 字符串 token（反引号写法不会产生 0xFF 字节，
	// 必须用解释字符串拼接）。
	badUTF8Value := "\"r00" + "\xff" + "\""
	badUTF8Key := "\"x" + "\xff\""

	joinHistory := func(ids []RecordID, at int, bad string) string {
		parts := make([]string, 0, len(ids)+1)
		for i, id := range ids {
			if i == at {
				parts = append(parts, bad)
			}
			parts = append(parts, fmt.Sprintf("%q", id))
		}
		if at >= len(ids) {
			parts = append(parts, bad)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}

	cases := []struct {
		name    string
		keyword string
		raw     func(ids []RecordID) string
	}{
		// ---- 无效 UTF-8 字节 ----
		{"当前标识含无效UTF8", "UTF-8", func(ids []RecordID) string {
			// 真实 0xFF 字节必须在解释字符串中给出，不能写进反引号原始字符串。
			return "{\"latest\": \"r00" + "\xff" + "\", \"history\": " + quotedRecordIDs(ids) + "}"
		}},
		{"历史标识含无效UTF8但当前标识正确", "UTF-8", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s}`, ids[0], joinHistory(ids, 1, badUTF8Value))
		}},
		{"字段名含无效UTF8", "UTF-8", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s, %s: 1}`,
				ids[0], quotedRecordIDs(ids), badUTF8Key)
		}},
		{"其他字符串含无效UTF8", "UTF-8", func(ids []RecordID) string {
			return `{"latest": ` + fmt.Sprintf("%q", ids[0]) +
				`, "history": ` + quotedRecordIDs(ids) + `, "note": "a` + "\xff" + `b"}`
		}},
		// ---- 不成对的代理项转义 ----
		{"当前标识高位代理项在末尾", "Unicode", func(ids []RecordID) string {
			return `{"latest": "r00\uD800", "history": ` + quotedRecordIDs(ids) + `}`
		}},
		{"历史标识高位代理项后接普通文字", "Unicode", func(ids []RecordID) string {
			return `{"latest": ` + fmt.Sprintf("%q", ids[0]) +
				`, "history": ` + joinHistory(ids, 1, `"r00\uD800ab"`) + `}`
		}},
		{"历史标识高位后没有紧接低位", "Unicode", func(ids []RecordID) string {
			return `{"latest": ` + fmt.Sprintf("%q", ids[0]) +
				`, "history": ` + joinHistory(ids, 1, `"r00\uD800\uD800"`) + `}`
		}},
		{"历史标识孤立低位代理项", "Unicode", func(ids []RecordID) string {
			return `{"latest": ` + fmt.Sprintf("%q", ids[0]) +
				`, "history": ` + joinHistory(ids, 1, `"r00\uDC00ab"`) + `}`
		}},
		{"字段名含孤立低位代理项", "Unicode", func(ids []RecordID) string {
			return `{"latest": ` + fmt.Sprintf("%q", ids[0]) +
				`, "history": ` + quotedRecordIDs(ids) + `, "x\uDC00": 1}`
		}},
		{"history键名含孤立高位代理项", "Unicode", func(ids []RecordID) string {
			// 键名损坏后 encoding/json 会找不到 history；文本检查必须更早拒绝。
			return `{"latest": ` + fmt.Sprintf("%q", ids[0]) +
				`, "histor\uD800": ` + quotedRecordIDs(ids) + `}`
		}},
		{"其他字符串含孤立低位代理项", "Unicode", func(ids []RecordID) string {
			return `{"latest": ` + fmt.Sprintf("%q", ids[0]) +
				`, "history": ` + quotedRecordIDs(ids) + `, "note": "a\uDC00b"}`
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			ids := saveN(t, a, "s", 2) // r2 r1 r0，全部完好
			raw := tc.raw(ids)
			writePointerRaw(t, a, "s", raw)

			// 当前世界：版本被接受与不被接受都报指针文本损坏。
			if _, err := a.Latest("s", []string{"v1"}); err == nil {
				t.Fatal("Latest 不应成功：不能把坏字节补成替换字符后交付")
			} else {
				assertPointerTextCorrupt(t, err, "s", tc.keyword)
			}
			if _, err := a.Latest("s", []string{"other"}); err == nil {
				t.Fatal("版本不被接受也不能掩盖指针文本损坏")
			} else {
				assertPointerTextCorrupt(t, err, "s", tc.keyword)
			}

			// 按标识读取的历史记录本身完好，但归属确认必须先读指针。
			if _, err := a.Record("s", ids[1], []string{"v1"}); err == nil {
				t.Fatal("Record 必须先确认槽指针可读，不能交付完好记录")
			} else {
				assertPointerTextCorrupt(t, err, "s", tc.keyword)
			}

			if infos, err := a.History("s"); err == nil {
				t.Fatalf("历史浏览必须整体拒绝，不能只略过改写后的标识: %+v", infos)
			} else {
				assertPointerTextCorrupt(t, err, "s", tc.keyword)
			}

			// 恢复读取不能沿剩余历史继续找旧存档；所有版本都不接受时也不能
			// 改报不可恢复。
			if _, err := a.RecoverLatest("s", []string{"v1"}); err == nil {
				t.Fatal("恢复读取必须拒绝，不能按剩余历史继续寻找旧存档")
			} else {
				assertPointerTextCorrupt(t, err, "s", tc.keyword)
			}
			if _, err := a.RecoverLatest("s", []string{"other"}); err == nil {
				t.Fatal("无版本可接受时也不能恢复成功")
			} else if errors.Is(err, ErrUnrecoverable) {
				t.Fatalf("指针文本损坏必须报损坏，不能改报不可恢复: %v", err)
			} else {
				assertPointerTextCorrupt(t, err, "s", tc.keyword)
			}

			if pv, err := a.PreviewRecovery("s", []string{"v1"}); err == nil {
				t.Fatalf("恢复预览必须拒绝，不能返回 current=%s source=%s", pv.Current, pv.Source)
			} else {
				assertPointerTextCorrupt(t, err, "s", tc.keyword)
			}

			// 只读：坏字节原样保留，不补替换字符，不删除。
			if got, err := os.ReadFile(a.slotPath("s")); err != nil || string(got) != raw {
				t.Fatalf("读取改写了文本损坏的槽指针: %q err=%v", string(got), err)
			}
		})
	}
}

// TestSlotPointerTextCorruptRejectsWrites 依赖槽指针的保存操作在指针文本
// 损坏时一律拒绝：坏槽不能被当成空槽重新创建，覆盖、升级、迁移、确认恢复
// 不能改写它，从坏槽分支或向坏槽建立分支都不能产生新记录。指针与记录原样
// 保留，其他正常槽照常使用。
func TestSlotPointerTextCorruptRejectsWrites(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)

	rec, err := a.Record("s", ids[0], []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := WorldFromState(rec.State)
	if err != nil {
		t.Fatal(err)
	}
	good := saveBase(t, a, "good")

	// 当前标识完全正确、所有记录完好，仅历史数组中混入一个坏字节标识。
	badHistory := "[" + fmt.Sprintf("%q, ", ids[0]) + fmt.Sprintf("%q, ", ids[1]) +
		"\"r00\xff\", " + fmt.Sprintf("%q", ids[2]) + "]"
	raw := fmt.Sprintf(`{"latest": %q, "history": %s}`, ids[0], badHistory)
	writePointerRaw(t, a, "s", raw)
	before := recordCount(t, a)

	reject := func(name string, fn func() error) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			err := fn()
			assertPointerTextCorrupt(t, err, "s", "UTF-8")
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

	t.Run("Branch目标槽", func(t *testing.T) {
		// 向坏槽建立分支：不能把已有坏槽当成可创建的空槽。
		_, err := a.Branch("good", good.ID, "s")
		assertPointerTextCorrupt(t, err, "s", "UTF-8")
		if got := recordCount(t, a); got != before {
			t.Fatalf("被拒绝的 Branch 不应产生新记录: %d -> %d", before, got)
		}
		if got, rerr := os.ReadFile(a.slotPath("s")); rerr != nil || string(got) != raw {
			t.Fatal("Branch 改写了目标坏槽指针")
		}
	})

	// 坏槽仍被列出，其他正常槽照常使用；重开目录后结论不变。
	names, err := a.Slots()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	if !seen["s"] || !seen["good"] {
		t.Fatalf("坏槽与正常槽都应仍被列出: %v", names)
	}
	a2, err := Open(dir)
	if err != nil {
		t.Fatalf("含坏槽的存档目录应能打开: %v", err)
	}
	if _, err := a2.Latest("s", []string{"v1"}); err == nil {
		t.Fatal("重开后坏槽仍应损坏")
	} else {
		assertPointerTextCorrupt(t, err, "s", "UTF-8")
	}
	if g, err := a2.Latest("good", []string{"v1"}); err != nil || g.ID != good.ID {
		t.Fatalf("其他正常槽应继续可读: id=%s err=%v", g.ID, err)
	}
}

// TestSlotPointerLegalTextStillReadable 合法文本的兼容性保留：真实写入的
// “�”、中文、正确配对的代理项转义，以及转义后的反斜杠后跟普通文字 uD800，
// 都不是文本损坏；只出现一次的字段大小写变体继续可读，旧指针缺少历史数组
// 仍按原规则重建。
func TestSlotPointerLegalTextStillReadable(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	hist := quotedRecordIDs(ids)
	v1 := []string{"v1"}

	variants := []string{
		// 真实的替换字符、中文、表情：合法 UTF-8。
		fmt.Sprintf(`{"latest": %q, "history": %s, "替�身": "大厅😀"}`, ids[0], hist),
		// 正确配对的代理项转义（值和键中各一个）解码为 😀，合法。
		`{"latest": ` + fmt.Sprintf("%q", ids[0]) + `, "history": ` + hist +
			`, "note": "😀", "k😀": 1}`,
		// 同一字符以纯 ASCII 形态的配对代理项转义书写，也合法。
		`{"latest": ` + fmt.Sprintf("%q", ids[0]) + `, "history": ` + hist +
			`, "note": "a😀b", "k😀": 1}`,
		// 已转义的反斜杠其后的 uD800 只是普通文字，不是代理项转义。
		`{"latest": ` + fmt.Sprintf("%q", ids[0]) + `, "history": ` + hist +
			`, "note": "\\uD800"}`,
		// 字段名只出现一次的大小写变体。
		fmt.Sprintf(`{"Latest": %q, "History": %s}`, ids[0], hist),
		// 旧版本裸指针：没有 history 字段，沿校验通过的父链重建。
		fmt.Sprintf(`{"latest": %q}`, ids[0]),
	}
	for i, raw := range variants {
		writePointerRaw(t, a, "s", raw)

		rec, err := a.Latest("s", v1)
		if err != nil {
			t.Fatalf("合法变体 %d 应继续可读: %v\n%s", i, err, raw)
		}
		if rec.ID != ids[0] {
			t.Fatalf("变体 %d 当前标识错误: %s", i, rec.ID)
		}
		infos, err := a.History("s")
		if err != nil {
			t.Fatalf("变体 %d 历史应可读: %v", i, err)
		}
		if len(infos) != len(ids) || infos[0].ID != ids[0] || infos[len(ids)-1].ID != ids[2] {
			t.Fatalf("变体 %d 历史次序错误: %+v", i, infos)
		}
		if rec, err := a.RecoverLatest("s", v1); err != nil || rec.ID != ids[0] {
			t.Fatalf("变体 %d 恢复读取应正常: %+v err=%v", i, rec, err)
		}
		pv, err := a.PreviewRecovery("s", v1)
		if err != nil {
			t.Fatalf("变体 %d 预览应正常: %v", i, err)
		}
		if pv.Current != ids[0] || pv.Source != ids[0] {
			t.Fatalf("变体 %d 预览标识错误: current=%s source=%s", i, pv.Current, pv.Source)
		}
	}

	// 重开后旧裸指针仍只读可用。
	writePointerRaw(t, a, "s", fmt.Sprintf(`{"latest": %q}`, ids[0]))
	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rec, err := a2.Latest("s", v1); err != nil || rec.ID != ids[0] {
		t.Fatalf("重开后合法指针应继续可读: %+v err=%v", rec, err)
	}
}
