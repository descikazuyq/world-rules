package world

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
// 解析后还要检查同一对象内的名称是否重复出现：encoding/json 对同名字段
// 静默采用后一个值，只要解码后的内容与校验和匹配，一份内部自相矛盾的
// 记录（例如先写一个负的时间片、再用同名字段写回合法值）就会被当作完好
// 记录交付。因此同一对象内任何名称出现第二次，无论两个值是否相同、采用
// 后一个值后的世界是否合法、校验和是否匹配，整条记录都视为损坏。
//
// 对会解码到结构体固定字段的对象，判定与 encoding/json 的字段选择一致：
// 它对结构体字段名做大小写不敏感匹配，所以 "Time" 与 "time"、"ID" 与
// "id" 读写的是同一个固定字段，只换大小写就算重复，不能让后一个值遮住
// 前一个值。名称到数值的携带上限是 map：其键是实际角色标识而非固定字段，
// "hero" 与 "HERO" 是两个角色，仍按还原后的精确名称区分。
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

// checkRecordText 校验记录文件原始字节的文本编码，规则见 checkJSONText。
func checkRecordText(data []byte) error {
	return checkJSONText(data, "记录")
}

// checkSlotPointerText 校验槽指针文件原始字节的文本编码，规则见
// checkJSONText。槽指针与记录一样会被 encoding/json 悄悄改写损坏文本，
// 因此必须在解析前按原始字节拒绝。
func checkSlotPointerText(data []byte) error {
	return checkJSONText(data, "槽指针")
}

// checkJSONText 校验 JSON 文本原始字节的文本编码：文本必须是合法
// UTF-8；字符串值与对象键中的 \uXXXX 转义若是代理项，必须完整配对——
// 高位代理项（D800–DBFF）后必须紧接一个低位代理项转义（DC00–DFFF），
// 低位代理项不得单独出现。合法内容不受影响：真实存在的“�”、正确配对
// 的代理项转义与同一字符的合法转义写法都照常接受；已经转义的反斜杠
// （\\）其后的 uD800 只是普通文字，不是代理项转义。what 是出错原因中
// 对这份文本的称呼（如“记录”“槽指针”）。
func checkJSONText(data []byte, what string) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("%s包含无效 UTF-8 字节，文本编码损坏", what)
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
				return fmt.Errorf("%s包含未配对的高位代理项转义 \\u%04X，Unicode 转义损坏", what, v)
			case v >= 0xDC00 && v <= 0xDFFF:
				return fmt.Errorf("%s包含单独出现的低位代理项转义 \\u%04X，Unicode 转义损坏", what, v)
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
