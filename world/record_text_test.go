package world

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// rewriteRecordText 把记录文件中的 from 原样替换为 to（to 可含无效 UTF-8
// 字节或不成对的 Unicode 代理项转义），并按替换字符补齐后的解析结果重算
// 内容校验和写回，模拟“损坏文本被替换字符补齐后仍通过校验和检查”的记录：
// 替换后的世界状态合法、校验和匹配，只有按原始字节检查文本编码才能发现。
func rewriteRecordText(t *testing.T, a *Archive, id RecordID, from, to string) {
	t.Helper()
	path := recordPath(a.dir, id)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	oldSum := env.Checksum
	if !strings.Contains(string(raw), from) {
		t.Fatalf("记录 %s 中找不到待替换文本 %q", id, from)
	}
	corrupted := strings.Replace(string(raw), from, to, 1)
	// encoding/json 会把无效字节与不成对的代理项转义改写成替换字符，解析
	// 仍然成功；按解析结果重算校验和，使损坏在校验和下被掩盖。
	var cenv envelope
	if err := json.Unmarshal([]byte(corrupted), &cenv); err != nil {
		t.Fatalf("测试前提：损坏后的记录应仍可被 encoding/json 解析: %v", err)
	}
	corrupted = strings.Replace(corrupted, oldSum, computeChecksum(&cenv), 1)
	if err := os.WriteFile(path, []byte(corrupted), 0o600); err != nil {
		t.Fatal(err)
	}
}

// assertTextCorrupt 断言 err 是保留了被拒绝记录标识、且说明损坏类别
// （文本编码或 Unicode 转义）的 *CorruptError，而不是版本拒绝或其它错误。
func assertTextCorrupt(t *testing.T, err error, id RecordID, keyword string) {
	t.Helper()
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("应为 *CorruptError，得到 %T: %v", err, err)
	}
	if ce.Record != id {
		t.Fatalf("CorruptError 应保留被拒绝的记录标识 %s，得到 %q", id, ce.Record)
	}
	if !strings.Contains(ce.Error(), keyword) {
		t.Fatalf("错误应说明损坏类别 %q: %v", keyword, ce)
	}
	if errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("文本损坏不应报成版本拒绝: %v", err)
	}
}

// TestLatestAndRecordInvalidUTF8Corrupt 记录文件中出现无效 UTF-8 字节时，
// 即使替换字符补齐后的世界状态合法、校验和匹配、规则版本也被接受，Latest
// 与 Record 都必须把整条记录视为损坏，返回保留记录标识并说明文本编码损坏
// 的 *CorruptError；读取保持只读，不修补这份记录。
func TestLatestAndRecordInvalidUTF8Corrupt(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1) // r1 r0
	// 角色标识中的无效字节：替换后是合法的 "he�ro"，状态自洽、校验和匹配。
	rewriteRecordText(t, a, ids[0], `"ID": "hero"`, "\"ID\": \"he\xffro\"")
	before := recordFileBytes(t, a, ids[0])

	_, err := a.Latest("s", []string{"v1"})
	assertTextCorrupt(t, err, ids[0], "UTF-8")

	_, err = a.Record("s", ids[0], []string{"v1"})
	assertTextCorrupt(t, err, ids[0], "UTF-8")

	// 版本不被接受时同样报损坏，不报版本拒绝。
	_, err = a.Latest("s", []string{"other"})
	assertTextCorrupt(t, err, ids[0], "UTF-8")

	// 记录没有被悄悄修补或删除，槽指向不变。
	if got := recordFileBytes(t, a, ids[0]); string(got) != string(before) {
		t.Fatal("读取改写了文本损坏的记录")
	}
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != ids[0] {
		t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
	}

	// 本槽完好的历史记录不受影响，仍可正常读取。
	if _, err := a.Record("s", ids[1], []string{"v1"}); err != nil {
		t.Fatalf("完好的历史记录应可读: %v", err)
	}
}

// TestUnpairedSurrogateEscapeCorrupt 字符串值与对象键中不成对的 Unicode
// 代理项转义（高位未紧接合法低位、低位单独出现）都属于损坏：即使替换后的
// 状态合法、校验和匹配，也一律返回 *CorruptError。
func TestUnpairedSurrogateEscapeCorrupt(t *testing.T) {
	cases := []struct {
		name string
		from string
		to   string
	}{
		{"高位后接普通文字", `"ID": "hero"`, `"ID": "he\uD800ro"`},
		{"高位在字符串末尾", `"ID": "hero"`, `"ID": "her\uD800"`},
		{"高位后接非低位转义", `"ID": "hero"`, `"ID": "he\uD800Ao"`},
		{"低位单独出现", `"ID": "hero"`, `"ID": "he\uDC00ro"`},
		{"对象键中的高位代理项", `"hero": 5`, `"he\uDBFFAro": 5`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			info := saveBase(t, a, "s")
			rewriteRecordText(t, a, info.ID, tc.from, tc.to)
			before := recordFileBytes(t, a, info.ID)

			_, err := a.Latest("s", []string{"v1"})
			assertTextCorrupt(t, err, info.ID, "Unicode")

			_, err = a.Record("s", info.ID, []string{"v1"})
			assertTextCorrupt(t, err, info.ID, "Unicode")

			// 版本不被接受时也报损坏。
			_, err = a.Latest("s", []string{"other"})
			assertTextCorrupt(t, err, info.ID, "Unicode")

			if got := recordFileBytes(t, a, info.ID); string(got) != string(before) {
				t.Fatal("读取改写了转义损坏的记录")
			}
		})
	}
}

// TestHistorySkipsTextCorruptRecords 带历史索引的槽中，文本损坏的记录在
// 浏览时只被略过，其余完好记录仍按保存生效次序列出；恢复读取与恢复预览
// 选择本槽最近一份完整且版本可接受的记录，全部不可用时返回
// ErrUnrecoverable。浏览、读取与预览都不修补、删除或切换任何内容。
func TestHistorySkipsTextCorruptRecords(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2(time 2) r1(time 1) r0(time 0)
	rewriteRecordText(t, a, ids[0], `"ID": "hero"`, `"ID": "he\uD800ro"`)
	before := recordFileBytes(t, a, ids[0])

	// 浏览略过损坏的最新记录，其余按保存生效次序列出。
	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(infos) != 2 || infos[0].ID != ids[1] || infos[1].ID != ids[2] {
		t.Fatalf("损坏记录应被略过，其余按次序列出: %+v", infos)
	}

	// 恢复读取与恢复预览选中最近一份完好且版本可接受的记录。
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("RecoverLatest: %v", err)
	}
	if rec.ID != ids[1] || rec.State.Time != 1 {
		t.Fatalf("应恢复 %s（时间片 1），得到 %s（时间片 %d）", ids[1], rec.ID, rec.State.Time)
	}
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("PreviewRecovery: %v", err)
	}
	if pv.Current != ids[0] || pv.Source != ids[1] {
		t.Fatalf("预览应为 current=%s source=%s，得到 current=%s source=%s",
			ids[0], ids[1], pv.Current, pv.Source)
	}

	// 全部记录都文本损坏时，恢复与预览返回 ErrUnrecoverable。
	rewriteRecordText(t, a, ids[1], `"ID": "hero"`, `"ID": "he\uD800ro"`)
	rewriteRecordText(t, a, ids[2], `"ID": "hero"`, `"ID": "he\uD800ro"`)
	if _, err := a.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("全部损坏应不可恢复，得到 %T: %v", err, err)
	}
	if _, err := a.PreviewRecovery("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("预览同样应不可恢复，得到 %T: %v", err, err)
	}
	if infos, err := a.History("s"); err != nil || len(infos) != 0 {
		t.Fatalf("全部损坏时历史应为成功的空列表: %+v err=%v", infos, err)
	}

	// 全程只读：受损文件未被修补或删除，槽当前指向不变。
	if got := recordFileBytes(t, a, ids[0]); string(got) != string(before) {
		t.Fatal("浏览/恢复改写了文本损坏的记录")
	}
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != ids[0] {
		t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
	}
}

// TestLegalUnicodeTextStillReadable 合法文本的兼容性保留：真实存在的“�”、
// 中文与表情符号、正确配对的代理项转义、同一字符的合法转义写法以及已转义
// 反斜杠后的 uD800 普通文字都正常读回；仅改变缩进或转义写法的记录仍按
// 现有内容校验规则判断，不要求与最初保存的字节完全相同。
func TestLegalUnicodeTextStillReadable(t *testing.T) {
	a, _ := newTestArchive(t)
	w, err := NewWorld(InitialData{
		Seed: 7,
		Rules: Rules{
			Version:     "v1",
			Locations:   []string{"大厅", "院子"},
			Edges:       []Edge{{From: "大厅", To: "院子"}},
			ItemKinds:   []string{"金币"},
			CarryLimits: map[string]int{"英雄😀": 3},
		},
		Characters: []Character{
			{ID: "英雄😀", Location: "大厅", Items: []CharacterItem{{Item: "金币", Count: 1}}},
			// 已转义的反斜杠后跟 uD800 只是普通文字，不是孤立代理项。
			{ID: `名字\uD800`, Location: "院子"},
			// 真实存在的替换字符是合法名称。
			{ID: "替�身", Location: "院子"},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	want := w.Snapshot()
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 把文件中的 😀 改写为正确配对的代理项转义、大厅改写为 \u5927\u5385：
	// 解析内容不变，校验和仍然匹配，记录应照常可读。
	path := recordPath(a.dir, info.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	s = strings.ReplaceAll(s, "😀", `\uD83D\uDE00`)
	s = strings.ReplaceAll(s, "大厅", `\u5927\u5385`)
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}

	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("合法转义写法的记录应可读: %v", err)
	}
	if !reflect.DeepEqual(rec.State, want) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec.State, want)
	}

	// 仅改变缩进与空白也不影响内容校验。
	compact, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(compact, &env); err != nil {
		t.Fatal(err)
	}
	flat, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, flat, 0o600); err != nil {
		t.Fatal(err)
	}
	rec2, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("仅改变格式的记录应可读: %v", err)
	}
	if !reflect.DeepEqual(rec2.State, want) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec2.State, want)
	}
}
