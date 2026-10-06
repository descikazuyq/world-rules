package world

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// RecordID 是一次成功保存产生的、在同一存档目录内唯一的记录标识。
type RecordID string

// archiveFormatVersion 是存档目录布局版本，写入目录标记中。
const archiveFormatVersion = 1

// recordFormatVersion 是当前唯一支持的记录格式版本。记录格式与存档目录
// 布局版本是两个独立的编号：目录可打开不代表其中每条记录都能被理解，
// 格式编号不是该值的记录（缺失、零、负值或其他正整数）一律按损坏处理，
// 即使其校验和匹配、世界状态合法。
const recordFormatVersion = 1

// envelope 是记录文件在磁盘上的完整编码。
type envelope struct {
	// Format 是记录格式版本。
	Format int `json:"format"`
	// ID 是本条记录的唯一标识。
	ID RecordID `json:"id"`
	// Parent 是父记录标识；直接建立的槽首条记录为空。
	Parent RecordID `json:"parent,omitempty"`
	// SlotFirst 标记本条记录是否为其所在槽的首条记录
	// （直接建立的根记录，或从其他槽分出的分支记录）。
	SlotFirst bool `json:"slotFirst"`
	// Checksum 是对（格式版本、ID、父记录、首记录标记、完整世界状态）
	// 计算出的内容校验和，因此父记录关系也被纳入校验。
	Checksum string `json:"checksum"`
	// State 是完整世界数据：种子、规则与全部状态。
	State State `json:"state"`
}

// checksumPayload 是参与校验和计算的规范内容。
type checksumPayload struct {
	Format    int      `json:"format"`
	ID        RecordID `json:"id"`
	Parent    RecordID `json:"parent"`
	SlotFirst bool     `json:"slotFirst"`
	State     State    `json:"state"`
}

func computeChecksum(e *envelope) string {
	p := checksumPayload{
		Format:    e.Format,
		ID:        e.ID,
		Parent:    e.Parent,
		SlotFirst: e.SlotFirst,
		State:     e.State,
	}
	// encoding/json 对 map 按键排序输出，同一状态总是得到相同字节。
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// newRecordID 产生目录内唯一的记录标识。
func newRecordID() (RecordID, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return RecordID("r" + hex.EncodeToString(b[:])), nil
}

func recordPath(dir string, id RecordID) string {
	return filepath.Join(dir, "records", string(id)+".json")
}

// writeRecord 将一条记录以“先临时文件后原子改名”的方式落盘，
// 并校验目标文件名此前不存在，保证记录标识不会复用或混写。
func writeRecord(slotDir string, e *envelope) error {
	final := recordPath(slotDir, e.ID)
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}

	// O_EXCL：即便极小概率撞到重复 ID，也绝不会覆盖别的记录。
	f, err := os.OpenFile(final+".tmp",
		os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	tmpName := f.Name()
	clean := true
	defer func() {
		if clean {
			os.Remove(tmpName)
		}
	}()

	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		return err
	}
	clean = false
	if err := syncDir(filepath.Dir(final)); err != nil {
		return err
	}
	return nil
}

// loadRecord 读取并解析一条记录文件，但不做校验。
//
// 解析前先做文本编码检查：encoding/json 会把记录中的无效 UTF-8 字节和
// 字符串值、对象键里不成对的 Unicode 代理项转义悄悄改写成替换字符“�”，
// 随后才做内容校验——若原记录本就含有合法的“�”，改写后的文本甚至可能
// 仍通过校验和检查，文件损坏便被掩盖。因此无效字节与不成对的代理项转义
// 必须在解析前按原始字节拒绝，整条记录视为损坏，绝不用替换字符补齐后
// 继续交付。
//
// 解析后还要检查同一对象内是否有重复字段：encoding/json 对同名键静默采用
// 后一个值，而且结构体字段匹配本身忽略大小写——先写 "Time": -1 再写
// "time": 0，两个键都会写入同一个固定字段，只要解码后的内容与校验和匹配，
// 一份内部自相矛盾的记录就会被当作完好记录交付。因此同一对象内只要两个
// 名称会被读取为同一个固定字段（拼写相同，或仅大小写/Unicode 转义写法
// 不同），无论两个值是否相同、采用后一个值后的世界是否合法、校验和是否
// 匹配，整条记录都视为损坏。
func loadRecord(path string) (*envelope, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if err := checkRecordText(data); err != nil {
		return nil, err
	}
	var e envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("记录无法解析: %w", err)
	}
	if err := checkDuplicateNames(data); err != nil {
		return nil, err
	}
	return &e, nil
}

// checkDuplicateNames 检查 JSON 文本中同一对象内是否有重复字段。
//
// 判定标准与记录解码完全一致：两个名称是否会被读取为同一个固定字段。
// 编码格式与各层级的固定字段（含大小写忽略的 Go 名称和 JSON 标签）固定
// 且互不相同，检查器据此沿对象栈定位当前对象的固定字段集合：
//
//   - 结构体型对象中，键按名称还原后忽略大小写归并（复刻 encoding/json
//     对无标签导出字段的匹配：先精确匹配，再按 foldName 匹配；本记录格式
//     没有在忽略大小写后互相冲突的固定字段）。因此 "Time" 与 "time"、
//     "time" 与 "Time" 都写入同一字段，同一对象内第二次出现即重复。
//   - 携带上限（CarryLimits）是名称到数值的映射而不是结构体：其键是实际
//     角色标识，"hero" 与 "HERO" 是两个不同角色，必须各自保留，绝不按
//     大小写合并或误报；判定与 map 解码一样按还原后的精确字符串比较。
//
// 名称按 JSON 转义还原后的字符串比较：直接写出的名称与表示同一名称的
// Unicode 转义（如 "Time" 与 "time"）视为同一名称，不能借转义
// 绕过限制。判定严格按对象边界：两个不同对象各自使用相同名称是正常的
// （例如两个物品条目各自有数量字段），只有同一对象内一个固定字段被提供
// 两次才报错；字符串值中的文字与标点不算对象名称。记录文本已经通过
// json.Unmarshal 解析，这里只做重复字段判定，语法问题不再出现。
//
// 不映射到任何固定字段的未知键在读取时被忽略；它们彼此完全同名仍沿用既有
// 的同名重复拒绝，但大小写不同的未知键不会被误报，也不可能遮住固定字段。
func checkDuplicateNames(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	// UseNumber 避免数值范围差异：这里只关心对象结构与名称，不解释数值。
	dec.UseNumber()
	// kind 描述解析栈中一层容器的类型。
	type kind int
	const (
		kIgnore    kind = iota // 不对应固定结构的对象（防御性，正常记录不出现）
		kArray                 // 数组：只占位，其中的字符串是值而不是名称
		kEnvelope              // 记录信封
		kState                 // 世界状态
		kRules                 // 规则
		kEdge                  // 道路条目
		kCharacter             // 角色条目
		kItem                  // 物品条目
		kStringMap             // 携带上限 map：键是实际角色标识
	)
	isObject := func(k kind) bool { return k != kArray }
	// fixedFields 给出每种结构体型对象的固定字段，键为复刻 encoding/json
	// foldName 的折叠名称；与 types.go 的结构体定义及 envelope 的 JSON 标签
	// 一一对应。这些固定字段折叠后互不相同，因此忽略大小写不会让两个不同
	// 的固定字段撞在一起。
	fixedFields := map[kind]map[string]bool{
		kEnvelope: {"FORMAT": true, "ID": true, "PARENT": true, "SLOTFIRST": true,
			"CHECKSUM": true, "STATE": true},
		kState: {"SEED": true, "RULES": true, "TIME": true, "CHARACTERS": true},
		kRules: {"VERSION": true, "LOCATIONS": true, "EDGES": true,
			"ITEMKINDS": true, "CARRYLIMITS": true},
		kEdge:      {"FROM": true, "TO": true},
		kCharacter: {"ID": true, "LOCATION": true, "ITEMS": true},
		kItem:      {"ITEM": true, "COUNT": true},
	}
	// childSpec 描述某固定字段的值是哪种容器：obj 为对象类型，arr 为数组
	// 时其元素对象的类型（kIgnore 表示标量数组）。
	type childSpec struct {
		obj kind
		arr kind
	}
	fieldSpecs := map[kind]map[string]childSpec{
		kEnvelope: {"STATE": {obj: kState}},
		kState:    {"RULES": {obj: kRules}, "CHARACTERS": {arr: kCharacter}},
		kRules: {
			"LOCATIONS":   {arr: kIgnore},
			"EDGES":       {arr: kEdge},
			"ITEMKINDS":   {arr: kIgnore},
			"CARRYLIMITS": {obj: kStringMap},
		},
		kCharacter: {"ITEMS": {arr: kItem}},
	}
	// frame 是解析栈中的一层容器。
	type frame struct {
		k         kind
		seen      map[string]bool // 已提供的固定字段（规范名）或 map 键（精确名）
		seenRaw   map[string]bool // 已出现的键的精确名（保留对未知键同名重复的拒绝）
		expectKey bool
		pending   childSpec // 最近一个键对应值的容器描述，仅下一个值有效
		elem      kind      // 仅数组帧：其元素对象的类型
	}
	var stack []*frame
	// canonicalField 返回 key 在该结构体对象中会被读入的固定字段规范名
	// （大写名称）；不属于任何固定字段时返回空。判定复刻 encoding/json：
	// 先按精确名匹配、再忽略大小写匹配（与 strings.EqualFold 同语义，也
	// 覆盖 Unicode 转义还原后的名称）。本格式的固定字段折叠后互不相同。
	canonicalField := func(k kind, key string) string {
		for f := range fixedFields[k] {
			if strings.EqualFold(f, key) {
				return f
			}
		}
		return ""
	}
	// 一个值（标量或容器）结束后，父对象等待下一个键；数组帧不等待键。
	markValueDone := func() {
		if n := len(stack); n > 0 && isObject(stack[n-1].k) {
			stack[n-1].expectKey = true
		}
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			// 文本已通过 json.Unmarshal，正常只会遇到 io.EOF；其余情况
			// 不在这里报告。
			return nil
		}
		if n := len(stack); n > 0 && isObject(stack[n-1].k) && stack[n-1].expectKey {
			top := stack[n-1]
			if d, ok := tok.(json.Delim); ok && d == '}' {
				stack = stack[:n-1]
				markValueDone()
				continue
			}
			key, ok := tok.(string)
			if !ok {
				return nil
			}
			if top.k == kStringMap {
				// map 的键是实际角色标识：按还原后的精确字符串区分，
				// hero 与 HERO 是两个角色；只有完全相同的键才算重复。
				if top.seen[key] {
					return fmt.Errorf("同一对象内名称 %q 重复出现", key)
				}
				top.seen[key] = true
			} else {
				// 未知键（读取时忽略）沿用原有同名重复拒绝；不同大小写的
				// 未知键不映射到任何固定字段，不视为重复。
				if top.seenRaw[key] {
					return fmt.Errorf("同一对象内名称 %q 重复出现", key)
				}
				top.seenRaw[key] = true
				name := canonicalField(top.k, key)
				if name != "" {
					// 两个键会被读入同一个固定字段（拼写相同，或仅大小写/
					// Unicode 转义写法不同）即重复；后一个值不能遮住前一个。
					if top.seen[name] {
						return fmt.Errorf("同一对象内固定字段 %q 重复出现（字段名称忽略大小写与 Unicode 转义写法）", key)
					}
					top.seen[name] = true
				}
				top.pending = fieldSpecs[top.k][name]
			}
			top.expectKey = false
			continue
		}
		switch d := tok.(type) {
		case json.Delim:
			switch d {
			case '{':
				var nk kind
				if len(stack) == 0 {
					nk = kEnvelope // 根对象是记录信封
				} else if top := stack[len(stack)-1]; top.k == kArray {
					nk = top.elem
				} else {
					nk = stack[len(stack)-1].pending.obj
					stack[len(stack)-1].pending = childSpec{}
				}
				fr := &frame{k: nk, expectKey: true}
				if isObject(nk) {
					fr.seen = make(map[string]bool)
					if nk != kStringMap {
						fr.seenRaw = make(map[string]bool)
					}
				}
				stack = append(stack, fr)
			case '[':
				elem := kIgnore
				if n := len(stack); n > 0 && isObject(stack[n-1].k) {
					elem = stack[n-1].pending.arr
					stack[n-1].pending = childSpec{}
				}
				stack = append(stack, &frame{k: kArray, elem: elem})
			case '}', ']':
				stack = stack[:len(stack)-1]
				markValueDone()
			}
		default:
			// 标量值：消费掉键上记录的容器描述，父对象继续等待下一个键。
			if n := len(stack); n > 0 && isObject(stack[n-1].k) {
				stack[n-1].pending = childSpec{}
			}
			markValueDone()
		}
	}
}

// checkRecordText 校验记录文件原始字节的文本编码：JSON 文本必须是合法
// UTF-8；字符串值与对象键中的 \uXXXX 转义若是代理项，必须完整配对——
// 高位代理项（D800–DBFF）后必须紧接一个低位代理项转义（DC00–DFFF），
// 低位代理项不得单独出现。合法内容不受影响：真实存在的“�”、正确配对
// 的代理项转义与同一字符的合法转义写法都照常接受；已经转义的反斜杠
// （\\）其后的 uD800 只是普通文字，不是代理项转义。
func checkRecordText(data []byte) error {
	if !utf8.Valid(data) {
		return errors.New("记录包含无效 UTF-8 字节，文本编码损坏")
	}
	// 只需定位字符串字面量并检查其中的 \u 转义；JSON 其余部分的合法性
	// 由 json.Unmarshal 判断。data 已是合法 UTF-8，多字节字符中不可能
	// 出现 '"' 或 '\\' 字节。
	i := 0
	for i < len(data) {
		if data[i] != '"' {
			i++
			continue
		}
		i++ // 进入字符串字面量
		for i < len(data) {
			c := data[i]
			if c == '"' {
				i++
				break
			}
			if c != '\\' {
				i++
				continue
			}
			// 转义序列：\\ 是已转义的反斜杠，其后的 uD800 只是普通文字。
			i++
			if i >= len(data) {
				break // 截断的转义由 json.Unmarshal 报解析错误
			}
			esc := data[i]
			i++
			if esc != 'u' {
				continue
			}
			if i+4 > len(data) {
				break // 截断的转义由 json.Unmarshal 报解析错误
			}
			v, ok := hex4(data[i : i+4])
			i += 4
			if !ok {
				continue // 非法十六进制由 json.Unmarshal 报解析错误
			}
			switch {
			case v >= 0xD800 && v <= 0xDBFF:
				// 高位代理项必须紧接一个低位代理项转义才完整。
				if i+6 <= len(data) && data[i] == '\\' && data[i+1] == 'u' {
					if lo, ok := hex4(data[i+2 : i+6]); ok && lo >= 0xDC00 && lo <= 0xDFFF {
						i += 6
						continue
					}
				}
				return fmt.Errorf("记录包含未配对的高位代理项转义 \\u%04X，Unicode 转义损坏", v)
			case v >= 0xDC00 && v <= 0xDFFF:
				return fmt.Errorf("记录包含单独出现的低位代理项转义 \\u%04X，Unicode 转义损坏", v)
			}
		}
	}
	return nil
}

// hex4 解析 4 位十六进制数字（\uXXXX 转义的码位部分）。
func hex4(b []byte) (int, bool) {
	v := 0
	for _, c := range b {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= int(c - '0')
		case c >= 'a' && c <= 'f':
			v |= int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= int(c-'A') + 10
		default:
			return 0, false
		}
	}
	return v, true
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// writeAtomic 把 data 写入 dir 下的 name：同目录临时文件、fsync、
// 原子改名，保证崩溃后只会看到旧文件或新文件。
func writeAtomic(dir, name string, data []byte) error {
	tmp := filepath.Join(dir, ".tmp-"+name+"-"+randomSuffix())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	clean := true
	defer func() {
		if clean {
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	final := filepath.Join(dir, name)
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	clean = false
	return syncDir(dir)
}

func randomSuffix() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
