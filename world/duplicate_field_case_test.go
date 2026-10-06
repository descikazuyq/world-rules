package world

import (
	"errors"
	"reflect"
	"testing"
)

// TestCaseVariantDuplicateFixedFieldCorrupt 同一对象内两个名称只要会被
// encoding/json 读取为同一个固定字段（仅大小写或 Unicode 转义写法不同），
// 就算重复：记录、世界状态、规则、道路、角色与物品条目各处都适用。即使两个
// 值相同、交换先后顺序、后一个值让世界合法且按解码结果重算的校验和匹配，
// Latest 与 Record 都必须返回保留记录标识、说明名称重复的 *CorruptError；
// 规则版本不被接受时也先报损坏而不是版本拒绝。
func TestCaseVariantDuplicateFixedFieldCorrupt(t *testing.T) {
	cases := []struct {
		name string
		from string
		to   string
	}{
		// 任务描述的核心情形：先写 "Time": -1，再写 "time": 0/合法值。
		{"时间片大小写后者合法", `"Time": 1,`, `"Time": -1, "time": 1,`},
		// 小写变体在前：交换先后顺序不能让重复通过。
		{"时间片小写变体在前", `"Time": 1,`, `"time": -1, "Time": 1,`},
		// 两个值完全相同也不能通过。
		{"时间片同值大小写", `"Time": 1,`, `"time": 1, "Time": 1,`},
		// 世界状态中的其他固定字段。
		{"种子大小写", `"Seed": 42,`, `"Seed": 7, "seed": 42,`},
		// 规则对象中的固定字段。
		{"规则版本大小写", `"Version": "v1",`, `"Version": "v9", "version": "v1",`},
		// 一个变体用合法 Unicode 转义写出（u0076 还原为小写 v），还原后
		// 是 "version"，与 Version 读取为同一字段；先写转义、再写直接写法。
		{"规则版本Unicode转义", `"Version": "v1",`, `"\u0076ersion": "v9", "Version": "v1",`},
		// 直接写法在前、转义形式在后：交换先后顺序仍算重复。
		{"规则版本Unicode转义在后", `"Version": "v1",`, `"Version": "v9", "\u0076ersion": "v1",`},
		// 时间片的小写变体用转义写出（u0074 还原为小写 t），先写负时间片，
		// 再以转义字段写回合法值，按解码结果重算校验和也不能通过。
		{"时间片Unicode转义小写", `"Time": 1,`, `"Time": -1, "\u0074ime": 1,`},
		// 道路条目中的固定字段（前者引用未知地点、后者写回合法值）。
		{"道路起点大小写", `"From": "hall"`, `"From": "zzz", "from": "hall"`},
		{"道路终点大小写", `"To": "yard"`, `"To": "zzz", "to": "yard"`},
		// 角色对象中的固定字段。
		{"角色标识大小写", `"ID": "hero",`, `"ID": "villain", "id": "hero",`},
		{"角色地点大小写", `"Location": "hall",`, `"Location": "zzz", "location": "hall",`},
		// 物品条目中的固定字段。
		{"物品名称大小写", `"Item": "gold",`, `"Item": "key", "item": "gold",`},
		{"物品数量大小写", `"Count": 1`, `"Count": 9, "count": 1`},
		// 记录信封：字段有 JSON 标签，匹配按标签大小写不敏感进行。
		{"信封格式字段大小写", `"format": 1,`, `"format": 1, "Format": 1,`},
		{"信封首记录标记大小写", `"slotFirst": false,`, `"slotFirst": false, "SlotFirst": false,`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestArchive(t)
			ids := saveN(t, a, "s", 1) // r1(time 1) r0(time 0)
			rewriteRecordText(t, a, ids[0], tc.from, tc.to)
			before := recordFileBytes(t, a, ids[0])

			_, err := a.Latest("s", []string{"v1"})
			assertDuplicateNameCorrupt(t, err, ids[0])

			_, err = a.Record("s", ids[0], []string{"v1"})
			assertDuplicateNameCorrupt(t, err, ids[0])

			// 即使规则版本不被接受，也先报告损坏。
			_, err = a.Latest("s", []string{"other"})
			assertDuplicateNameCorrupt(t, err, ids[0])

			// 只读：文件不被修补，槽指向不变；本槽更早的完好记录仍可读。
			if got := recordFileBytes(t, a, ids[0]); string(got) != string(before) {
				t.Fatal("读取改写了含重复字段的记录")
			}
			if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != ids[0] {
				t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
			}
			if _, err := a.Record("s", ids[1], []string{"v1"}); err != nil {
				t.Fatalf("完好的历史记录应可读: %v", err)
			}
		})
	}
}

// TestCarryLimitKeysCaseSensitive 携带上限解码到 map，键是实际角色标识：
// hero 与 HERO 是两个角色，各自的上限与角色条目都必须保留，不能被合并，
// 也不能因为大小写不同而误报重复字段。
func TestCarryLimitKeysCaseSensitive(t *testing.T) {
	a, _ := newTestArchive(t)
	rules := baseRules()
	rules.CarryLimits = map[string]int{"hero": 5, "HERO": 3}
	w, err := NewWorld(InitialData{
		Seed:  42,
		Rules: rules,
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
			{ID: "HERO", Location: "yard"},
			{ID: "mage", Location: "cave"},
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
		t.Fatalf("hero 与 HERO 是携带上限中两个不同的键，记录应可读: %v", err)
	}
	if !reflect.DeepEqual(rec.State, want) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec.State, want)
	}
	if got := rec.State.Rules.CarryLimits["HERO"]; got != 3 {
		t.Fatalf("HERO 的携带上限应为 3，得到 %d", got)
	}

	// 在携带上限对象中再注入一个仅大小写不同的键 "Hero"：它与 hero、HERO
	// 都不是同一个精确键，解码后三个角色各自有条目，记录仍应完好可读。
	// JSON 按 UTF-8 排序输出，"hero" 是最后一个键，后面没有逗号。
	rewriteRecordText(t, a, info.ID, `"hero": 5`, `"Hero": 4, "hero": 5`)
	rec, err = a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("携带上限中大小写不同的键应视为不同角色: %v", err)
	}
	if rec.State.Rules.CarryLimits["hero"] != 5 ||
		rec.State.Rules.CarryLimits["HERO"] != 3 ||
		rec.State.Rules.CarryLimits["Hero"] != 4 {
		t.Fatalf("三个仅大小写不同的上限键应各自保留: %+v", rec.State.Rules.CarryLimits)
	}
}

// TestSingleCaseVariantFixedFieldReadable 某个固定字段只出现一次时，原本
// 就能被 encoding/json 接受的大小写写法继续可读，无需重写旧记录；读回状态
// 与保存时一致（字段值、角色/地点/物品名称等字符串原样保留）。
func TestSingleCaseVariantFixedFieldReadable(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 1)
	id := ids[0]
	want, err := a.Record("s", id, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}

	// 状态中唯一的时间片字段改成小写写法，解码内容不变、校验和不变。
	rewriteRecordText(t, a, id, `"Time": 1,`, `"time": 1,`)
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("只出现一次的小写时间片字段应继续可读: %v", err)
	}
	if !reflect.DeepEqual(rec.State, want.State) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec.State, want.State)
	}

	// 角色对象中的唯一标识字段改成小写写法同样可读。
	rewriteRecordText(t, a, id, `"ID": "hero",`, `"id": "hero",`)
	rec, err = a.Record("s", id, []string{"v1"})
	if err != nil {
		t.Fatalf("只出现一次的小写字段应继续可读: %v", err)
	}
	if !reflect.DeepEqual(rec.State, want.State) {
		t.Fatalf("读回状态与保存前不一致:\n got %+v\nwant %+v", rec.State, want.State)
	}
}

// TestCaseVariantDuplicateBrowseAndRecover 含大小写变体重复字段的最新记录
// 在历史浏览中被略过；恢复读取与恢复预览越过它，选择本槽最近一份完好且
// 版本可接受的记录，全程不修补文件、不切换槽指向。
func TestCaseVariantDuplicateBrowseAndRecover(t *testing.T) {
	a, _ := newTestArchive(t)
	ids := saveN(t, a, "s", 2) // r2(time 2) r1(time 1) r0(time 0)
	rewriteRecordText(t, a, ids[0], `"Time": 2,`, `"Time": -1, "time": 2,`)
	before := recordFileBytes(t, a, ids[0])

	infos, err := a.History("s")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(infos) != 2 || infos[0].ID != ids[1] || infos[1].ID != ids[2] {
		t.Fatalf("含大小写重复字段的记录应被略过，其余按次序列出: %+v", infos)
	}

	rec, err := a.RecoverLatest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("应恢复最近一份完好记录: %v", err)
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

	// 其余记录也带上大小写变体重复字段后，恢复与预览返回不可恢复错误。
	rewriteRecordText(t, a, ids[1], `"Time": 1,`, `"Time": -1, "time": 1,`)
	rewriteRecordText(t, a, ids[2], `"Time": 0,`, `"Time": -1, "time": 0,`)
	if _, err := a.RecoverLatest("s", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("全部损坏时应不可恢复，得到 %T: %v", err, err)
	}
	if infos, err := a.History("s"); err != nil || len(infos) != 0 {
		t.Fatalf("全部损坏时历史应为成功的空列表: %+v err=%v", infos, err)
	}

	if got := recordFileBytes(t, a, ids[0]); string(got) != string(before) {
		t.Fatal("浏览/恢复改写了含重复字段的记录")
	}
	if p, err := a.readSlotPointerLocked("s"); err != nil || p.Latest != ids[0] {
		t.Fatalf("槽指向不应改变: %+v err=%v", p, err)
	}
}
