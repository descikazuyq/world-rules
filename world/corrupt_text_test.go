package world

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

// fffdState 是一份全部文本合法、但角色标识中含有真实替换字符“�”
// （U+FFFD，UTF-8 编码为 3 字节 EF BF BD）的完整世界状态。
func fffdState() State {
	return State{
		Seed: 5,
		Rules: Rules{
			Version:   "v1",
			Locations: []string{"hall", "yard"},
			Edges:     []Edge{{From: "hall", To: "yard"}},
			ItemKinds: []string{"gold"},
		},
		Characters: []Character{
			{ID: "替�身", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	}
}

func saveFFFDWorld(t *testing.T, a *Archive, slot string) RecordInfo {
	t.Helper()
	w, err := WorldFromState(fffdState())
	if err != nil {
		t.Fatalf("含合法“�”的状态应被接受: %v", err)
	}
	info, err := a.Save(slot, w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return info
}

// replaceRecordBytes 把记录文件中 from 的第一次出现替换为 to 后写回，
// 用于模拟字节层面的损坏或等价的合法改写。
func replaceRecordBytes(t *testing.T, a *Archive, id RecordID, from, to string) {
	t.Helper()
	path := recordPath(a.dir, id)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), from) {
		t.Fatalf("记录文件中找不到 %q", from)
	}
	out := strings.Replace(string(raw), from, to, 1)
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
}

// assertTextCorrupt 断言 err 是保留了被拒绝记录标识、且 Reason 说明文本
// 损坏类别（hint）的 *CorruptError，而不是版本拒绝或其它错误。
func assertTextCorrupt(t *testing.T, err error, id RecordID, hint string) {
	t.Helper()
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("应为 *CorruptError，得到 %T: %v", err, err)
	}
	if ce.Record != id {
		t.Fatalf("CorruptError 应保留被拒绝的记录标识 %s，得到 %q", id, ce.Record)
	}
	if !strings.Contains(ce.Reason, hint) {
		t.Fatalf("错误应说明文本损坏类别 %q: %v", hint, ce)
	}
	if errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("文本损坏不应报成版本拒绝: %v", err)
	}
}

// assertMaskedByReplacement 断言测试前提成立：若按 encoding/json 的替换
// 语义解析，损坏记录仍会得出合法状态且内容校验和匹配——即没有文本完整性
// 检查时损坏确实会被掩盖。
func assertMaskedByReplacement(t *testing.T, a *Archive, id RecordID) {
	t.Helper()
	raw := recordFileBytes(t, a, id)
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("测试前提: 替换语义下记录应可解析: %v", err)
	}
	if got := computeChecksum(&env); got != env.Checksum {
		t.Fatal("测试前提: 替换语义下内容校验和应仍匹配")
	}
	if err := validateInitialData(InitialData{
		Seed:       env.State.Seed,
		Rules:      env.State.Rules,
		Characters: env.State.Characters,
	}); err != nil {
		t.Fatalf("测试前提: 替换语义下世界状态应合法: %v", err)
	}
}

// TestLatestRecordInvalidUTF8Corrupt 记录文件中的无效 UTF-8 字节会被
// encoding/json 悄悄改写成“�”；原记录本就含合法“�”时替换后的文本甚至
// 仍通过校验和检查。读取必须把整条记录按损坏拒绝：Latest 与 Record 都返回
// 保留记录标识、说明文本编码损坏的 *CorruptError；即使版本不被接受也报
// 损坏而非版本拒绝；读取保持只读。
func TestLatestRecordInvalidUTF8Corrupt(t *testing.T) {
	a, _ := newTestArchive(t)
	info := saveFFFDWorld(t, a, "s")

	// 把“�”的 3 字节 UTF-8 编码换成一个无效字节：替换语义下恰好得到
	// 原来的“�”，校验和与状态校验都会被蒙混过去。
	replaceRecordBytes(t, a, info.ID, "�", "\xff")
	assertMaskedByReplacement(t, a, info.ID)
	before := recordFileBytes(t, a, info.ID)

	_, err := a.Latest("s", []string{"v1"})
	assertTextCorrupt(t, err, info.ID, "UTF-8")

	_, err = a.Record("s", info.ID, []string{"v1"})
	assertTextCorrupt(t, err, info.ID, "UTF-8")

	// 版本不被接受时同样报损坏，不报版本拒绝。
	_, err = a.Latest("s", []string{"other"})
	assertTextCorrupt(t, err, info.ID, "UTF-8")
	_, err = a.Record("s", info.ID, []string{"other"})
	assertTextCorrupt(t, err, info.ID, "UTF-8")

	// 读取只读：受损文件原样保留，槽指向不变。
	if got := recordFileBytes(t, a, info.ID); string(got) != string(before) {
		t.Fatal("读取改写了受损记录")
	}
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != info.ID {
		t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
	}
}

// TestLatestRecordUnpairedSurrogateCorrupt 字符串值或对象键中不成对的
// Unicode 代理项转义（高位未紧跟合法低位、低位单独出现）同样会被改写成
// “�”而可能蒙混通过校验和；读取必须按损坏拒绝并说明是 Unicode 转义损坏。
func TestLatestRecordUnpairedSurrogateCorrupt(t *testing.T) {
	cases := []struct {
		name string
		to   string
	}{
		{"高位代理项未紧跟低位", `\uD800`},
		{"高位代理项后是普通字符", `\uDBFF`},
		{"低位代理项单独出现", `\uDC00`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			info := saveFFFDWorld(t, a, "s")

			// 把“�”改写成不成对的代理项转义：替换语义下又得到“�”，
			// 校验和与状态校验都会被蒙混过去。
			replaceRecordBytes(t, a, info.ID, "�", tc.to)
			assertMaskedByReplacement(t, a, info.ID)
			before := recordFileBytes(t, a, info.ID)

			_, err := a.Latest("s", []string{"v1"})
			assertTextCorrupt(t, err, info.ID, "代理项")
			_, err = a.Record("s", info.ID, []string{"v1"})
			assertTextCorrupt(t, err, info.ID, "代理项")
			// 版本不被接受时同样报损坏。
			_, err = a.Latest("s", nil)
			assertTextCorrupt(t, err, info.ID, "代理项")

			if got := recordFileBytes(t, a, info.ID); string(got) != string(before) {
				t.Fatal("读取改写了受损记录")
			}
			if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != info.ID {
				t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
			}
		})
	}

	t.Run("对象键中的孤立代理项", func(t *testing.T) {
		a, _ := newTestArchive(t)
		st := fffdState()
		st.Characters[0].ID = "hero"
		st.Rules.CarryLimits = map[string]int{"上限�表": 3}
		w, err := WorldFromState(st)
		if err != nil {
			t.Fatal(err)
		}
		info, err := a.Save("s", w)
		if err != nil {
			t.Fatal(err)
		}
		replaceRecordBytes(t, a, info.ID, "�", `\uD800`)
		assertMaskedByReplacement(t, a, info.ID)

		_, err = a.Latest("s", []string{"v1"})
		assertTextCorrupt(t, err, info.ID, "代理项")
		_, err = a.Record("s", info.ID, []string{"v1"})
		assertTextCorrupt(t, err, info.ID, "代理项")
	})
}

// TestCheckRecordText 直接检查文本完整性扫描：无效 UTF-8 与不成对的代理项
// 转义被拒绝；合法文本、合法转义、配对代理项与“转义反斜杠后的普通文字”
// 都正常通过。
func TestCheckRecordText(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"合法文本", `{"s":"大厅😀�"}`, false},
		{"无效UTF8字节", "{\"s\":\"a\xff\"}", true},
		{"孤立高位代理项", `{"s":"\uD800"}`, true},
		{"孤立低位代理项", `{"s":"\uDFFF"}`, true},
		{"高位后接非低位转义", `{"s":"\uD800A"}`, true},
		{"高位后接另一个高位", `{"s":"\uD800\uD801"}`, true},
		{"配对代理项", `{"s":"\uD83D\uDE00"}`, false},
		{"小写十六进制配对", `{"s":"\ud83d\ude00"}`, false},
		{"转义反斜杠后是普通文字", `{"s":"\\uD800"}`, false},
		{"合法转义替换符", `{"s":"\ufffd"}`, false},
		{"对象键中的孤立代理项", `{"\uD800":1}`, true},
		{"对象键中的配对代理项", `{"\uD83D\uDE00":1}`, false},
		{"高位代理项在字符串末尾", `{"s":"x\uD800"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkRecordText([]byte(tc.in))
			if tc.wantErr && err == nil {
				t.Fatalf("应拒绝 %s", tc.in)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("应接受 %s: %v", tc.in, err)
			}
		})
	}
}

// TestHistoryAndRecoverSkipTextCorruptRecords 带历史索引的槽中，文本损坏的
// 记录在浏览时被略过，其余完好记录仍按保存生效次序返回；恢复读取与恢复预览
// 选择本槽最近一份完整且版本可接受的记录，全部不可用时返回 ErrUnrecoverable；
// 全程只读，受损文件、槽指向与历史不被修补、删除或切换。
func TestHistoryAndRecoverSkipTextCorruptRecords(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // ids[0] 最新 … ids[2] 槽首

	// 最新记录注入无效 UTF-8 字节，中间记录注入孤立代理项转义。
	replaceRecordBytes(t, a, ids[0], `"v1"`, "\"v\xff1\"")
	replaceRecordBytes(t, a, ids[1], `"v1"`, `"v\uD800"`)
	before0 := recordFileBytes(t, a, ids[0])
	before1 := recordFileBytes(t, a, ids[1])

	// 浏览略过两条受损记录，槽首记录仍在，次序不变。
	hist, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 || hist[0].ID != ids[2] || !hist[0].SlotFirst {
		t.Fatalf("浏览应略过受损记录、保留完好的槽首记录: %+v", hist)
	}

	// 恢复读取与预览选中本槽最近一份完好且版本可接受的记录（槽首）。
	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("RecoverLatest: %v", err)
	}
	if rec.ID != ids[2] {
		t.Fatalf("应恢复到槽首记录 %s，得到 %s", ids[2], rec.ID)
	}
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("PreviewRecovery: %v", err)
	}
	if pv.Current != ids[0] || pv.Source != ids[2] {
		t.Fatalf("预览应为 current=%s source=%s，得到 current=%s source=%s",
			ids[0], ids[2], pv.Current, pv.Source)
	}

	// 全部记录都文本损坏后，恢复与预览返回 ErrUnrecoverable。
	replaceRecordBytes(t, a, ids[2], `"v1"`, `"v\uDC00"`)
	if _, err := a.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("全部受损时应返回 ErrUnrecoverable，得到 %T: %v", err, err)
	}
	if _, err := a.PreviewRecovery("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("全部受损时预览应返回 ErrUnrecoverable，得到 %T: %v", err, err)
	}
	if hist, err := a.History("s"); err != nil || len(hist) != 0 {
		t.Fatalf("全部受损时浏览应返回空列表: %+v err=%v", hist, err)
	}

	// 全程只读：受损文件原样保留，槽指向与历史不被切换或删除。
	if got := recordFileBytes(t, a, ids[0]); string(got) != string(before0) {
		t.Fatal("读取改写了受损记录")
	}
	if got := recordFileBytes(t, a, ids[1]); string(got) != string(before1) {
		t.Fatal("读取改写了受损记录")
	}
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != ids[0] || len(p.History) != 3 {
		t.Fatalf("槽指向与历史不应改变: %+v err=%v", p, err)
	}
	if got := recordCount(t, a); got != 3 {
		t.Fatalf("记录文件不应被删除: %d", got)
	}
}

// TestLegitTextEscapesRoundTrip 合法文本的兼容性保留：真实“�”的合法转义
// 写法、正确配对的代理项转义、同一字符的合法转义、以及仅改变缩进空白的
// 记录都按现有内容校验规则正常读回，文本内容与角色、物品排列不变。
func TestLegitTextEscapesRoundTrip(t *testing.T) {
	t.Run("替换符的合法转义写法", func(t *testing.T) {
		a, _ := newTestArchive(t)
		info := saveFFFDWorld(t, a, "s")
		// 把字面“�”改写成等价的合法转义：内容不变，仍应读回。
		replaceRecordBytes(t, a, info.ID, "�", `\ufffd`)
		rec, err := a.Latest("s", []string{"v1"})
		if err != nil {
			t.Fatalf("合法转义写法应可读: %v", err)
		}
		if !reflect.DeepEqual(rec.State, fffdState()) {
			t.Fatalf("读回状态不一致:\n got %+v\nwant %+v", rec.State, fffdState())
		}
	})

	t.Run("配对代理项转义", func(t *testing.T) {
		a, _ := newTestArchive(t)
		st := fffdState()
		st.Characters[0].ID = "法师🧙"
		w, err := WorldFromState(st)
		if err != nil {
			t.Fatal(err)
		}
		info, err := a.Save("s", w)
		if err != nil {
			t.Fatal(err)
		}
		// 把表情符号改写成等价的配对代理项转义。
		pair := utf16.Encode([]rune{'🧙'})
		esc := fmt.Sprintf(`\u%04X\u%04X`, pair[0], pair[1])
		replaceRecordBytes(t, a, info.ID, "🧙", esc)
		rec, err := a.Latest("s", []string{"v1"})
		if err != nil {
			t.Fatalf("配对代理项转义应可读: %v", err)
		}
		if !reflect.DeepEqual(rec.State, st) {
			t.Fatalf("读回状态不一致:\n got %+v\nwant %+v", rec.State, st)
		}
	})

	t.Run("转义反斜杠后的uD800是普通文字", func(t *testing.T) {
		a, _ := newTestArchive(t)
		st := fffdState()
		st.Characters[0].ID = `hero\uD800` // 反斜杠 + uD800 只是普通文字
		w, err := WorldFromState(st)
		if err != nil {
			t.Fatal(err)
		}
		info, err := a.Save("s", w)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(recordFileBytes(t, a, info.ID)), `\\uD800`) {
			t.Fatal("测试前提: 记录文件应包含转义后的反斜杠序列")
		}
		rec, err := a.Latest("s", []string{"v1"})
		if err != nil {
			t.Fatalf("含普通文字 \\uD800 的记录应可读: %v", err)
		}
		if !reflect.DeepEqual(rec.State, st) {
			t.Fatalf("读回状态不一致:\n got %+v\nwant %+v", rec.State, st)
		}
	})

	t.Run("同一字符的合法转义与重新排版", func(t *testing.T) {
		a, _ := newTestArchive(t)
		info := saveFFFDWorld(t, a, "s")
		// 同一字符的合法转义写法：h 写成 \u0068，内容不变。
		replaceRecordBytes(t, a, info.ID, `"hall"`, `"\u0068all"`)
		// 仅改变缩进与空白：按现有内容校验规则仍应通过。
		raw := recordFileBytes(t, a, info.ID)
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		compact, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(recordPath(a.dir, info.ID), compact, 0o600); err != nil {
			t.Fatal(err)
		}
		rec, err := a.Latest("s", []string{"v1"})
		if err != nil {
			t.Fatalf("仅改变转义写法与排版的记录应可读: %v", err)
		}
		if !reflect.DeepEqual(rec.State, fffdState()) {
			t.Fatalf("读回状态不一致:\n got %+v\nwant %+v", rec.State, fffdState())
		}
	})
}
