package world

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// rewriteRecordDuplicate 对记录文件做一次原始文本替换（在同一对象内注入
// 同名字段），并按替换后解码（同名字段后者覆盖前者）的内容重算校验和写回，
// 模拟“同一对象内出现重复名称、解码后的世界合法且校验和匹配”的损坏记录。
func rewriteRecordDuplicate(t *testing.T, a *Archive, id RecordID, from, to string) {
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
	// encoding/json 对同名字段静默采用后一个值，解析仍然成功；按解码结果
	// 重算校验和，使重复名称在校验和下被掩盖。
	var cenv envelope
	if err := json.Unmarshal([]byte(corrupted), &cenv); err != nil {
		t.Fatalf("测试前提：注入重复名称后的记录应仍可被 encoding/json 解析: %v", err)
	}
	corrupted = strings.Replace(corrupted, oldSum, computeChecksum(&cenv), 1)
	if err := os.WriteFile(path, []byte(corrupted), 0o600); err != nil {
		t.Fatal(err)
	}
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
	if !strings.Contains(ce.Error(), "重复") {
		t.Fatalf("错误应说明名称重复: %v", ce)
	}
	if errors.As(err, new(*VersionRejectedError)) {
		t.Fatalf("名称重复不应报成版本拒绝: %v", err)
	}
}

// TestLatestAndRecordDuplicateNameCorrupt 同一 JSON 对象内出现同名字段时，
// 无论两个值是否相同、采用后一个值后的世界是否合法、校验和是否匹配，Latest
// 与 Record 都必须把整条记录视为损坏，返回保留记录标识并说明名称重复的
// *CorruptError；即使规则版本不在可接受集合内也先报损坏。读取保持只读，
// 不修补这份记录，也不切换槽当前记录。
func TestLatestAndRecordDuplicateNameCorrupt(t *testing.T) {
	cases := []struct {
		name string
		from string
		to   string
	}{
		// 记录顶层字段，两个值相同也算重复。
		{"顶层同值", `"format": 1,`, `"format": 1, "format": 1,`},
		// 记录顶层字段，前者是矛盾值、后者写回合法值。
		{"顶层异值", `"slotFirst": false,`, `"slotFirst": true, "slotFirst": false,`},
		// 先写负时间片，再用同名字段写回合法时间片。
		{"负时间片写回", `"Time": 1,`, `"Time": -5, "Time": 1,`},
		// 世界规则中的字段。
		{"规则版本", `"Version": "v1",`, `"Version": "v9", "Version": "v1",`},
		// 角色对象中的字段。
		{"角色标识", `"ID": "hero",`, `"ID": "villain", "ID": "hero",`},
		// 物品条目中的字段。
		{"物品名称", `"Item": "gold",`, `"Item": "key", "Item": "gold",`},
		{"物品数量", `"Count": 1`, `"Count": -9, "Count": 1`},
		// 名称到数值的携带上限对象。
		{"携带上限", `"hero": 5,`, `"hero": 99, "hero": 5,`},
		// 直接写出的名称与表示同一名称的 Unicode 转义（\u0049D）同为 "ID"。
		{"Unicode转义同名", `"ID": "hero",`, `"\u0049D": "hero", "ID": "hero",`},
		// 转义写在前：先以转义形式给出同一名称，再直接写出。
		{"Unicode转义同名在前", `"ID": "hero",`, `"ID": "hero", "\u0049D": "hero",`},
		// 仅大小写不同的两个名称也会写入同一个固定字段，后一个值不能遮住
		// 前一个值——本次修复的核心情形。
		{"时间片大小写", `"Time": 1,`, `"Time": -5, "time": 1,`},
		// 小写写在前、大写写在后；两个值完全相同也算重复。
		{"时间片小写在前同值", `"Time": 1,`, `"time": 1, "Time": 1,`},
		// 信封字段（JSON 标签为小写）换成大写仍是同一字段。
		{"信封格式大小写", `"format": 1,`, `"format": 1, "FORMAT": 1,`},
		{"信封格式大写在前", `"format": 1,`, `"FORMAT": 1, "format": 1,`},
		// 规则、道路、角色、物品条目里的字段同样按忽略大小写归并。
		{"规则版本大小写", `"Version": "v1",`, `"VERSION": "v9", "Version": "v1",`},
		{"道路端点大小写", `"From": "hall",`, `"from": "void", "From": "hall",`},
		{"角色标识大小写", `"ID": "hero",`, `"id": "ghost", "ID": "hero",`},
		{"物品数量大小写", `"Count": 1`, `"count": -9, "Count": 1`},
		// 直接写法与 Unicode 转义写法在大小写维度上仍然相撞。
		{"大小写混合转义", `"Time": 1,`, `"Time": -5, "time": 1,`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			ids := saveN(t, a, "s", 1) // r1(time 1) r0(time 0)
			rewriteRecordDuplicate(t, a, ids[0], tc.from, tc.to)
			before := recordFileBytes(t, a, ids[0])

			_, err := a.Latest("s", []string{"v1"})
			assertDuplicateNameCorrupt(t, err, ids[0])

			_, err = a.Record("s", ids[0], []string{"v1"})
			assertDuplicateNameCorrupt(t, err, ids[0])

			// 版本不被接受时同样报损坏，不报版本拒绝。
			_, err = a.Latest("s", []string{"other"})
			assertDuplicateNameCorrupt(t, err, ids[0])

			// 记录没有被悄悄修补或删除，槽指向不变。
			if got := recordFileBytes(t, a, ids[0]); string(got) != string(before) {
				t.Fatal("读取改写了名称重复的记录")
			}
			if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != ids[0] {
				t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
			}

			// 本槽完好的历史记录不受影响，仍可正常读取。
			if _, err := a.Record("s", ids[1], []string{"v1"}); err != nil {
				t.Fatalf("完好的历史记录应可读: %v", err)
			}
		})
	}
}

// TestHistorySkipsDuplicateNameRecords 带历史索引的槽中，含重复名称的记录
// 在浏览时只被略过，其余完好记录仍按保存生效次序列出；恢复读取与恢复预览
// 跳过它，选择本槽最近一份完好且版本可接受的记录，全部不可用时返回
// ErrUnrecoverable。浏览、读取与预览都不修补、删除或切换任何内容。
func TestHistorySkipsDuplicateNameRecords(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2(time 2) r1(time 1) r0(time 0)
	rewriteRecordDuplicate(t, a, ids[0], `"Time": 2,`, `"Time": -1, "Time": 2,`)
	before := recordFileBytes(t, a, ids[0])

	// 浏览略过名称重复的最新记录，其余按保存生效次序列出。
	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(infos) != 2 || infos[0].ID != ids[1] || infos[1].ID != ids[2] {
		t.Fatalf("名称重复的记录应被略过，其余按次序列出: %+v", infos)
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

	// 全部记录都含重复名称时，恢复与预览返回 ErrUnrecoverable。
	rewriteRecordDuplicate(t, a, ids[1], `"Time": 1,`, `"Time": -1, "Time": 1,`)
	rewriteRecordDuplicate(t, a, ids[2], `"Time": 0,`, `"Time": -1, "Time": 0,`)
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
		t.Fatal("浏览/恢复改写了名称重复的记录")
	}
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != ids[0] {
		t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
	}
}

// TestDuplicateNameLegacyPointerHistory 旧格式裸指针沿父链重建历史时，含
// 重复名称的记录同样校验不过：重建停在那里，无法确认归属的更老记录不进入
// 恢复候选。
func TestDuplicateNameLegacyPointerHistory(t *testing.T) {
	a, dir := newTestArchive(t)
	ids := saveN(t, a, "s", 3) // r3 r2 r1 r0
	rewriteRecordDuplicate(t, a, ids[2], `"Time": 1,`, `"Time": -1, "Time": 1,`)
	writeLegacyPointer(t, a, "s", ids[0])

	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 最新与次新完好：恢复仍命中最新记录。
	rec, err := a2.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("名称重复记录之前的完好记录应可恢复: %v", err)
	}
	if rec.ID != ids[0] {
		t.Fatalf("应恢复最新记录 %s，得到 %s", ids[0], rec.ID)
	}

	// 最新与次新也损坏后，链在名称重复的记录处断裂，首存记录无法确认归属。
	corruptChecksum(t, dir, ids[0])
	corruptChecksum(t, dir, ids[1])
	if _, err := a2.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("断裂处更老的记录不应进入候选，得到 %T: %v", err, err)
	}
}

// TestDistinctObjectsSameNameStillReadable 不同对象各自使用相同名称是正常
// 内容：两个角色各自有标识与物品、两个物品条目各自有名称与数量字段，不能
// 被误判为重复。字符串值中的文字和标点（包括看起来像对象内容的名称）不算
// 对象名称。合法中文名称及其 Unicode 转义写法、只改变缩进或字段排列但内容
// 未变的记录都照常可读。
func TestDistinctObjectsSameNameStillReadable(t *testing.T) {
	a, _ := newTestArchive(t)
	w, err := NewWorld(InitialData{
		Seed: 7,
		Rules: Rules{
			Version:     "v1",
			Locations:   []string{"大厅", "院子"},
			Edges:       []Edge{{From: "大厅", To: "院子"}},
			ItemKinds:   []string{"金币", "钥匙"},
			CarryLimits: map[string]int{"英雄": 5},
		},
		Characters: []Character{
			{ID: "英雄", Location: "大厅", Items: []CharacterItem{
				{Item: "金币", Count: 1},
				{Item: "钥匙", Count: 2},
			}},
			// 字符串值中的标点与文字不算对象名称。
			{ID: `{"ID": 1, "Count": 2}`, Location: "院子"},
			{ID: "Count", Location: "院子"},
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

	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("不同对象各自使用相同名称的记录应可读: %v", err)
	}
	if !reflect.DeepEqual(rec.State, want) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec.State, want)
	}

	// 把文件中的中文名称改写为 Unicode 转义写法：解析内容不变，也不与
	// 直接写出的同名字符串值构成重复，记录应照常可读。
	path := recordPath(a.dir, info.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(raw), "大厅", `\u5927\u5385`)
	s = strings.ReplaceAll(s, "英雄", `\u82f1\u96c4`)
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, err = a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("合法转义写法的记录应可读: %v", err)
	}
	if !reflect.DeepEqual(rec.State, want) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec.State, want)
	}

	// 只改变缩进与字段排列（经 map 重新序列化）但内容未变的记录仍可读。
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	flat, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, flat, 0o600); err != nil {
		t.Fatal(err)
	}
	rec, err = a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("仅改变格式的记录应可读: %v", err)
	}
	if !reflect.DeepEqual(rec.State, want) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec.State, want)
	}
}
