package world

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// duplicatePatch 描述一次重复键注入：在 anchor 前插入 `"key": value, `
// （可选把新键名整体写成 \uXXXX 转义），锚点必须落在目标对象内部。
type duplicatePatch struct {
	anchor    string
	key       string
	value     any
	escapeKey bool
}

// duplicateNameRecordBytes 复制一条已有记录的文本，在 anchor 前插入一个与
// 已有字段同名的键，再按 encoding/json“后值覆盖前值”后的解析结果重算
// 校验和（只替换校验和字符串，保留注入的重复键文本），模拟一份可正常
// 解析、采用后值后世界合法、校验和匹配，但同一对象内写着两份相互矛盾
// 内容的记录。
//
// escapeKey 为 true 时把新键名整体写成 \uXXXX 转义：这样构造出的文本
// 依旧合法、校验和匹配，只有按转义还原后的名称判重才能发现。
func duplicateNameRecordBytes(t *testing.T, raw []byte, p duplicatePatch) []byte {
	t.Helper()
	var orig envelope
	if err := json.Unmarshal(raw, &orig); err != nil {
		t.Fatal(err)
	}
	rawS := strings.TrimRight(string(raw), "\n")
	if !strings.Contains(rawS, p.anchor) {
		t.Fatalf("记录中找不到待插入位置锚点 %q", p.anchor)
	}
	keyLit := `"` + p.key + `"`
	if p.escapeKey {
		var b strings.Builder
		b.WriteByte('"')
		for _, r := range p.key {
			fmt.Fprintf(&b, `\u%04X`, r)
		}
		b.WriteByte('"')
		keyLit = b.String()
	}
	valBytes, err := json.Marshal(p.value)
	if err != nil {
		t.Fatal(err)
	}
	pair := keyLit + ": " + string(valBytes) + ", "
	out := strings.Replace(rawS, p.anchor, pair+p.anchor, 1)

	// 注入的第二个同名字段按“后值覆盖前值”确定解码内容；记录必须仍可被
	// encoding/json 正常解析，且按该内容重算校验和后与校验和匹配。
	var cenv envelope
	if err := json.Unmarshal([]byte(out), &cenv); err != nil {
		t.Fatalf("测试前提：注入重复键后的记录应仍可被 encoding/json 解析: %v", err)
	}
	out = strings.Replace(out, orig.Checksum, computeChecksum(&cenv), 1)
	return []byte(out)
}

// writeDuplicateNameRecord 用重复键文本覆盖给定记录文件并返回该文本。
func writeDuplicateNameRecord(t *testing.T, a *Archive, id RecordID, p duplicatePatch) []byte {
	t.Helper()
	raw, err := os.ReadFile(recordPath(a.dir, id))
	if err != nil {
		t.Fatal(err)
	}
	out := duplicateNameRecordBytes(t, raw, p)
	if err := os.WriteFile(recordPath(a.dir, id), out, 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertDuplicateNameCorrupt 断言 err 是保留了被拒绝记录标识、且说明名称
// 重复的 *CorruptError，而不是版本拒绝或其它错误。
func assertDuplicateNameCorrupt(t *testing.T, err error, id RecordID) {
	t.Helper()
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("应为 *CorruptError，得到 %T: %v", err, err)
	}
	if ce.Record != id {
		t.Fatalf("CorruptError 应保留被拒绝的记录标识 %s，得到 %q", id, ce.Record)
	}
	if !strings.Contains(ce.Error(), "重复") || !strings.Contains(ce.Error(), "名称") {
		t.Fatalf("错误应说明同一对象内名称重复: %v", ce)
	}
	if errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("名称重复不应报成版本拒绝: %v", err)
	}
}

// TestCheckDuplicateObjectNames 直接校验扫描器的判重语义：同一对象内重复
// 名称（含相同值、Unicode 转义同名、中文键转义同名）一律报错；不同对象
// （含数组中各自的元素对象）同名合法；字符串值中的文字和标点不算名称。
func TestCheckDuplicateObjectNames(t *testing.T) {
	good := []string{
		`{}`,
		`{"a": 1}`,
		`{"a": {"b": 1}, "b": 2}`,
		`[{"a": 1}, {"a": 2}]`,
		`{"items": [{"Count": 1}, {"Count": 2}]}`,
		// 字符串值中的文字、引号、花括号与标点不算对象名称。
		`{"a": "a, a: a { \"a\" }"}`,
		`{"英雄": 1}`,
		// 直接写出的中文键与另一对象中同名键分属不同对象，不冲突。
		`{"x": {"英雄": 1}, "y": {"英雄": 2}}`,
		// 同一字符的合法 Unicode 转义写法，在该对象内只出现一次，合法。
		`{"a": 1, "b": 2}`,
	}
	for i, s := range good {
		if err := checkDuplicateObjectNames([]byte(s)); err != nil {
			t.Fatalf("用例 %d 合法 JSON 被误判: %v\n%s", i, err, s)
		}
	}
	bad := []struct {
		json  string
		match string
	}{
		{`{"a": 1, "a": 2}`, "a"},                    // 两个值不同
		{`{"a": 1, "a": 1}`, "a"},                    // 两个值相同也算
		{"{\"a\": 1, \"\\u0061\": 2}", "a"},          // 直接写出与 Unicode 转义同名
		{"{\"英雄\": 1, \"\\u82F1\\u96C4\": 2}", "英雄"}, // 中文键与其 Unicode 转义同名
		{`{"o": {"x": 1, "x": 2}}`, "x"},             // 嵌套对象内部重复
		{`[{"a": 1, "a": 2}, {"a": 3}]`, "a"},        // 数组元素对象内部重复
		{`{"hero": 5, "hero": 6}`, "hero"},           // 名称到数值的对象
	}
	for i, tc := range bad {
		err := checkDuplicateObjectNames([]byte(tc.json))
		if err == nil {
			t.Fatalf("用例 %d 应判重复: %s", i, tc.json)
		}
		if !strings.Contains(err.Error(), "重复") || !strings.Contains(err.Error(), tc.match) {
			t.Fatalf("用例 %d 错误应指出名称 %q 重复: %v", i, tc.match, err)
		}
	}
}

// TestDuplicateNameLatestAndRecordCorrupt 同一对象内名称第二次出现时，即使
// 两个值相同或采用后值后世界合法、校验和也按后值重算匹配，Latest 与
// Record 都必须把整条记录视为损坏：覆盖记录顶层、世界规则、角色与物品
// 条目，以及携带上限这些名称到值的对象。
func TestDuplicateNameLatestAndRecordCorrupt(t *testing.T) {
	cases := []struct {
		name  string
		patch duplicatePatch
	}{
		{
			name: "顶层先负时间片后合法时间片",
			patch: duplicatePatch{
				anchor: `"Time": 0,`,
				key:    "Time",
				value:  -3,
			},
		},
		{
			name: "顶层同名字段两个值相同",
			patch: duplicatePatch{
				anchor: `"Time": 0,`,
				key:    "Time",
				value:  0,
			},
		},
		{
			name: "顶层时间片键写成Unicode转义",
			patch: duplicatePatch{
				anchor:    `"Time": 0,`,
				key:       "Time",
				value:     0,
				escapeKey: true,
			},
		},
		{
			name: "规则对象内重复版本字段",
			patch: duplicatePatch{
				anchor: `"Version": "v1",`,
				key:    "Version",
				value:  "v1",
			},
		},
		{
			name: "角色对象内重复地点字段",
			patch: duplicatePatch{
				anchor: `"Location": "hall",`,
				key:    "Location",
				value:  "hall",
			},
		},
		{
			name: "物品条目内重复数量字段",
			patch: duplicatePatch{
				anchor: `"Count": 1`,
				key:    "Count",
				value:  1,
			},
		},
		{
			name: "携带上限对象内重复角色键",
			patch: duplicatePatch{
				anchor:    `"hero": 5,`,
				key:       "hero",
				value:     5,
				escapeKey: true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			info := saveBase(t, a, "s")
			out := writeDuplicateNameRecord(t, a, info.ID, tc.patch)

			// 矛盾内容被后值掩盖：直接解码得到的是合法状态且校验和匹配。
			var probe envelope
			if err := json.Unmarshal(out, &probe); err != nil {
				t.Fatalf("测试前提：记录应可解析: %v", err)
			}
			if computeChecksum(&probe) != probe.Checksum {
				t.Fatal("测试前提：按后值解码后校验和应匹配")
			}

			rec, err := a.Latest("s", []string{"v1"})
			assertDuplicateNameCorrupt(t, err, info.ID)
			if rec.ID != "" || rec.State.Time != 0 || rec.State.Seed != 0 {
				t.Fatalf("损坏记录不应交付任何部分世界: %+v", rec)
			}

			_, err = a.Record("s", info.ID, []string{"v1"})
			assertDuplicateNameCorrupt(t, err, info.ID)

			// 即使规则版本不在可接受集合内，也先报损坏，不报版本拒绝。
			_, err = a.Latest("s", []string{"other"})
			assertDuplicateNameCorrupt(t, err, info.ID)

			// 读取保持只读：文件字节不变，槽当前记录不切换。
			if got := recordFileBytes(t, a, info.ID); string(got) != string(out) {
				t.Fatal("读取改写了名称重复的记录")
			}
			if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != info.ID {
				t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
			}
		})
	}
}

// TestDuplicateNameChineseKeyUnicodeEscape 使用合法中文名称的存档中，直接
// 写出的中文键与表示同一名称的 Unicode 转义第二次出现时同样判损坏；而
// 合法中文名称、中文键的单次 Unicode 转义写法都照常可读。
func TestDuplicateNameChineseKeyUnicodeEscape(t *testing.T) {
	a, _ := newTestArchive(t)
	w, err := NewWorld(InitialData{
		Seed: 7,
		Rules: Rules{
			Version:     "v1",
			Locations:   []string{"大厅", "院子"},
			Edges:       []Edge{{From: "大厅", To: "院子"}},
			ItemKinds:   []string{"金币"},
			CarryLimits: map[string]int{"英雄": 3},
		},
		Characters: []Character{
			// 字符串值中出现与对象键相同的文字和标点，不算对象名称。
			{ID: "英雄", Location: "大厅", Items: []CharacterItem{{Item: "金币", Count: 1}}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	writeDuplicateNameRecord(t, a, info.ID, duplicatePatch{
		anchor:    `"英雄": 3`,
		key:       "英雄",
		value:     3,
		escapeKey: true, // 英雄 与直接写出的“英雄”同名
	})
	_, err = a.Latest("s", []string{"v1"})
	assertDuplicateNameCorrupt(t, err, info.ID)
	_, err = a.Record("s", info.ID, []string{"v1"})
	assertDuplicateNameCorrupt(t, err, info.ID)
}

// TestSameFieldNameInDistinctObjectsAllowed 同名字段出现在不同数组元素、
// 不同嵌套对象中是正常存档：两个角色物品条目各自有数量字段、两条连通关系
// 各自有起点终点字段，不能误判为重复；仅改变字段排列或缩进但内容未变的
// 记录照常读取，字符串值里的文字和标点不参与判重。
func TestSameFieldNameInDistinctObjectsAllowed(t *testing.T) {
	a, _ := newTestArchive(t)
	w, err := NewWorld(InitialData{
		Seed: 1,
		Rules: Rules{
			Version:   "v1",
			Locations: []string{"hall", "yard"},
			Edges: []Edge{
				{From: "hall", To: "yard"},
				{From: "yard", To: "hall"},
			},
			ItemKinds:   []string{"gold", "key"},
			CarryLimits: map[string]int{"hero": 5, "mage": 5},
		},
		Characters: []Character{
			// ID 与地点字符串值里出现 "Count"/"Time" 等文字和标点，
			// 它们是字符串内容，不是对象名称。
			{ID: "hero: Count, Time!", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
			{ID: "mage", Location: "yard", Items: []CharacterItem{{Item: "key", Count: 2}}},
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

	// 两个物品条目各自的 Count、两条边各自的 From/To 同名但分属不同对象。
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("不同对象中的同名字段不应误判: %v", err)
	}
	if !reflect.DeepEqual(rec.State, want) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec.State, want)
	}

	// 仅改变字段排列（经 map 按字典序重排）与缩进，内容未变、校验和仍匹配。
	path := recordPath(a.dir, info.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	flat, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(flat, &generic); err != nil {
		t.Fatal(err)
	}
	reordered, err := json.MarshalIndent(generic, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, reordered, 0o600); err != nil {
		t.Fatal(err)
	}
	rec2, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("仅改变字段排列与缩进的记录应可读: %v", err)
	}
	if !reflect.DeepEqual(rec2.State, want) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec2.State, want)
	}
}

// TestHistoryRecoverSkipsDuplicateName 带历史索引的槽中，名称重复的记录在
// 浏览时只被略过，其余完好记录仍按保存生效次序列出；恢复读取与恢复预览
// 越过它选择本槽最近一份完好且版本可接受的记录，全部不可用时返回
// ErrUnrecoverable。浏览、读取与预览都不修补、删除或切换任何内容。
func TestHistoryRecoverSkipsDuplicateName(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2(time 2) r1(time 1) r0(time 0)
	writeDuplicateNameRecord(t, a, ids[0], duplicatePatch{
		anchor: `"Time": 2,`,
		key:    "Time",
		value:  -9, // 先写负时间片，原有的 "Time": 2 作为后值保留
	})
	before0 := recordFileBytes(t, a, ids[0])

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(infos) != 2 || infos[0].ID != ids[1] || infos[1].ID != ids[2] {
		t.Fatalf("名称重复记录应被略过，其余按次序列出: %+v", infos)
	}

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

	// 其余记录也名称重复时，恢复与预览返回 ErrUnrecoverable，历史为空列表。
	writeDuplicateNameRecord(t, a, ids[1], duplicatePatch{
		anchor: `"Time": 1,`, key: "Time", value: 0,
	})
	writeDuplicateNameRecord(t, a, ids[2], duplicatePatch{
		anchor: `"Time": 0,`, key: "Time", value: 0,
	})
	if _, err := a.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("全部名称重复应不可恢复，得到 %T: %v", err, err)
	}
	if _, err := a.PreviewRecovery("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("预览同样应不可恢复，得到 %T: %v", err, err)
	}
	if infos, err := a.History("s"); err != nil || len(infos) != 0 {
		t.Fatalf("全部名称重复时历史应为成功的空列表: %+v err=%v", infos, err)
	}

	// 全程只读：受损文件未被修补，槽当前指向不变。
	if got := recordFileBytes(t, a, ids[0]); string(got) != string(before0) {
		t.Fatal("浏览/恢复改写了名称重复的记录")
	}
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != ids[0] {
		t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
	}
}

// TestDuplicateNameLegacyPointerHistory 无历史索引的旧槽保持原有回溯边界：
// 重建沿校验通过的父链进行，名称重复的记录校验不过，重建停在那里，不越过
// 它去确认更老记录的归属。
func TestDuplicateNameLegacyPointerHistory(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0
	writeDuplicateNameRecord(t, a, ids[2], duplicatePatch{
		anchor: `"Time": 1,`, key: "Time", value: 0,
	})
	writeLegacyPointer(t, a, "s", ids[0])

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 最新与次新完好：恢复仍命中最新记录；历史只列到名称重复记录之前。
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("名称重复记录之前的完好记录应可恢复: %v", err)
	}
	if rec.ID != ids[0] {
		t.Fatalf("应恢复最新记录 %s，得到 %s", ids[0], rec.ID)
	}
	infos, err := a2.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	assertHistoryIDs(t, infos, []RecordID{ids[0], ids[1]})

	// 最新与次新也损坏后，链在名称重复记录处断裂，首存记录无法确认归属。
	corruptChecksum(t, dir, ids[0])
	corruptChecksum(t, dir, ids[1])
	if _, err := a2.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("断裂处更老的记录不应进入候选，得到 %T: %v", err, err)
	}
}

// TestSaveSourcesDuplicateNameRejected 把名称重复的记录用作保存来源时一律
// 按损坏拒绝：分支、覆盖、升级、迁移与确认恢复都不产生新记录，不改变槽
// 指向或已有历史，也不把重复内容重新保存成完好记录。
func TestSaveSourcesDuplicateNameRejected(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2 r1 r0
	writeDuplicateNameRecord(t, a, ids[0], duplicatePatch{
		anchor: `"Time": 2,`, key: "Time", value: 0,
	})
	before := recordCount(t, a)

	// 分支：来源名称重复。
	_, err := a.Branch("s", ids[0], "b")
	assertDuplicateNameCorrupt(t, err, ids[0])
	if _, err := a.readLatestLocked("b"); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("不应创建目标槽，得到 %v", err)
	}

	// 覆盖：当前记录名称重复。
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
	assertDuplicateNameCorrupt(t, err, ids[0])

	// 升级：检查与正式升级都拒绝。
	_, err = a.CheckUpgrade("s", []string{"v1"}, v2Rules())
	assertDuplicateNameCorrupt(t, err, ids[0])
	_, err = a.Upgrade("s", []string{"v1"}, v2Rules(), ids[0])
	assertDuplicateNameCorrupt(t, err, ids[0])

	// 迁移：预览与正式迁移都拒绝。
	_, err = a.PreviewMigration("s", []string{"v1"}, v2Rules(), nil, nil)
	assertDuplicateNameCorrupt(t, err, ids[0])
	_, err = a.Migrate("s", []string{"v1"}, v2Rules(), nil, nil, ids[0])
	assertDuplicateNameCorrupt(t, err, ids[0])

	// 确认恢复：来源名称重复。
	_, err = a.ConfirmRecovery("s", ids[0], ids[0], []string{"v1"})
	assertDuplicateNameCorrupt(t, err, ids[0])

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

	// 本槽完好的历史记录仍可正常作为分支来源。
	if _, err := a.Branch("s", ids[1], "ok"); err != nil {
		t.Fatalf("完好来源应可分支: %v", err)
	}
}
