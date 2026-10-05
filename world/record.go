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
	var e envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("记录无法解析: %w", err)
	}
	return &e, nil
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
