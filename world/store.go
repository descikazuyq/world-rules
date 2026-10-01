package world

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// RecordInfo 描述一次保存产生的记录的元信息。
type RecordInfo struct {
	// ID 是记录在存档目录内的唯一标识。
	ID RecordID
	// Parent 是父记录标识；直接建立的槽首条记录为空。
	Parent RecordID
	// SlotFirst 表示这是所在槽的首条记录（根记录或分支记录）。
	SlotFirst bool
	// Version 是记录中世界规则的版本。
	Version string
}

// slotPointer 是槽指针文件的磁盘内容，指向该槽当前最新记录。
type slotPointer struct {
	Latest RecordID `json:"latest"`
}

// archiveMarker 是存档目录的标记文件。
type archiveMarker struct {
	Format int `json:"format"`
}

// Archive 是一个本地存档目录句柄。多个进程或多个实例可以同时打开
// 同一目录；所有写入通过目录内的 flock 串行化。
type Archive struct {
	dir string
}

const (
	markerName  = "archive.json"
	recordsName = "records"
	slotsName   = "slots"
)

// Create 在指定目录建立新的存档。目录会被创建；若该目录已经是一个
// 存档则返回错误，以免覆盖已有数据。
func Create(dir string) (*Archive, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	markerPath := filepath.Join(dir, markerName)
	f, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, &ConflictError{Reason: "目录已是一个存档: " + dir}
		}
		return nil, err
	}
	markerData, _ := json.Marshal(archiveMarker{Format: archiveFormatVersion})
	if _, err := f.Write(markerData); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	for _, sub := range []string{recordsName, slotsName} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	// 预先建立锁文件并同步目录，使之后的打开更稳妥。
	if lf, err := os.OpenFile(filepath.Join(dir, "lock"),
		os.O_RDWR|os.O_CREATE, 0o600); err == nil {
		lf.Close()
	}
	if err := syncDir(dir); err != nil {
		return nil, err
	}
	return &Archive{dir: dir}, nil
}

// Open 打开一个此前由 Create 建立的存档目录。
func Open(dir string) (*Archive, error) {
	path := filepath.Join(dir, markerName)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &NotFoundError{}
		}
		return nil, err
	}
	defer f.Close()
	var m archiveMarker
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return nil, &CorruptError{Reason: "存档标记无法解析"}
	}
	if m.Format != archiveFormatVersion {
		return nil, &CorruptError{Reason: "不支持的存档格式版本"}
	}
	for _, sub := range []string{recordsName, slotsName} {
		if fi, err := os.Stat(filepath.Join(dir, sub)); err != nil || !fi.IsDir() {
			return nil, &CorruptError{Reason: "存档目录结构不完整: 缺少 " + sub}
		}
	}
	return &Archive{dir: dir}, nil
}

// Dir 返回存档目录路径。
func (a *Archive) Dir() string { return a.dir }

func (a *Archive) slotPath(slot string) string {
	return filepath.Join(a.dir, slotsName, slot+".json")
}

// validSlotName 拒绝空名以及可能逃逸 slots 目录的名字。
func validSlotName(slot string) bool {
	if slot == "" || slot == "." || slot == ".." {
		return false
	}
	if filepath.IsAbs(slot) {
		return false
	}
	for _, r := range slot {
		if r == '/' || r == '\\' || r == 0 {
			return false
		}
	}
	return filepath.Clean(slot) == slot
}

// readLatestLocked 在调用方已持锁的前提下读取槽当前最新记录标识。
func (a *Archive) readLatestLocked(slot string) (RecordID, error) {
	data, err := os.ReadFile(a.slotPath(slot))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &NotFoundError{Slot: slot}
		}
		return "", err
	}
	var p slotPointer
	if err := json.Unmarshal(data, &p); err != nil {
		return "", &CorruptError{Slot: slot, Reason: "槽指针无法解析"}
	}
	if p.Latest == "" {
		return "", &CorruptError{Slot: slot, Reason: "槽指针为空"}
	}
	return p.Latest, nil
}

// Save 在存档中保存一个世界。
//
// 首次保存会创建同名存档槽；槽名已存在时返回 *ConflictError。
// 成功后返回新记录的标识，记录包含完整世界数据，且没有父记录。
func (a *Archive) Save(slot string, w *World) (RecordInfo, error) {
	if !validSlotName(slot) {
		return RecordInfo{}, &ConflictError{Slot: slot, Reason: "非法存档槽名"}
	}
	st := w.Snapshot()

	lock, err := acquireLock(a.dir)
	if err != nil {
		return RecordInfo{}, err
	}
	defer lock.release()

	// 持锁后再次确认槽不存在。
	if _, err := a.readLatestLocked(slot); err == nil {
		return RecordInfo{}, &ConflictError{Slot: slot, Reason: "存档槽名已存在"}
	} else if !errors.As(err, new(*NotFoundError)) {
		return RecordInfo{}, err
	}

	id, err := newRecordID()
	if err != nil {
		return RecordInfo{}, err
	}
	env := &envelope{
		Format:    archiveFormatVersion,
		ID:        id,
		SlotFirst: true,
		State:     st,
	}
	env.Checksum = computeChecksum(env)
	if err := writeRecord(a.dir, env); err != nil {
		return RecordInfo{}, err
	}
	if err := a.createSlotPointer(slot, id); err != nil {
		return RecordInfo{}, err
	}
	return RecordInfo{ID: id, SlotFirst: true, Version: st.Rules.Version}, nil
}

// createSlotPointer 以“临时文件写全 + os.Link 原子建立”的方式创建槽
// 指针：Link 在目标已存在时失败，因此重名会被明确拒绝，同时崩溃后
// 要么没有该槽，要么指向一份完整记录，绝不会留下半写的指针。
func (a *Archive) createSlotPointer(slot string, id RecordID) error {
	slotsDir := filepath.Join(a.dir, slotsName)
	final := a.slotPath(slot)
	data, _ := json.MarshalIndent(slotPointer{Latest: id}, "", "  ")

	tmp := filepath.Join(slotsDir, ".new-"+slot+"-"+randomSuffix()+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Link(tmp, final); err != nil {
		os.Remove(tmp)
		if errors.Is(err, os.ErrExist) {
			return &ConflictError{Slot: slot, Reason: "存档槽名已存在"}
		}
		return err
	}
	os.Remove(tmp)
	return syncDir(slotsDir)
}

// Replace 用世界当前状态覆盖槽的最新记录。
//
// expected 必须是调用方此前读到的记录标识，并且它仍是该槽最新记录，
// 写入才会发生；否则返回 *ConflictError。新记录的父记录就是被覆盖的
// 那次记录。同一目录下并发覆盖同一父记录时只有一个成功。
func (a *Archive) Replace(slot string, w *World, expected RecordID) (RecordInfo, error) {
	if !validSlotName(slot) {
		return RecordInfo{}, &ConflictError{Slot: slot, Reason: "非法存档槽名"}
	}
	if expected == "" {
		return RecordInfo{}, &ConflictError{Slot: slot, Reason: "覆盖必须提供所依据的记录标识"}
	}
	st := w.Snapshot()

	lock, err := acquireLock(a.dir)
	if err != nil {
		return RecordInfo{}, err
	}
	defer lock.release()

	latest, err := a.readLatestLocked(slot)
	if err != nil {
		return RecordInfo{}, err
	}
	if latest != expected {
		return RecordInfo{}, &ConflictError{
			Slot:   slot,
			Reason: "所依据的记录已不是该槽最新记录",
		}
	}

	id, err := newRecordID()
	if err != nil {
		return RecordInfo{}, err
	}
	env := &envelope{
		Format: archiveFormatVersion,
		ID:     id,
		Parent: expected,
		State:  st,
	}
	env.Checksum = computeChecksum(env)
	if err := writeRecord(a.dir, env); err != nil {
		return RecordInfo{}, err
	}
	if err := a.replaceSlotPointer(slot, id); err != nil {
		return RecordInfo{}, err
	}
	return RecordInfo{
		ID:      id,
		Parent:  expected,
		Version: st.Rules.Version,
	}, nil
}

func (a *Archive) replaceSlotPointer(slot string, id RecordID) error {
	data, _ := json.MarshalIndent(slotPointer{Latest: id}, "", "  ")
	if err := writeAtomic(filepath.Join(a.dir, slotsName), slot+".json", data); err != nil {
		return err
	}
	return nil
}

// Branch 从 srcSlot 的指定历史记录 src 分出新槽 dstSlot。
//
// 新槽的首条记录以 src 为父记录，完整复制当时的种子、规则和状态。
// 目标槽名已存在时返回 *ConflictError。源记录必须校验通过。
func (a *Archive) Branch(srcSlot string, src RecordID, dstSlot string) (RecordInfo, error) {
	if !validSlotName(srcSlot) {
		return RecordInfo{}, &ConflictError{Slot: srcSlot, Reason: "非法源存档槽名"}
	}
	if !validSlotName(dstSlot) {
		return RecordInfo{}, &ConflictError{Slot: dstSlot, Reason: "非法目标存档槽名"}
	}
	if src == "" {
		return RecordInfo{}, &NotFoundError{Slot: srcSlot}
	}

	lock, err := acquireLock(a.dir)
	if err != nil {
		return RecordInfo{}, err
	}
	defer lock.release()

	srcEnv, err := a.loadAndVerifyLocked(src)
	if err != nil {
		return RecordInfo{}, err
	}
	// 确认源记录确实属于源槽的历史链，避免拿别的槽的记录冒充分支源。
	if err := a.assertInSlotHistoryLocked(srcSlot, src); err != nil {
		return RecordInfo{}, err
	}
	// 目标槽重名拒绝创建。
	if _, err := a.readLatestLocked(dstSlot); err == nil {
		return RecordInfo{}, &ConflictError{Slot: dstSlot, Reason: "目标存档槽名已存在"}
	} else if !errors.As(err, new(*NotFoundError)) {
		return RecordInfo{}, err
	}

	id, err := newRecordID()
	if err != nil {
		return RecordInfo{}, err
	}
	env := &envelope{
		Format:    archiveFormatVersion,
		ID:        id,
		Parent:    src,
		SlotFirst: true,
		// 完整复制当时的世界数据；此后两槽各自保存互不影响。
		State: cloneState(srcEnv.State),
	}
	env.Checksum = computeChecksum(env)
	if err := writeRecord(a.dir, env); err != nil {
		return RecordInfo{}, err
	}
	if err := a.createSlotPointer(dstSlot, id); err != nil {
		return RecordInfo{}, err
	}
	return RecordInfo{
		ID:        id,
		Parent:    src,
		SlotFirst: true,
		Version:   env.State.Rules.Version,
	}, nil
}

// assertInSlotHistoryLocked 确认 id 位于 slot 的历史链上（不含越界到
// 其他槽的父记录）。
func (a *Archive) assertInSlotHistoryLocked(slot string, id RecordID) error {
	chain, err := a.slotChainLocked(slot)
	if err != nil {
		return err
	}
	for _, cid := range chain {
		if cid == id {
			return nil
		}
	}
	return &NotFoundError{Slot: slot, Record: id}
}

// slotChainLocked 返回槽的历史记录标识，最新在前，在该槽首条记录处
// 停止（不跨入分支来源槽）。
func (a *Archive) slotChainLocked(slot string) ([]RecordID, error) {
	latest, err := a.readLatestLocked(slot)
	if err != nil {
		return nil, err
	}
	var chain []RecordID
	seen := map[RecordID]bool{}
	cur := latest
	for cur != "" {
		if seen[cur] {
			return nil, &CorruptError{Slot: slot, Record: cur, Reason: "历史链出现环"}
		}
		seen[cur] = true
		env, err := loadRecord(recordPath(a.dir, cur))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, &CorruptError{Slot: slot, Record: cur, Reason: "历史记录文件缺失"}
			}
			return nil, &CorruptError{Slot: slot, Record: cur, Reason: err.Error()}
		}
		chain = append(chain, cur)
		if env.SlotFirst {
			break
		}
		cur = env.Parent
	}
	return chain, nil
}

// Slots 返回所有存档槽名。
func (a *Archive) Slots() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(a.dir, slotsName))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) > 5 && name[len(name)-5:] == ".json" {
			names = append(names, name[:len(name)-5])
		}
	}
	return names, nil
}
