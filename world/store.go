package world

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// 存档相关错误。调用方可以用 errors.Is 判定。
var (
	// ErrSlotExists 表示存档槽已存在（首次保存或分支时重名）。
	ErrSlotExists = errors.New("world: 存档槽已存在")
	// ErrConflict 表示覆盖失败：调用方持有的记录标识已不是该槽最新记录。
	ErrConflict = errors.New("world: 记录已被其他写入覆盖")
	// ErrSlotNotFound 表示存档槽不存在。
	ErrSlotNotFound = errors.New("world: 存档槽不存在")
	// ErrRecordNotFound 表示记录文件不存在。
	ErrRecordNotFound = errors.New("world: 记录不存在")
	// ErrCorrupt 表示记录内容损坏（校验和不符或无法解析）。
	ErrCorrupt = errors.New("world: 记录校验失败")
	// ErrVersion 表示记录的规则版本不在调用方可接受集合中。
	ErrVersion = errors.New("world: 规则版本不被接受")
	// ErrUnrecoverable 表示该槽没有校验通过且版本可接受的历史记录。
	ErrUnrecoverable = errors.New("world: 没有可恢复的记录")
)

// RecordInfo 是记录的元信息。
type RecordInfo struct {
	// ID 是目录内唯一的记录标识。
	ID string
	// ParentID 是父记录标识；直接建立的槽首条记录为空。
	ParentID string
	// Version 是记录中世界的规则版本。
	Version string
	// Time 是记录中的时间片。
	Time int
}

// Store 是建立在指定目录上的本地存档。
//
// 目录结构：
//
//	<dir>/slots.json      槽位清单（原子更新）
//	<dir>/records/<id>.json  记录文件（临时文件 + 原子 rename）
//	<dir>/lock            跨进程互斥锁
//
// 多个进程或同一进程的多个实例可以同时打开同一目录：
// 写操作在文件锁内进行“检查 latest + 写入”，覆盖同一父记录时
// 只有一个调用成功，其余得到 ErrConflict。
type Store struct {
	dir  string
	mu   sync.Mutex
	lock *os.File
}

type manifest struct {
	Slots map[string]slotEntry `json:"slots"`
}

type slotEntry struct {
	Latest string `json:"latest"`
}

type recordFile struct {
	ID       string          `json:"id"`
	Parent   string          `json:"parent,omitempty"`
	World    json.RawMessage `json:"world"`
	Checksum string          `json:"checksum"`
}

// Open 打开（必要时创建）指定目录上的存档。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "records"), 0o755); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, lock: lock}
	if err := s.withLock(func() error {
		return s.ensureManifestLocked()
	}); err != nil {
		lock.Close()
		return nil, err
	}
	return s, nil
}

// Close 释放存档占用的资源。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}

// Create 首次保存：创建存档槽并写入首条记录（无父记录）。
// 槽名已存在时返回 ErrSlotExists。
func (s *Store) Create(slot string, w *World) (string, error) {
	if slot == "" {
		return "", errors.New("world: 存档槽名不能为空")
	}
	if w == nil {
		return "", errors.New("world: 世界不能为空")
	}
	var id string
	err := s.withLock(func() error {
		m, err := s.readManifestLocked()
		if err != nil {
			return err
		}
		if _, exists := m.Slots[slot]; exists {
			return fmt.Errorf("%w: %q", ErrSlotExists, slot)
		}
		rf, err := s.buildRecordLocked("", w)
		if err != nil {
			return err
		}
		if err := s.writeRecordLocked(rf); err != nil {
			return err
		}
		m.Slots[slot] = slotEntry{Latest: rf.ID}
		if err := s.writeManifestLocked(m); err != nil {
			return err
		}
		id = rf.ID
		return nil
	})
	return id, err
}

// Overwrite 覆盖存档槽的最新记录。expectedRecord 是调用方此前读到的
// 记录标识；只有它仍是该槽最新记录时才允许写入，否则返回 ErrConflict。
// 新记录的父记录是 expectedRecord。
func (s *Store) Overwrite(slot, expectedRecord string, w *World) (string, error) {
	if slot == "" {
		return "", errors.New("world: 存档槽名不能为空")
	}
	if expectedRecord == "" {
		return "", fmt.Errorf("%w: 覆盖必须提供此前读到的记录标识", ErrConflict)
	}
	if w == nil {
		return "", errors.New("world: 世界不能为空")
	}
	var id string
	err := s.withLock(func() error {
		m, err := s.readManifestLocked()
		if err != nil {
			return err
		}
		e, ok := m.Slots[slot]
		if !ok {
			return fmt.Errorf("%w: %q", ErrSlotNotFound, slot)
		}
		if e.Latest != expectedRecord {
			return fmt.Errorf("%w: 存档槽 %q 的最新记录是 %s，调用方持有的是 %s",
				ErrConflict, slot, e.Latest, expectedRecord)
		}
		rf, err := s.buildRecordLocked(expectedRecord, w)
		if err != nil {
			return err
		}
		if err := s.writeRecordLocked(rf); err != nil {
			return err
		}
		e.Latest = rf.ID
		m.Slots[slot] = e
		if err := s.writeManifestLocked(m); err != nil {
			return err
		}
		id = rf.ID
		return nil
	})
	return id, err
}

// Branch 从源存档槽的一条历史记录分出作为新槽。srcRecord 为空时
// 以源槽最新记录为父记录。新记录完整复制源记录的种子、规则和状态。
// 新槽名已存在时返回 ErrSlotExists。
func (s *Store) Branch(srcSlot, srcRecord, newSlot string) (string, error) {
	if srcSlot == "" || newSlot == "" {
		return "", errors.New("world: 存档槽名不能为空")
	}
	var id string
	err := s.withLock(func() error {
		m, err := s.readManifestLocked()
		if err != nil {
			return err
		}
		se, ok := m.Slots[srcSlot]
		if !ok {
			return fmt.Errorf("%w: %q", ErrSlotNotFound, srcSlot)
		}
		if _, exists := m.Slots[newSlot]; exists {
			return fmt.Errorf("%w: %q", ErrSlotExists, newSlot)
		}
		recID := srcRecord
		if recID == "" {
			recID = se.Latest
		}
		rf, err := s.readRecordFileLocked(recID)
		if err != nil {
			return err
		}
		// 分支前校验源记录完整（校验和）；分支原样复制规则，不做版本替换。
		if _, err := verifyRecord(rf, nil); err != nil {
			return fmt.Errorf("world: 源记录不可读取: %w", err)
		}
		nf := &recordFile{ID: newRecordID(), Parent: recID, World: rf.World}
		nf.World = canonicalJSON(nf.World)
		nf.Checksum = recordChecksum(nf.ID, nf.Parent, nf.World)
		if err := s.writeRecordLocked(nf); err != nil {
			return err
		}
		m.Slots[newSlot] = slotEntry{Latest: nf.ID}
		if err := s.writeManifestLocked(m); err != nil {
			return err
		}
		id = nf.ID
		return nil
	})
	return id, err
}

// Load 读取存档槽的最新记录，校验校验和（含父记录关系）和规则版本。
// acceptedVersions 是调用方明确提供的当前可接受版本集合；旧版本只允许
// 读取，记录中的规则不会被替换。返回世界与记录标识。
// 最新记录损坏或版本不被接受时返回带原因的错误（ErrCorrupt/ErrVersion），
// 调用方可另行调用 Recover。读取不改变任何存档。
func (s *Store) Load(slot string, acceptedVersions []string) (*World, string, error) {
	if slot == "" {
		return nil, "", errors.New("world: 存档槽名不能为空")
	}
	if len(acceptedVersions) == 0 {
		return nil, "", errors.New("world: 必须提供可接受的规则版本集合")
	}
	accepted := makeSet(acceptedVersions)

	var world *World
	var recID string
	err := s.withLock(func() error {
		m, err := s.readManifestLocked()
		if err != nil {
			return err
		}
		e, ok := m.Slots[slot]
		if !ok {
			return fmt.Errorf("%w: %q", ErrSlotNotFound, slot)
		}
		rf, err := s.readRecordFileLocked(e.Latest)
		if err != nil {
			return err
		}
		wd, err := verifyRecord(rf, accepted)
		if err != nil {
			return err
		}
		w, err := worldFromData(wd)
		if err != nil {
			return err
		}
		world = w
		recID = e.Latest
		return nil
	})
	return world, recID, err
}

// Recover 从最新记录开始沿父记录链向历史回溯，返回最近一份校验通过
// 且版本可接受的记录。没有这样的记录时返回 ErrUnrecoverable。
// 恢复读取不改变任何存档、历史记录或世界规则。
func (s *Store) Recover(slot string, acceptedVersions []string) (*World, string, error) {
	if slot == "" {
		return nil, "", errors.New("world: 存档槽名不能为空")
	}
	if len(acceptedVersions) == 0 {
		return nil, "", errors.New("world: 必须提供可接受的规则版本集合")
	}
	accepted := makeSet(acceptedVersions)

	var world *World
	var recID string
	err := s.withLock(func() error {
		m, err := s.readManifestLocked()
		if err != nil {
			return err
		}
		e, ok := m.Slots[slot]
		if !ok {
			return fmt.Errorf("%w: %q", ErrSlotNotFound, slot)
		}
		id := e.Latest
		for id != "" {
			rf, err := s.readRecordFileLocked(id)
			if err != nil {
				// 记录文件缺失或无法解析时无法继续回溯父链。
				return fmt.Errorf("%w: 存档槽 %q 的历史记录 %s 无法读取: %v",
					ErrUnrecoverable, slot, id, err)
			}
			wd, verr := verifyRecord(rf, accepted)
			if verr == nil {
				w, err := worldFromData(wd)
				if err != nil {
					return err
				}
				world = w
				recID = id
				return nil
			}
			id = rf.Parent
		}
		return fmt.Errorf("%w: 存档槽 %q 没有校验通过且版本可接受的历史记录",
			ErrUnrecoverable, slot)
	})
	return world, recID, err
}

// History 返回存档槽的记录元信息，顺序从最新到最旧。
// 覆盖后历史记录仍可按标识读取。
func (s *Store) History(slot string) ([]RecordInfo, error) {
	if slot == "" {
		return nil, errors.New("world: 存档槽名不能为空")
	}
	var infos []RecordInfo
	err := s.withLock(func() error {
		m, err := s.readManifestLocked()
		if err != nil {
			return err
		}
		e, ok := m.Slots[slot]
		if !ok {
			return fmt.Errorf("%w: %q", ErrSlotNotFound, slot)
		}
		id := e.Latest
		for id != "" {
			rf, err := s.readRecordFileLocked(id)
			if err != nil {
				return err
			}
			var wd WorldData
			if err := json.Unmarshal(rf.World, &wd); err != nil {
				return fmt.Errorf("%w: 记录 %s 世界数据解析失败: %v", ErrCorrupt, id, err)
			}
			infos = append(infos, RecordInfo{
				ID:       rf.ID,
				ParentID: rf.Parent,
				Version:  wd.Rules.Version,
				Time:     wd.Time,
			})
			id = rf.Parent
		}
		return nil
	})
	return infos, err
}

// LoadRecord 按标识读取任意一条历史记录（覆盖后仍可读取），
// 校验校验和与规则版本。
func (s *Store) LoadRecord(id string, acceptedVersions []string) (*World, RecordInfo, error) {
	if id == "" {
		return nil, RecordInfo{}, errors.New("world: 记录标识不能为空")
	}
	if len(acceptedVersions) == 0 {
		return nil, RecordInfo{}, errors.New("world: 必须提供可接受的规则版本集合")
	}
	accepted := makeSet(acceptedVersions)

	var world *World
	var info RecordInfo
	err := s.withLock(func() error {
		rf, err := s.readRecordFileLocked(id)
		if err != nil {
			return err
		}
		wd, err := verifyRecord(rf, accepted)
		if err != nil {
			return err
		}
		w, err := worldFromData(wd)
		if err != nil {
			return err
		}
		world = w
		info = RecordInfo{ID: rf.ID, ParentID: rf.Parent, Version: wd.Rules.Version, Time: wd.Time}
		return nil
	})
	return world, info, err
}

// Slots 返回目录中所有存档槽名，按字典序排列。
func (s *Store) Slots() ([]string, error) {
	var out []string
	err := s.withLock(func() error {
		m, err := s.readManifestLocked()
		if err != nil {
			return err
		}
		for name := range m.Slots {
			out = append(out, name)
		}
		sort.Strings(out)
		return nil
	})
	return out, err
}

// ---- 内部实现 ----

func (s *Store) withLock(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	return fn()
}

func (s *Store) manifestPath() string { return filepath.Join(s.dir, "slots.json") }

func (s *Store) recordPath(id string) string {
	return filepath.Join(s.dir, "records", id+".json")
}

func (s *Store) ensureManifestLocked() error {
	if _, err := os.Stat(s.manifestPath()); err == nil {
		return nil
	}
	m := &manifest{Slots: map[string]slotEntry{}}
	return s.writeManifestLocked(m)
}

func (s *Store) readManifestLocked() (*manifest, error) {
	data, err := os.ReadFile(s.manifestPath())
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Slots == nil {
		m.Slots = map[string]slotEntry{}
	}
	return &m, nil
}

func (s *Store) writeManifestLocked(m *manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.manifestPath(), data)
}

func (s *Store) buildRecordLocked(parent string, w *World) (*recordFile, error) {
	worldJSON, err := json.Marshal(w.Snapshot())
	if err != nil {
		return nil, err
	}
	rf := &recordFile{ID: newRecordID(), Parent: parent, World: worldJSON}
	// 校验和基于规范化 JSON：MarshalIndent 会重排 RawMessage 的缩进，
	// 读取侧 compact 后可还原出完全相同的字节。
	rf.World = canonicalJSON(rf.World)
	rf.Checksum = recordChecksum(rf.ID, rf.Parent, rf.World)
	return rf, nil
}

func (s *Store) writeRecordLocked(rf *recordFile) error {
	data, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.recordPath(rf.ID), data)
}

func (s *Store) readRecordFileLocked(id string) (*recordFile, error) {
	data, err := os.ReadFile(s.recordPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrRecordNotFound, id)
		}
		return nil, err
	}
	var rf recordFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("%w: 记录 %s 解析失败: %v", ErrCorrupt, id, err)
	}
	if rf.ID != id {
		return nil, fmt.Errorf("%w: 记录 %s 标识不匹配", ErrCorrupt, id)
	}
	return &rf, nil
}

// verifyRecord 校验记录的校验和（覆盖 id、parent 与完整世界数据），
// 并在 accepted 非 nil 时校验规则版本。
func verifyRecord(rf *recordFile, accepted map[string]bool) (*WorldData, error) {
	worldJSON := canonicalJSON(rf.World)
	expected := recordChecksum(rf.ID, rf.Parent, worldJSON)
	if rf.Checksum != expected {
		return nil, fmt.Errorf("%w: 记录 %s 校验和不匹配", ErrCorrupt, rf.ID)
	}
	var wd WorldData
	if err := json.Unmarshal(worldJSON, &wd); err != nil {
		return nil, fmt.Errorf("%w: 记录 %s 世界数据解析失败: %v", ErrCorrupt, rf.ID, err)
	}
	if accepted != nil && !accepted[wd.Rules.Version] {
		return nil, fmt.Errorf("%w: 记录 %s 的规则版本 %q 不在可接受集合中",
			ErrVersion, rf.ID, wd.Rules.Version)
	}
	return &wd, nil
}

func worldFromData(wd *WorldData) (*World, error) {
	w, err := New(wd.Seed, wd.Rules, wd.Locations, wd.Characters)
	if err != nil {
		return nil, err
	}
	w.time = wd.Time
	return w, nil
}

func recordChecksum(id, parent string, worldJSON []byte) string {
	h := sha256.New()
	h.Write([]byte(id))
	h.Write([]byte{0})
	h.Write([]byte(parent))
	h.Write([]byte{0})
	h.Write(worldJSON)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// canonicalJSON 返回 JSON 的紧凑规范化形式，使校验和计算不受
// MarshalIndent 缩进差异的影响。
func canonicalJSON(data []byte) []byte {
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return data
	}
	return buf.Bytes()
}

func newRecordID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("rec-%016x-%x", time.Now().UnixNano(), b[:])
}

func makeSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// atomicWrite 通过“同目录临时文件 + fsync + rename + 目录 fsync”写入，
// 保证进程中断后重新打开只能看到旧的完整文件或新的完整文件。
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
