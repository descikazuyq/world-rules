package world

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// 本文件锁定“重复名称检查所认识的字段随真实记录结构自动派生”这一性质：
// 测试自己（独立于生产代码的建表逻辑）用 reflect 遍历 envelope 可达的
// 每个结构体与每个可导出 JSON 字段，逐字段在其真实嵌套路径上注入一个仅
// 大小写折叠的重复键，检查必须判损坏；不注入重复键的同一文档必须完好。
// 一旦真实结构新增/改名字段或调整嵌套，遍历会自动覆盖新字段，无需再手工
// 维护第二份字段清单，也不会出现“结构加了字段、检查却不认识”的漏判。

// jsonNameOf 与生产代码的 jsonFieldName 同样取字段的 JSON 名称，但这是
// 测试侧独立实现，用来交叉核对派生结果。
func jsonNameOf(sf reflect.StructField) string {
	tag := sf.Tag.Get("json")
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}
	if tag == "" || tag == "-" {
		return sf.Name
	}
	return tag
}

// rawValueFor 为任意 Go 类型生成一个括号配平的 JSON 占位值；数值与字符串
// 的具体内容不影响重复名称检查，只有容器边界影响解析栈。
func rawValueFor(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Struct:
		return buildRawObject(t, "")
	case reflect.Ptr:
		return rawValueFor(t.Elem())
	case reflect.Map:
		return "{}"
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Struct {
			return "[" + buildRawObject(t.Elem(), "") + "]"
		}
		if t.Elem().Kind() == reflect.Ptr && t.Elem().Elem().Kind() == reflect.Struct {
			return "[" + buildRawObject(t.Elem().Elem(), "") + "]"
		}
		return "[]"
	case reflect.String:
		return `""`
	case reflect.Bool:
		return "false"
	default:
		// 各类整数、浮点等：检查用 UseNumber 读入，不解释数值。
		return "0"
	}
}

// buildRawObject 构造结构体类型 t 的完整原始 JSON 对象：每个可导出 JSON
// 字段都按声明顺序出现一次。dupField 非空时，再为该字段追加一个仅大小写
// 折叠的同名键（值使用相同占位值，保证容器括号配平），折叠键写在原键
// 之后。
func buildRawObject(t reflect.Type, dupField string) string {
	var parts []string
	emit := func(name, val string) { parts = append(parts, "\""+name+"\":"+val) }
	for fi, n := 0, t.NumField(); fi < n; fi++ {
		sf := t.Field(fi)
		if sf.PkgPath != "" || sf.Tag.Get("json") == "-" {
			continue
		}
		name := jsonNameOf(sf)
		val := rawValueFor(sf.Type)
		emit(name, val)
		if name == dupField {
			emit(foldCaseVariant(name), val)
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// foldCaseVariant 翻转名称首字符的大小写，得到一个与原名会被
// encoding/json 读取为同一字段、但字面不同的键。记录结构中的全部 JSON
// 字段名都以 ASCII 字母开头。
func foldCaseVariant(name string) string {
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z':
			return name[:i] + strings.ToUpper(name[i:i+1]) + name[i+1:]
		case c >= 'A' && c <= 'Z':
			return name[:i] + strings.ToLower(name[i:i+1]) + name[i+1:]
		}
	}
	return name
}

// pathSeg 描述从记录根信封到某一对象类型的嵌套路径上的一跳。
type pathSeg struct {
	parent reflect.Type // 该跳所在的父结构体类型
	key    string       // 父结构体中的 JSON 字段名
	array  bool         // 该字段是数组，目标对象是其元素
}

// collectStructPaths 从根结构体出发，独立遍历真实结构，得到每个可达结构
// 体类型的一条嵌套路径（记录结构中每个结构体只有一条可达路径）。
func collectStructPaths(root reflect.Type) map[reflect.Type][]pathSeg {
	paths := map[reflect.Type][]pathSeg{root: nil}
	queue := []reflect.Type{root}
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		for fi, n := 0, t.NumField(); fi < n; fi++ {
			sf := t.Field(fi)
			if sf.PkgPath != "" || sf.Tag.Get("json") == "-" {
				continue
			}
			ft := sf.Type
			register := func(child reflect.Type, viaArray bool) {
				if _, seen := paths[child]; seen {
					return
				}
				p := append(append([]pathSeg(nil), paths[t]...), pathSeg{
					parent: t, key: jsonNameOf(sf), array: viaArray,
				})
				paths[child] = p
				queue = append(queue, child)
			}
			switch ft.Kind() {
			case reflect.Struct:
				register(ft, false)
			case reflect.Ptr:
				if ft.Elem().Kind() == reflect.Struct {
					register(ft.Elem(), false)
				}
			case reflect.Slice, reflect.Array:
				if et := ft.Elem(); et.Kind() == reflect.Struct {
					register(et, true)
				} else if et.Kind() == reflect.Ptr && et.Elem().Kind() == reflect.Struct {
					register(et.Elem(), true)
				}
			}
		}
	}
	return paths
}

// buildRawObjectWith 与 buildRawObject 相同，但用 overrides 替换指定
// JSON 字段的值（用于在嵌套路径上放置目标对象）。
func buildRawObjectWith(t reflect.Type, overrides map[string]string) string {
	var parts []string
	for fi, n := 0, t.NumField(); fi < n; fi++ {
		sf := t.Field(fi)
		if sf.PkgPath != "" || sf.Tag.Get("json") == "-" {
			continue
		}
		name := jsonNameOf(sf)
		val, ok := overrides[name]
		if !ok {
			val = rawValueFor(sf.Type)
		}
		parts = append(parts, "\""+name+"\":"+val)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// wrapAtPath 把目标对象的原始 JSON 按路径逐层包进父结构体的完整对象，
// 最终得到以记录信封为根的完整文档。
func wrapAtPath(target reflect.Type, targetRaw string, segs []pathSeg) string {
	raw := targetRaw
	for i := len(segs) - 1; i >= 0; i-- {
		seg := segs[i]
		val := raw
		if seg.array {
			val = "[" + val + "]"
		}
		raw = buildRawObjectWith(seg.parent, map[string]string{seg.key: val})
	}
	return raw
}

// TestDerivedSchemaCoversEveryStructField 对记录结构可达的每个结构体中的
// 每个固定字段：在它真实所属对象内追加一个仅大小写折叠的重复键（折叠键
// 在前、在后两种次序都测），checkDuplicateNames 必须判损坏；不追加重复
// 键时同一份文档必须完好。若嵌套关系派生错误，目标对象会被当成多余对象，
// 折叠键就不会被判重复——基线与重复两侧会同时暴露问题。
func TestDerivedSchemaCoversEveryStructField(t *testing.T) {
	paths := collectStructPaths(recordRoot)
	wantTypes := map[reflect.Type]bool{
		reflect.TypeOf(envelope{}):      true,
		reflect.TypeOf(State{}):         true,
		reflect.TypeOf(Rules{}):         true,
		reflect.TypeOf(Edge{}):          true,
		reflect.TypeOf(Character{}):     true,
		reflect.TypeOf(CharacterItem{}): true,
	}
	for wt := range wantTypes {
		if _, ok := paths[wt]; !ok {
			t.Fatalf("记录结构应可达类型 %v，但派生路径中缺失", wt)
		}
	}

	for target, segs := range paths {
		for fi, n := 0, target.NumField(); fi < n; fi++ {
			sf := target.Field(fi)
			if sf.PkgPath != "" || sf.Tag.Get("json") == "-" {
				continue
			}
			field := jsonNameOf(sf)
			t.Run(target.Name()+"/"+field, func(t *testing.T) {
				// 基线：全部字段只出现一次，文档完好且是合法 JSON。
				clean := wrapAtPath(target, buildRawObject(target, ""), segs)
				if err := checkDuplicateNames([]byte(clean)); err != nil {
					t.Fatalf("无重复键的基线应完好: %v\n%s", err, clean)
				}
				var validJSON any
				if err := json.Unmarshal([]byte(clean), &validJSON); err != nil {
					t.Fatalf("测试构造的基线必须是合法 JSON: %v\n%s", err, clean)
				}

				// 折叠键在后：追加式构造。
				dupAfter := wrapAtPath(target, buildRawObject(target, field), segs)
				if err := checkDuplicateNames([]byte(dupAfter)); err == nil ||
					!strings.Contains(err.Error(), "重复") {
					t.Fatalf("固定字段 %q 的大小写折叠重复键（在后）应判损坏，得到 %v:\n%s",
						field, err, dupAfter)
				}

				// 折叠键在前：与原键交换先后次序，仍应损坏。
				folded := foldCaseVariant(field)
				val := rawValueFor(sf.Type)
				dupBefore := strings.Replace(dupAfter,
					"\""+field+"\":"+val+",\""+folded+"\":",
					"\""+folded+"\":"+val+",\""+field+"\":", 1)
				if err := checkDuplicateNames([]byte(dupBefore)); err == nil ||
					!strings.Contains(err.Error(), "重复") {
					t.Fatalf("固定字段 %q 的大小写折叠重复键（在前）应判损坏，得到 %v:\n%s",
						field, err, dupBefore)
				}
			})
		}
	}
}

// TestDerivedSchemaShape 派生出的对象表形状应与真实结构一致：根是信封
// 结构体且字段名取自 json 标签；6 个结构体对象加 1 个携带上限 map 对象。
func TestDerivedSchemaShape(t *testing.T) {
	root := recordObjects[0]
	if !root.fixed {
		t.Fatal("根对象应解码到 envelope 结构体")
	}
	gotNames := map[string]bool{}
	for _, f := range root.fields {
		gotNames[f.name] = true
	}
	for _, want := range []string{"format", "id", "parent", "slotFirst", "checksum", "state"} {
		if !gotNames[want] {
			t.Fatalf("根信封应认识标签字段 %q，实际 %v", want, gotNames)
		}
	}

	structs, maps := 0, 0
	for i, obj := range recordObjects {
		switch obj.fixed {
		case true:
			structs++
		case false:
			maps++
			if len(obj.fields) != 0 {
				t.Fatalf("map 对象 %d 不应登记固定字段", i)
			}
		}
	}
	if structs != 6 || maps != 1 {
		t.Fatalf("应有 6 个结构体对象与 1 个携带上限 map 对象，实际 struct=%d map=%d",
			structs, maps)
	}
}

// wrapCarryLimits 把携带上限对象的原始 JSON 放进 state.rules.carryLimits
// 的真实嵌套位置，得到完整记录文档。
func wrapCarryLimits(t *testing.T, objRaw string) []byte {
	t.Helper()
	rules := reflect.TypeOf(Rules{})
	rulesRaw := buildRawObjectWith(rules, map[string]string{"CarryLimits": objRaw})
	return []byte(wrapAtPath(rules, rulesRaw, collectStructPaths(recordRoot)[rules]))
}

// TestDerivedMapKeysAreBusinessIDs 携带上限派生为 map 对象：键是真实角色
// 标识，hero 与 HERO 分别保留、互不冲突；同一精确键直接写两次、或一次用
// Unicode 转义写出同一名称，都判损坏。
func TestDerivedMapKeysAreBusinessIDs(t *testing.T) {
	ok := []string{
		`{"hero":1,"HERO":2}`,
		`{"hero":1,"HERO":2,"Hero":3}`,
		`{}`,
	}
	for _, obj := range ok {
		doc := wrapCarryLimits(t, obj)
		if err := checkDuplicateNames(doc); err != nil {
			t.Fatalf("携带上限中精确名称不同的键应各自保留: %v\n%s", err, doc)
		}
	}

	bad := []string{
		`{"hero":1,"hero":2}`,
		// 直接写法与 \u0068ero（还原后是同一精确键 hero）。
		"{\"hero\":1,\"\\u0068ero\":2}",
	}
	for _, obj := range bad {
		doc := wrapCarryLimits(t, obj)
		err := checkDuplicateNames(doc)
		if err == nil || !strings.Contains(err.Error(), "重复") {
			t.Fatalf("携带上限同一精确键重复应判损坏，得到 %v\n%s", err, doc)
		}
	}
}

// TestDerivedUnknownObjectExactKeys 被结构体忽略的多余字段值属于多余对象：
// 其中没有固定字段，精确同名重复拒绝，仅大小写不同的未知键允许；信封根
// 上的多余键同理，绝不与真正的固定字段折叠规则混淆。
func TestDerivedUnknownObjectExactKeys(t *testing.T) {
	ok := []string{
		// 根信封上的多余键：仅大小写不同不折叠。
		`{"format":1,"id":"x","state":{},"extra":1,"Extra":2}`,
		// 多余字段值是对象：其中 a/A 是两个精确键。
		`{"format":1,"id":"x","state":{},"extra":{"a":1,"A":2}}`,
	}
	for _, doc := range ok {
		if err := checkDuplicateNames([]byte(doc)); err != nil {
			t.Fatalf("多余对象中精确名称不同的键应允许: %v\n%s", err, doc)
		}
	}

	bad := []string{
		// 根信封上的多余键精确重复仍按原规则拒绝。
		`{"format":1,"id":"x","state":{},"extra":1,"extra":2}`,
		// 多余对象内部精确键重复。
		`{"format":1,"id":"x","state":{},"extra":{"a":1,"a":2}}`,
		// 多余对象中同一名称的直接写法与 Unicode 转义写法。
		"{\"format\":1,\"id\":\"x\",\"state\":{},\"extra\":{\"a\":1,\"\\u0061\":2}}",
	}
	for _, doc := range bad {
		err := checkDuplicateNames([]byte(doc))
		if err == nil || !strings.Contains(err.Error(), "重复") {
			t.Fatalf("多余对象内精确键重复应判损坏，得到 %v\n%s", err, doc)
		}
	}
}

// TestDerivedFieldNamesUseJSONTags 派生字段名必须取自 json 标签而非 Go
// 字段名：信封字段是小写标签（format/id/...），状态等嵌套结构是大写字段
// 名。在信封层注入与标签折叠的 "Format" 应损坏，注入一个不与任何标签
// 折叠的键应作为多余键完好接受。
func TestDerivedFieldNamesUseJSONTags(t *testing.T) {
	// "Format" 与标签 "format" 会被读取为同一固定字段。
	doc := `{"format":1,"Format":1,"id":"x","state":{}}`
	if err := checkDuplicateNames([]byte(doc)); err == nil {
		t.Fatal("信封层 Format 与标签 format 应折叠为同一固定字段并判损坏")
	}
	// "StateX" 不与任何信封标签折叠，作为多余键允许。
	doc = `{"format":1,"id":"x","state":{},"StateX":1}`
	if err := checkDuplicateNames([]byte(doc)); err != nil {
		t.Fatalf("不映射到任何标签的多余键应允许: %v", err)
	}
}
