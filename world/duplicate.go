package world

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// 记录读入时的重复名称检查直接建立在真实记录结构之上：检查所认识的固定
// 字段、字段名称（含 json 标签）与嵌套关系全部由 envelope 及其引用到的
// 结构体经反射派生，不再手工登记第二份字段清单。结构体增删或改名字段、
// 调整 json 标签或嵌套后，重复检查随之自动一致，无需再同步维护另一张表。
//
// 派生规则与 encoding/json 的解码目标一一对应：
//   - 结构体对象的每个可导出且未标记 "-" 的字段是一个固定字段，字段名取
//     json 标签名（无标签时取 Go 字段名）；encoding/json 对固定字段做大小
//     写不敏感匹配，所以重复判定也按字段名折叠大小写与 Unicode 转义写法
//     （"Time" 与 "time" 是同一个固定字段），折叠方式与旧实现一致，使用
//     strings.EqualFold。
//   - 解码到 map 的对象（携带上限 map[string]int）没有固定字段，其键是真实
//     业务标识（角色名），按 Unicode 转义还原后的精确名称比较：hero 与
//     HERO 是两个角色，必须分别保留；同一精确键写两次才算重复。
//   - 不属于记录结构的多余对象（被忽略的未知字段值）同样没有固定字段，
//     其中的键按还原后的精确名称判定：精确重复拒绝，不把未知名称一律当成
//     固定字段做大小写折叠。
//
// 固定字段的 Go 类型决定嵌套：结构体/结构体指针是对象子节点，数组/切片是
// 数组子节点（元素是结构体时其元素对象登记为对应结构体），其余为标量。
// 这与 encoding/json 实际会进入的容器完全相同，包括 map[string]int 这个
// 唯一解码到 map 的字段。

// recordRoot 是重复名称检查遍历的根类型，即记录文件在磁盘上的完整结构。
// 记录结构发生变化时，检查经由这里自动跟随，无需另行登记字段。
var recordRoot = reflect.TypeOf(envelope{})

// unknownObject 标记一个不属于记录结构的多余对象（被忽略的未知字段值）：
// 其中没有固定字段，键按还原后的精确名称判定。它不是结构表的下标。
const unknownObject = -1

// recordObject 描述检查遍历时一类 JSON 对象的结构，全部由真实 Go 类型派生。
type recordObject struct {
	// fixed 为 true 时该对象解码到结构体：fields 列出它的固定字段，重复
	// 判定按字段名大小写不敏感归并。为 false 时对象解码到 map（携带上限）：
	// 没有固定字段，键按还原后的精确名称判定。
	fixed bool
	// fields 按 Go 字段声明顺序保存固定字段的 JSON 名称与值容器信息。
	fields []recordField
}

// recordField 是一个固定字段的 JSON 名称与其值对应的 JSON 容器类型。
type recordField struct {
	name string
	ch   byte // 0 表示标量；'{' 表示对象，'[' 表示数组
	// obj 为 ch=='{' 时给出值对象的结构表下标（结构体或携带上限 map）；
	// elem 为 ch=='[' 且数组元素是结构体时给出元素对象的结构表下标；
	// 其余情况下标为 unknownObject。
	obj  int
	elem int
}

// recordObjects 是按真实类型构建出的对象结构描述表，根信封位于下标 0，
// 其余为遍历中遇到的结构体与 map 类型。记录结构变化时此表自动重建，
// 不存在需要手工同步的第二份字段清单。
var recordObjects = buildRecordObjects(recordRoot)

func buildRecordObjects(root reflect.Type) []recordObject {
	// 类型 -> 描述下标。结构体与 map 各自登记；多余对象统一用
	// unknownObject，不进此表。
	index := map[reflect.Type]int{}
	var objs []recordObject

	var ensure func(t reflect.Type) int
	ensure = func(t reflect.Type) int {
		if i, ok := index[t]; ok {
			return i
		}
		i := len(objs)
		index[t] = i
		fixed := t.Kind() == reflect.Struct
		// 先占位，使自引用/互相引用的字段在递归中能找到本表。
		objs = append(objs, recordObject{fixed: fixed})
		if !fixed {
			return i // map 对象：无固定字段，键是真实业务标识。
		}

		obj := recordObject{fixed: true}
		for fi, n := 0, t.NumField(); fi < n; fi++ {
			sf := t.Field(fi)
			if sf.PkgPath != "" || sf.Tag.Get("json") == "-" {
				continue // 非导出字段或显式不参与 JSON 的字段
			}
			f := recordField{name: jsonFieldName(sf), obj: unknownObject, elem: unknownObject}
			switch ft := sf.Type; ft.Kind() {
			case reflect.Struct:
				f.ch = '{'
				f.obj = ensure(ft)
			case reflect.Ptr:
				if ft.Elem().Kind() == reflect.Struct {
					f.ch = '{'
					f.obj = ensure(ft.Elem())
				}
			case reflect.Map:
				// 记录结构中唯一的 map 字段是携带上限 map[string]int；
				// 其键是真实角色标识，不按固定字段折叠。
				f.ch = '{'
				f.obj = ensure(ft)
			case reflect.Slice, reflect.Array:
				f.ch = '['
				if et := ft.Elem(); et.Kind() == reflect.Struct {
					f.elem = ensure(et)
				} else if et.Kind() == reflect.Ptr && et.Elem().Kind() == reflect.Struct {
					f.elem = ensure(et.Elem())
				}
			}
			obj.fields = append(obj.fields, f)
		}
		objs[i] = obj
		return i
	}

	ensure(root)
	return objs
}

// jsonFieldName 返回字段在 JSON 中的名称：优先取 json 标签的名称段，
// 无标签时取 Go 字段名；omitempty 等选项不影响名称。
func jsonFieldName(sf reflect.StructField) string {
	tag := sf.Tag.Get("json")
	if tag == "" {
		return sf.Name
	}
	if comma := strings.IndexByte(tag, ','); comma >= 0 {
		tag = tag[:comma]
	}
	if tag == "" {
		return sf.Name
	}
	return tag
}

// fieldIndex 返回对象 objIdx 中 JSON 键 key 会被 encoding/json 读取到的
// 固定字段下标；key 不对应任何固定字段，或该对象不是结构体对象时返回
// false。匹配按声明顺序用 strings.EqualFold 做大小写不敏感比较，与
// encoding/json 的字段选择及此前的检查行为一致；key 已经是 Unicode
// 转义还原后的字符串。
func fieldIndex(objIdx int, key string) (int, bool) {
	if objIdx == unknownObject || objIdx < 0 || objIdx >= len(recordObjects) {
		return 0, false
	}
	obj := recordObjects[objIdx]
	if !obj.fixed {
		return 0, false
	}
	for i := range obj.fields {
		if strings.EqualFold(obj.fields[i].name, key) {
			return i, true
		}
	}
	return 0, false
}

// checkDuplicateNames 检查 JSON 文本中同一对象内的名称是否重复出现。
//
// 名称按 JSON 转义还原后的字符串比较：直接写出的名称与表示同一名称的
// Unicode 转义（如 "ID" 与写作转义形式的同一名称）视为同一名称，不能
// 借此绕过限制。
//
// 对解码到结构体的对象（记录顶层、世界状态、规则、道路、角色、物品条目），
// 判定与 encoding/json 选择字段的方式一致：固定字段名大小写不敏感，只要
// 两个名称会被读取为同一个固定字段（如 "Time" 与 "time"），即使拼写大小
// 写不同、两个值相同或先后顺序不同，也算重复。解码到 map 的携带上限对象
// 不同：其中的键是实际角色标识，按还原后的精确名称比较，hero 与 HERO 是
// 两个角色，不能合并或误报。
//
// 判定严格按对象边界：两个不同对象各自使用相同名称是正常的（例如两个物品
// 条目各自有数量字段），只有同一对象内名称第二次出现才报错；字符串值中的
// 文字与标点不算对象名称。记录文本已经通过 json.Unmarshal 解析，这里只做
// 重复名称判定，语法问题不再出现。
func checkDuplicateNames(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	// UseNumber 避免数值范围差异：这里只关心对象结构与名称，不解释数值。
	dec.UseNumber()
	// frame 描述解析栈中的一层容器。对象记录已见过的名称身份：结构体对象
	// 按真实结构派生的固定字段归并大小写变体，map 与多余对象用还原后的
	// 精确键；expectKey 跟踪下一个 token 是否为键。数组只占位并记录其
	// 元素的对象类型，其中的字符串是值而不是名称。
	type frame struct {
		isObj      bool
		obj        int // 对象帧使用：结构表下标；多余对象为 unknownObject
		elem       int // 仅数组帧使用：元素是结构体时的对象表下标
		elemValid  bool
		seen       map[string]bool
		expectKey  bool
		pendingKey string // 刚读入、其值尚未开始的键（对象帧使用）
	}
	// fieldAt 返回对象帧 f 中字段 field（大小写不敏感）在其值容器为 want
	// （'{' 或 '['）时登记的子对象表下标。
	fieldAt := func(f *frame, field string, want byte) (int, bool) {
		i, ok := fieldIndex(f.obj, field)
		if !ok {
			return 0, false
		}
		df := recordObjects[f.obj].fields[i]
		if df.ch != want {
			return 0, false
		}
		if want == '{' {
			return df.obj, df.obj != unknownObject
		}
		return df.elem, df.elem != unknownObject
	}
	var stack []*frame
	pushObject := func(obj int) {
		stack = append(stack, &frame{
			isObj:     true,
			obj:       obj,
			seen:      make(map[string]bool),
			expectKey: true,
		})
	}
	// 一个值（标量或容器）结束后，父对象等待下一个键。
	markValueDone := func() {
		if n := len(stack); n > 0 && stack[n-1].isObj {
			stack[n-1].expectKey = true
		}
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			// 文本已通过 json.Unmarshal，正常只会遇到 io.EOF；其余
			// 情况不在这里报告。
			return nil
		}
		if n := len(stack); n > 0 && stack[n-1].isObj && stack[n-1].expectKey {
			if d, ok := tok.(json.Delim); ok && d == '}' {
				stack = stack[:n-1]
				continue
			}
			key, ok := tok.(string)
			if !ok {
				return nil
			}
			parent := stack[n-1]
			// 消息口径与旧实现一致，按对象种类选择：结构体对象内的重复
			// （含其中被忽略的多余键精确重复）提示固定字段折叠；携带
			// 上限 map 与多余对象提示精确名称重复。
			isStructObj := parent.obj != unknownObject &&
				parent.obj < len(recordObjects) && recordObjects[parent.obj].fixed
			// 结构体按键会读取到的固定字段归并大小写变体；携带上限 map
			// 与多余对象保留还原后的精确名称。
			identity := key
			if i, ok := fieldIndex(parent.obj, key); ok {
				identity = recordObjects[parent.obj].fields[i].name
			}
			if parent.seen[identity] {
				if !isStructObj {
					return fmt.Errorf("同一对象内名称 %q 重复出现", key)
				}
				return fmt.Errorf("同一对象内名称 %q 重复出现：与先前某名称会被读取为同一个固定字段", key)
			}
			parent.seen[identity] = true
			parent.expectKey = false
			parent.pendingKey = key
			continue
		}
		d, isDelim := tok.(json.Delim)
		if !isDelim {
			markValueDone()
			continue
		}
		switch d {
		case '{':
			obj := unknownObject
			if n := len(stack); n > 0 {
				parent := stack[n-1]
				switch {
				case parent.isObj:
					// 父对象键对应的值对象：类型由真实结构派生（如 State、
					// Rules 是结构体，CarryLimits 是 map）。未登记的是被
					// 结构体忽略的多余字段，按未知对象处理。
					if k, ok := fieldAt(parent, parent.pendingKey, '{'); ok {
						obj = k
					}
				case parent.elemValid:
					// 数组中的对象元素：结构体类型由数组帧登记。
					obj = parent.elem
				}
			} else {
				// 根对象是记录信封（结构表第 0 项）。
				obj = 0
			}
			markValueDone()
			pushObject(obj)
		case '[':
			var elem int
			var valid bool
			if n := len(stack); n > 0 && stack[n-1].isObj {
				parent := stack[n-1]
				if i, ok := fieldIndex(parent.obj, parent.pendingKey); ok {
					df := recordObjects[parent.obj].fields[i]
					if df.ch == '[' {
						elem, valid = df.elem, df.elem != unknownObject
					}
				}
			}
			markValueDone()
			stack = append(stack, &frame{elem: elem, elemValid: valid})
		case '}', ']':
			stack = stack[:len(stack)-1]
			markValueDone()
		}
	}
}
