package world

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestZeroTimeCaseDuplicateRejected 任务描述中的原始情形：零时间片状态里
// 先写大写 "Time": -1 再写小写 "time": 0，解码结果是合法的零时间片状态
// 且按该状态重算的校验和匹配，但两个键会被读取为同一个固定字段，必须按
// 损坏拒绝；交换先后顺序同样拒绝。
func TestZeroTimeCaseDuplicateRejected(t *testing.T) {
	// 大写在前、小写在后：最终解码时间片为 0。
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 0) // 只有一份时间片为 0 的槽首记录
	rewriteRecordDuplicate(t, a, ids[0], `"Time": 0,`, `"Time": -1, "time": 0,`)
	_, err := a.Latest("s", []string{"v1"})
	assertDuplicateNameCorrupt(t, err, ids[0])
	_, err = a.Record("s", ids[0], []string{"v1"})
	assertDuplicateNameCorrupt(t, err, ids[0])
	// 版本不被接受也先报损坏。
	_, err = a.Latest("s", []string{"other"})
	assertDuplicateNameCorrupt(t, err, ids[0])

	// 小写在前、大写在后：交换顺序不改变判定。
	a2, _ := newTestArchive(t)
	ids2 := saveN(t, a2, "s", 0)
	rewriteRecordDuplicate(t, a2, ids2[0], `"Time": 0,`, `"time": -1, "Time": 0,`)
	_, err = a2.Latest("s", []string{"v1"})
	assertDuplicateNameCorrupt(t, err, ids2[0])
}

// TestCaseEscapeVariantsCollide 直接写法与 Unicode 转义写法在大小写维度
// 上也会相撞：T 是大写 T、t 是小写 t，转义写法与直接写法只要落入同一
// 个时间片字段就算重复。
func TestCaseEscapeVariantsCollide(t *testing.T) {
	cases := []struct {
		name string
		from string
		to   string
	}{
		// 转义的大写 T（\u0054 解码为 T）在前，直接的小写 time 在后。
		{"转义大写在前", `"Time": 1,`, `"\u0054ime": -1, "time": 1,`},
		// 直接的大写 Time 在前，转义的小写 t（\u0074 解码为 t）在后。
		{"转义小写在后", `"Time": 1,`, `"Time": -1, "\u0074ime": 1,`},
		// 两个键都用 Unicode 转义，仅大小写不同。
		{"两者皆转义", `"Time": 1,`, `"\u0054ime": -1, "\u0074ime": 1,`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			ids := saveN(t, a, "s", 1)
			rewriteRecordDuplicate(t, a, ids[0], tc.from, tc.to)
			_, err := a.Latest("s", []string{"v1"})
			assertDuplicateNameCorrupt(t, err, ids[0])
			_, err = a.Record("s", ids[0], []string{"v1"})
			assertDuplicateNameCorrupt(t, err, ids[0])
		})
	}
}

// TestAlternateCaseSingleFieldReadable 某个固定字段只出现一次时，换一种
// 大小写（与 JSON 标签/Go 名称不同的写法）仍是合法记录，无需重写旧记录。
// 这里把记录中各层级固定字段键整体改成另一种大小写（每个字段在每个对象中
// 仍只出现一次），解码内容不变；校验和只取决于解码后的内容，因此原校验和
// 仍匹配，必须照常读出完全相同的状态。
func TestAlternateCaseSingleFieldReadable(t *testing.T) {
	a, _ := newTestArchive(t)
	w := baseWorld(t)
	want := w.Snapshot()
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := recordPath(a.dir, info.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	// 每个固定字段在每个对象中只出现一次；整体换成另一种大小写写法，
	// 解码目标不变，校验和也不变。
	for _, key := range []string{
		`"State"`, `"Seed"`, `"Rules"`, `"Time"`, `"Characters"`,
		`"Version"`, `"Locations"`, `"Edges"`, `"ItemKinds"`, `"CarryLimits"`,
		`"From"`, `"To"`, `"ID"`, `"Location"`, `"Items"`,
		`"Item"`, `"Count"`,
	} {
		s = strings.ReplaceAll(s, key+":", strings.ToLower(key)+":")
	}
	var cenv envelope
	if err := json.Unmarshal([]byte(s), &cenv); err != nil {
		t.Fatalf("测试前提：换大小写键后的记录应可解析: %v", err)
	}
	// 只改键的大小写不改变解码内容，记录原有校验和必须仍然匹配——
	// 这正是旧记录无需重写即可继续读取的兼容性依据。
	if got := computeChecksum(&cenv); got != cenv.Checksum {
		t.Fatalf("只改键的大小写不应改变校验和: got %s want %s", got, cenv.Checksum)
	}
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}

	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("固定字段只出现一次时，大小写变体应当可读: %v", err)
	}
	if !reflect.DeepEqual(rec.State, want) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec.State, want)
	}
}

// TestCarryLimitsKeysCaseSensitive 携带上限的键是实际角色标识：hero 与
// HERO 分别表示两个角色，各自的上限都要保留，不能按大小写合并或误报重复；
// 角色标识、地点与物品名称等字符串值也原样区分大小写。
func TestCarryLimitsKeysCaseSensitive(t *testing.T) {
	a, _ := newTestArchive(t)
	w, err := NewWorld(InitialData{
		Seed: 9,
		Rules: Rules{
			Version:   "v1",
			Locations: []string{"Hall", "hall"},
			Edges:     []Edge{{From: "Hall", To: "hall"}},
			ItemKinds: []string{"Gold", "gold"},
			CarryLimits: map[string]int{
				"hero": 3,
				"HERO": 7,
			},
		},
		Characters: []Character{
			{ID: "hero", Location: "Hall", Items: []CharacterItem{{Item: "Gold", Count: 2}}},
			{ID: "HERO", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 5}}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	want := w.Snapshot()
	if _, err := a.Save("s", w); err != nil {
		t.Fatalf("Save: %v", err)
	}
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("hero 与 HERO 的携带上限键必须各自保留: %v", err)
	}
	if got := rec.State.Rules.CarryLimits["hero"]; got != 3 {
		t.Fatalf("hero 上限应为 3，得到 %d", got)
	}
	if got := rec.State.Rules.CarryLimits["HERO"]; got != 7 {
		t.Fatalf("HERO 上限应为 7，得到 %d", got)
	}
	if len(rec.State.Rules.CarryLimits) != 2 {
		t.Fatalf("两个仅大小写不同的角色键不能被合并: %+v", rec.State.Rules.CarryLimits)
	}
	if !reflect.DeepEqual(rec.State, want) {
		t.Fatalf("字符串值（角色/地点/物品名称）应原样保留:\n got %+v\nwant %+v", rec.State, want)
	}
}

// TestUnknownObjectKeysCaseVariants 不映射到固定字段的多余键读取时被
// 忽略：大小写不同的多余键不会误报重复；完全同名的多余键仍按原规则拒绝。
func TestUnknownObjectKeysCaseVariants(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 0)

	// 两个大小写不同的未知键都被忽略，记录仍可读。
	rewriteRecordDuplicate(t, a, ids[0], `"format": 1,`, `"format": 1, "extra": 1, "EXTRA": 2,`)
	if _, err := a.Latest("s", []string{"v1"}); err != nil {
		t.Fatalf("大小写不同的未知键不应误报重复: %v", err)
	}

	// 完全同名的未知键仍属于同一对象内重复，继续拒绝。
	rewriteRecordDuplicate(t, a, ids[0], `"extra": 1, "EXTRA": 2,`, `"extra": 1, "extra": 2,`)
	_, err := a.Latest("s", []string{"v1"})
	assertDuplicateNameCorrupt(t, err, ids[0])
}

// TestHistoryRecoverySkipsCaseDuplicate 含大小写变体重复字段的记录与其他
// 损坏记录同等待遇：浏览略过；恢复读取与恢复预览选择本槽最近一份完好且
// 版本可接受的记录；全部不可用时返回 ErrUnrecoverable；全程只读，不修补
// 文件也不切换槽当前指向。
func TestHistoryRecoverySkipsCaseDuplicate(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // time 2, 1, 0
	rewriteRecordDuplicate(t, a, ids[0], `"Time": 2,`, `"Time": -1, "time": 2,`)
	before := recordFileBytes(t, a, ids[0])

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(infos) != 2 || infos[0].ID != ids[1] || infos[1].ID != ids[2] {
		t.Fatalf("大小写重复字段的记录应被略过: %+v", infos)
	}

	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("RecoverLatest: %v", err)
	}
	if rec.ID != ids[1] || rec.State.Time != 1 {
		t.Fatalf("应恢复到 %s（时间片 1），得到 %s（时间片 %d）", ids[1], rec.ID, rec.State.Time)
	}
	pv, err := a.PreviewRecovery("s", []string{"v1"})
	if err != nil {
		t.Fatalf("PreviewRecovery: %v", err)
	}
	if pv.Current != ids[0] || pv.Source != ids[1] {
		t.Fatalf("预览应为 current=%s source=%s，得到 current=%s source=%s",
			ids[0], ids[1], pv.Current, pv.Source)
	}

	// 即使不接受记录的规则版本，损坏仍先于版本判断报 CorruptError。
	_, err = a.Latest("s", []string{"other"})
	assertDuplicateNameCorrupt(t, err, ids[0])

	// 全部记录都含大小写变体重复字段：不可恢复，浏览得到成功的空列表。
	rewriteRecordDuplicate(t, a, ids[1], `"Time": 1,`, `"Time": -1, "time": 1,`)
	rewriteRecordDuplicate(t, a, ids[2], `"Time": 0,`, `"Time": -1, "time": 0,`)
	if _, err := a.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("全部损坏应不可恢复，得到 %T: %v", err, err)
	}
	if _, err := a.PreviewRecovery("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("预览同样应不可恢复，得到 %T: %v", err, err)
	}
	if infos, err := a.History("s"); err != nil || len(infos) != 0 {
		t.Fatalf("全部损坏时历史应为成功的空列表: %+v err=%v", infos, err)
	}

	// 全程只读：文件未被修补，槽当前指向不变。
	if got := recordFileBytes(t, a, ids[0]); string(got) != string(before) {
		t.Fatal("浏览/恢复改写了含重复字段的记录")
	}
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != ids[0] {
		t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
	}
}

// TestDistinctObjectsSameCaseFieldStillReadable 同一字段名（含不同大小写
// 写法）分别出现在两个不同对象里是正常内容，不能跨对象误报：第二个角色
// 条目的 id、第二个物品条目的 count 换了大小写，但与第一个对象中的同名
// 字段分属不同对象。仅改键的大小写不改变解码内容，原校验和仍匹配。
func TestDistinctObjectsSameCaseFieldStillReadable(t *testing.T) {
	a, _ := newTestArchive(t)
	w, err := NewWorld(InitialData{
		Seed: 3,
		Rules: Rules{
			Version:     "v1",
			Locations:   []string{"a", "b"},
			Edges:       []Edge{{From: "a", To: "b"}},
			ItemKinds:   []string{"x", "y"},
			CarryLimits: map[string]int{"p": 9, "q": 9},
		},
		Characters: []Character{
			{ID: "p", Location: "a", Items: []CharacterItem{{Item: "x", Count: 1}}},
			{ID: "q", Location: "b", Items: []CharacterItem{{Item: "y", Count: 2}}},
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
	path := recordPath(a.dir, info.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	// 只改第二个角色对象的 ID 键、第二个物品对象的 Count 键的大小写：
	// 与第一个对象中的同名字段分属不同对象，不构成重复。
	s = strings.Replace(s, `"ID": "q"`, `"id": "q"`, 1)
	s = strings.Replace(s, `"Count": 2`, `"count": 2`, 1)
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}

	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("不同对象各自的同名字段不应误报: %v", err)
	}
	if !reflect.DeepEqual(rec.State, want) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec.State, want)
	}
}
