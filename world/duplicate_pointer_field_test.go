package world

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// writeSlotPointerText 用给定原始字节覆盖槽指针文件，模拟外部写入器留下的
// 指针文本。
func writeSlotPointerText(t *testing.T, a *Archive, slot, text string) {
	t.Helper()
	if err := os.WriteFile(a.slotPath(slot), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// historyFragment 把记录标识拼成 JSON 数组片段。
func historyFragment(ids []RecordID) string {
	s := "["
	for i, id := range ids {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%q", string(id))
	}
	return s + "]"
}

// assertSlotPointerDuplicateCorrupt 断言 err 是带槽名、且明确说明槽指针
// 字段重复的 *CorruptError：不能是版本拒绝、不可恢复或其它错误，也不允许
// 返回记录、历史列表或恢复来源。
func assertSlotPointerDuplicateCorrupt(t *testing.T, err error, slot string) {
	t.Helper()
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("应为 *CorruptError，得到 %T: %v", err, err)
	}
	if ce.Slot != slot {
		t.Fatalf("CorruptError 应带槽名 %q，得到 %+v", slot, ce)
	}
	if !strings.Contains(ce.Reason, "重复") {
		t.Fatalf("错误原因应明确说明槽指针字段重复: %v", ce)
	}
	if errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("字段重复不应改报为版本不被接受: %v", err)
	}
	if errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("字段重复不应改报为没有可恢复记录: %v", err)
	}
}

// TestSlotPointerDuplicateFieldReadEntries 槽指针顶层同一对象内两个名称会被
// 解释为同一个 latest/history 字段时（完全同名、只改大小写、Unicode 转义
// 还原后同名、交换先后顺序），整个指针视为损坏：所有读取/浏览/恢复入口都
// 返回带槽名、说明字段重复的 *CorruptError，不返回记录或历史列表。
func TestSlotPointerDuplicateFieldReadEntries(t *testing.T) {
	// 每个案例的文本都能被 encoding/json 正常解析，且解码后 latest 非空、
	// 所指记录全部完好——重复本身是唯一的问题。
	cases := []struct {
		name  string
		build func(ids []RecordID) string
	}{
		{"latest同名异值", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "latest": %q, "history": %s}`,
				ids[2], ids[0], historyFragment(ids))
		}},
		{"latest同名同值", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "latest": %q, "history": %s}`,
				ids[0], ids[0], historyFragment(ids))
		}},
		{"latest交换先后顺序", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "LATEST": %q, "history": %s}`,
				ids[0], ids[2], historyFragment(ids))
		}},
		{"latest大小写变体", func(ids []RecordID) string {
			return fmt.Sprintf(`{"Latest": %q, "latest": %q, "history": %s}`,
				ids[2], ids[0], historyFragment(ids))
		}},
		{"latest大小写变体在前", func(ids []RecordID) string {
			return fmt.Sprintf(`{"LATEST": %q, "latest": %q, "history": %s}`,
				ids[0], ids[2], historyFragment(ids))
		}},
		// 006c 还原为小写 l：转义键 "latest" 还原为 "latest"，与直接
		// 写法读取为同一字段。
		{"latestUnicode转义", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "\u006catest": %q, "history": %s}`,
				ids[2], ids[0], historyFragment(ids))
		}},
		// 004c 还原为大写 L：转义键还原后是 "Latest"，与 latest 大小写不敏感
		// 归并；且直接写法在前、转义形式在后，交换次序仍算重复。
		{"latestUnicode转义大写在后", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "\u004catest": %q, "history": %s}`,
				ids[0], ids[2], historyFragment(ids))
		}},
		{"latest一份为空", func(ids []RecordID) string {
			// 解码结果非空（后一个值生效），仍必须报字段重复而非“指针为空”。
			return fmt.Sprintf(`{"latest": "", "latest": %q, "history": %s}`,
				ids[0], historyFragment(ids))
		}},
		{"latest后一个值为空", func(ids []RecordID) string {
			// 解码结果为空（后一个值生效）：仍先报字段重复，不能改报“指针为空”。
			return fmt.Sprintf(`{"latest": %q, "latest": "", "history": %s}`,
				ids[0], historyFragment(ids))
		}},
		{"history同名两份", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s, "history": %s}`,
				ids[0], historyFragment([]RecordID{ids[0]}), historyFragment(ids))
		}},
		{"history交换先后顺序", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s, "HISTORY": %s}`,
				ids[0], historyFragment(ids), historyFragment([]RecordID{ids[0]}))
		}},
		{"history一份为空", func(ids []RecordID) string {
			// 先空后全：解码得到完整历史，仍必须拒绝。
			return fmt.Sprintf(`{"latest": %q, "history": [], "history": %s}`,
				ids[0], historyFragment(ids))
		}},
		{"history后一份更完整", func(ids []RecordID) string {
			// 先一份残缺、后一份完整：更完整也不能豁免。
			return fmt.Sprintf(`{"latest": %q, "history": %s, "history": %s}`,
				ids[0], historyFragment([]RecordID{ids[2]}), historyFragment(ids))
		}},
		{"history大小写变体", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "History": %s, "history": %s}`,
				ids[0], historyFragment(ids), historyFragment(ids))
		}},
		// h 还原为小写 h。
		{"historyUnicode转义", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s, "\u0068istory": %s}`,
				ids[0], historyFragment(ids), historyFragment(ids))
		}},
		{"history后一个为null", func(ids []RecordID) string {
			// json 对 null 采用“null 生效”的优先级，解码后历史索引为空，
			// 旧逻辑会退回父链重建；字段重复仍必须先拒绝。
			return fmt.Sprintf(`{"latest": %q, "history": %s, "history": null}`,
				ids[0], historyFragment(ids))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			ids := saveN(t, a, "s", 2) // ids0 最新，全部记录完好
			writeSlotPointerText(t, a, "s", tc.build(ids))
			before := recordFileBytes(t, a, ids[0])
			ptrBefore := slotFileBytes(t, a, "s")

			_, err := a.Latest("s", []string{"v1"})
			assertSlotPointerDuplicateCorrupt(t, err, "s")

			_, err = a.Record("s", ids[1], []string{"v1"})
			assertSlotPointerDuplicateCorrupt(t, err, "s")

			// 即使规则版本不被接受，也先报指针损坏。
			_, err = a.Latest("s", []string{"other"})
			assertSlotPointerDuplicateCorrupt(t, err, "s")

			infos, err := a.History("s")
			assertSlotPointerDuplicateCorrupt(t, err, "s")
			if infos != nil {
				t.Fatalf("损坏指针不应返回历史列表: %+v", infos)
			}

			_, err = a.RecoverLatest("s", []string{"v1"})
			assertSlotPointerDuplicateCorrupt(t, err, "s")

			pv, err := a.PreviewRecovery("s", []string{"v1"})
			assertSlotPointerDuplicateCorrupt(t, err, "s")
			if pv.Source != "" || pv.Current != "" {
				t.Fatalf("损坏指针不应返回恢复来源: %+v", pv)
			}

			_, err = a.CheckUpgrade("s", []string{"v1"}, v2Rules())
			assertSlotPointerDuplicateCorrupt(t, err, "s")

			_, err = a.PreviewMigration("s", []string{"v1"}, v2Rules(), nil, nil)
			assertSlotPointerDuplicateCorrupt(t, err, "s")

			// 只读：指针与记录原样保留，不被自动修补。
			if got := slotFileBytes(t, a, "s"); string(got) != string(ptrBefore) {
				t.Fatal("读取改写了含重复字段的槽指针")
			}
			if got := recordFileBytes(t, a, ids[0]); string(got) != string(before) {
				t.Fatal("读取改写了已有记录")
			}
		})
	}
}

// TestSlotPointerDuplicateFieldWriteEntries 依赖既有指针承接当前记录与历史的
// 写入操作遇到重复字段指针时一律拒绝保存：不创建新记录、不修补指针，指针与
// 已有记录原样保留；冲突判断与来源校验之后的入口也要在任何写入前先报损坏。
func TestSlotPointerDuplicateFieldWriteEntries(t *testing.T) {
	type op struct {
		name string
		run  func(t *testing.T, a *Archive, ids []RecordID)
	}
	ops := []op{
		{"同名首存", func(t *testing.T, a *Archive, ids []RecordID) {
			_, err := a.Save("s", baseWorld(t))
			assertSlotPointerDuplicateCorrupt(t, err, "s")
		}},
		{"覆盖", func(t *testing.T, a *Archive, ids []RecordID) {
			// 即使预期标识恰好等于解码后的 latest，也不能通过。
			_, err := a.Replace("s", baseWorld(t), ids[0])
			assertSlotPointerDuplicateCorrupt(t, err, "s")
		}},
		{"覆盖预期其它标识", func(t *testing.T, a *Archive, ids []RecordID) {
			_, err := a.Replace("s", baseWorld(t), ids[2])
			assertSlotPointerDuplicateCorrupt(t, err, "s")
		}},
		{"升级", func(t *testing.T, a *Archive, ids []RecordID) {
			_, err := a.Upgrade("s", []string{"v1"}, v2Rules(), ids[0])
			assertSlotPointerDuplicateCorrupt(t, err, "s")
		}},
		{"迁移", func(t *testing.T, a *Archive, ids []RecordID) {
			_, err := a.Migrate("s", []string{"v1"}, v2Rules(), nil, nil, ids[0])
			assertSlotPointerDuplicateCorrupt(t, err, "s")
		}},
		{"确认恢复", func(t *testing.T, a *Archive, ids []RecordID) {
			_, err := a.ConfirmRecovery("s", ids[0], ids[1], []string{"v1"})
			assertSlotPointerDuplicateCorrupt(t, err, "s")
		}},
		{"分支", func(t *testing.T, a *Archive, ids []RecordID) {
			_, err := a.Branch("s", ids[1], "b")
			assertSlotPointerDuplicateCorrupt(t, err, "s")
			// 分支目标不得被创建。
			if _, err := a.readLatestLocked("b"); !errors.As(err, new(*NotFoundError)) {
				t.Fatalf("被拒绝的分支不应创建目标槽: %v", err)
			}
		}},
	}
	for _, tc := range ops {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			ids := saveN(t, a, "s", 2)
			// 两个 latest 都指向本槽存在且完好的记录，所指记录校验通过。
			writeSlotPointerText(t, a, "s",
				fmt.Sprintf(`{"latest": %q, "latest": %q, "history": %s}`,
					ids[2], ids[0], historyFragment(ids)))
			ptrBefore := slotFileBytes(t, a, "s")
			recordsBefore := recordCount(t, a)

			tc.run(t, a, ids)

			if got := recordCount(t, a); got != recordsBefore {
				t.Fatalf("被拒绝的写入不应产生新记录: %d -> %d", recordsBefore, got)
			}
			if got := slotFileBytes(t, a, "s"); string(got) != string(ptrBefore) {
				t.Fatal("被拒绝的写入修补了含重复字段的槽指针")
			}
		})
	}
}

// TestSlotPointerDuplicateHistoryWriteEntries history 字段重复时写入入口同样
// 拒绝：不能按任意一份 history 承接历史，也不能借重建沿旧记录继续。
func TestSlotPointerDuplicateHistoryWriteEntries(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	writeSlotPointerText(t, a, "s",
		fmt.Sprintf(`{"latest": %q, "history": %s, "history": %s}`,
			ids[0], historyFragment(ids), historyFragment([]RecordID{ids[0]})))
	ptrBefore := slotFileBytes(t, a, "s")
	recordsBefore := recordCount(t, a)

	if _, err := a.Replace("s", baseWorld(t), ids[0]); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("history 重复应拒绝覆盖，得到 %T: %v", err, err)
	}
	if _, err := a.ConfirmRecovery("s", ids[0], ids[1], []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("history 重复应拒绝确认恢复，得到 %T: %v", err, err)
	}
	if got := recordCount(t, a); got != recordsBefore {
		t.Fatalf("被拒绝的写入不应产生新记录: %d -> %d", recordsBefore, got)
	}
	if got := slotFileBytes(t, a, "s"); string(got) != string(ptrBefore) {
		t.Fatal("被拒绝的写入修补了含重复字段的槽指针")
	}
}

// TestSlotPointerSingleVariantStillReadable latest/history 只出现一次时，
// 现有读取方式本来就接受的大小写与 Unicode 转义写法继续可读，读回内容与
// 标准写法一致。
func TestSlotPointerSingleVariantStillReadable(t *testing.T) {
	cases := []struct {
		name  string
		build func(ids []RecordID) string
	}{
		{"顶层字段大写变体", func(ids []RecordID) string {
			return fmt.Sprintf(`{"Latest": %q, "History": %s}`, ids[0], historyFragment(ids))
		}},
		{"latest转义写法", func(ids []RecordID) string {
			return fmt.Sprintf(`{"\u006catest": %q, "history": %s}`, ids[0], historyFragment(ids))
		}},
		{"history转义大写写法", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "\u0048istory": %s}`, ids[0], historyFragment(ids))
		}},
		{"旧版本仅latest", func(ids []RecordID) string {
			return fmt.Sprintf(`{"Latest": %q}`, ids[0])
		}},
		{"标准写法", func(ids []RecordID) string {
			return fmt.Sprintf(`{"latest": %q, "history": %s}`, ids[0], historyFragment(ids))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			ids := saveN(t, a, "s", 2)
			writeSlotPointerText(t, a, "s", tc.build(ids))

			rec, err := a.Latest("s", []string{"v1"})
			if err != nil {
				t.Fatalf("只出现一次的可接受写法应继续可读: %v", err)
			}
			if rec.ID != ids[0] {
				t.Fatalf("应读到当前记录 %s，得到 %s", ids[0], rec.ID)
			}
			infos, err := a.History("s")
			if err != nil {
				t.Fatalf("History: %v", err)
			}
			if len(infos) != 3 || infos[0].ID != ids[0] || infos[2].ID != ids[2] {
				t.Fatalf("历史次序错误: %+v", infos)
			}
			old, err := a.Record("s", ids[2], []string{"v1"})
			if err != nil {
				t.Fatalf("最早的历史记录应可读: %v", err)
			}
			if old.ID != ids[2] {
				t.Fatalf("读错记录: %s", old.ID)
			}
		})
	}
}

// TestSlotPointerMissingHistoryNotDuplicate 缺少 history 是合法的旧版本指针，
// 不是重复：继续沿校验通过的父链重建历史。
func TestSlotPointerMissingHistoryNotDuplicate(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	writeLegacyPointer(t, a, "s", ids[0])

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("旧版本仅 latest 的指针应继续可用: %v", err)
	}
	if rec.ID != ids[0] {
		t.Fatalf("应恢复最新记录 %s，得到 %s", ids[0], rec.ID)
	}
}

// TestSlotPointerDuplicateIDsInHistoryUnchanged history 数组里同一记录标识
// 出现多次与字段名写两次是不同情况：原有按首次出现次序的去重保持不变。
func TestSlotPointerDuplicateIDsInHistoryUnchanged(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	// latest 与 history 首项相同、history 内部也重复一次 ids[1]。
	writeSlotPointerText(t, a, "s",
		fmt.Sprintf(`{"latest": %q, "history": [%q, %q, %q, %q]}`,
			ids[0], ids[0], ids[1], ids[1], ids[2]))

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("数组内标识重复不应让指针损坏: %v", err)
	}
	want := []RecordID{ids[0], ids[1], ids[2]}
	if len(infos) != len(want) {
		t.Fatalf("应按去重后的 3 条列出，得到 %+v", infos)
	}
	for i, w := range want {
		if infos[i].ID != w {
			t.Fatalf("去重后次序应为 %v，得到 %+v", want, infos)
		}
	}
}

// TestSlotPointerDuplicateFieldNestedObjectIgnored 重复判定只针对指针顶层
// 对象：未登记的多余字段值（嵌套对象/数组）内部即使出现同名键，也不会被
// 现有读取方式解释为 latest/history，指针仍可读。
func TestSlotPointerDuplicateFieldNestedObjectIgnored(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1)
	writeSlotPointerText(t, a, "s",
		fmt.Sprintf(`{"latest": %q, "history": %s, "extra": {"latest": 1, "latest": 2, "nested": [{"history": [], "history": []}]}}`,
			ids[0], historyFragment(ids)))

	if _, err := a.Latest("s", []string{"v1"}); err != nil {
		t.Fatalf("嵌套对象内的同名键不应判为指针字段重复: %v", err)
	}
	if _, err := a.History("s"); err != nil {
		t.Fatalf("嵌套对象内的同名键不应影响浏览: %v", err)
	}
}

// TestSlotPointerLegalButRecordCorruptSkipsAsBefore 合法的带历史索引指针下
// 单条记录损坏时，浏览与恢复的原有略过规则不变——字段重复检查只作用于
// 指针本身，不改变记录级别的容错。
func TestSlotPointerLegalButRecordCorruptSkipsAsBefore(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2)
	corruptChecksum(t, a.dir, ids[0])

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(infos) != 2 || infos[0].ID != ids[1] || infos[1].ID != ids[2] {
		t.Fatalf("损坏的最新记录应被略过，其余按次序列出: %+v", infos)
	}
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应越过损坏的最新记录恢复: %v", err)
	}
	if rec.ID != ids[1] {
		t.Fatalf("应恢复 %s，得到 %s", ids[1], rec.ID)
	}
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("PreviewRecovery: %v", err)
	}
	if pv.Current != ids[0] || pv.Source != ids[1] {
		t.Fatalf("预览 current=%s source=%s 错误: %+v", ids[0], ids[1], pv)
	}
}

// TestSlotPointerDuplicateFieldOtherSlotAndOpenUnaffected 一个槽的指针损坏
// 不影响打开存档目录，也不影响其他正常槽的读取与保存。
func TestSlotPointerDuplicateFieldOtherSlotAndOpenUnaffected(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 1)
	tids := saveN(t, a, "t", 1)
	writeSlotPointerText(t, a, "s",
		fmt.Sprintf(`{"latest": %q, "latest": %q}`, ids[0], ids[0]))

	a2, err := Open(dir)
	if err != nil {
		t.Fatalf("存档目录应照常打开: %v", err)
	}
	rec, err := a2.Latest("t", []string{"v1"})
	if err != nil {
		t.Fatalf("其他正常槽应照常读取: %v", err)
	}
	if rec.ID != tids[0] {
		t.Fatalf("读错其他槽的记录: %s", rec.ID)
	}
	w := baseWorld(t)
	if _, err := w.Apply(Commit{Time: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := a2.Replace("t", w, tids[0]); err != nil {
		t.Fatalf("其他正常槽应能继续保存: %v", err)
	}
	if _, err := a2.Latest("s", []string{"v1"}); !errors.As(err, new(*CorruptError)) {
		t.Fatalf("损坏槽仍应报损坏，得到 %T: %v", err, err)
	}
}

// slotFileBytes 读取槽指针文件原始字节。
func slotFileBytes(t *testing.T, a *Archive, slot string) []byte {
	t.Helper()
	b, err := os.ReadFile(a.slotPath(slot))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
