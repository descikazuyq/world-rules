package world

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// RecordInfo 描述一次保存产生的记录的元信息。
type RecordInfo struct {
	// ID 是记录在存档目录内的唯一标识。
	ID RecordID
	// Parent 是父记录标识。只有直接建立的槽首条记录没有父记录（为空）；
	// 分支槽首条记录以来源槽记录为父，故槽首记录的 Parent 不一定为空。
	Parent RecordID
	// SlotFirst 表示这是所在槽的首条记录（直接建立的根记录，或从其他
	// 槽分出的分支首记录）；它不等价于 Parent 为空。
	SlotFirst bool
	// Version 是记录中世界规则的版本；History 列出它不代表该版本已被
	// 调用方接受，用 Record 读取时仍需显式给出可接受版本集合。
	Version string
}

// slotPointer 是槽指针文件的磁盘内容，指向该槽当前最新记录。
//
// History 按该槽“保存生效的次序”记录全部属于本槽的记录标识，最新在前，
// 末尾是槽首条记录（直接建立的根记录或分支首记录）。它只在对应记录文件
// 已完整落盘后才随指针原子更新，因此绝不会包含写了一半的残留文件、写完
// 但尚未完成保存的孤儿记录，或 CAS 竞争失败的写入。恢复读取据此遍历，
// 不依赖文件修改时间，也不信任受损记录文件里读出的父标识或槽首标记。
//
// 旧版本写出的指针没有 History 字段；读取时会在内存中按校验通过的父链
// 回退重建，不要求用户转换数据。
type slotPointer struct {
	Latest  RecordID   `json:"latest"`
	History []RecordID `json:"history,omitempty"`
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
//
// 创建有明确的结果：要么完整成功并返回可立即使用的句柄，要么返回
// 错误且不返回句柄。准备必要目录、写入存档标记或建立锁文件时出错，
// 都会把指明失败操作与位置的错误交给调用方，并撤回本次创建已产生的
// 标记、锁文件与子目录——回退只限本次新增的内容：目录里原有的文件与
// 子目录（包括原本就存在、可直接使用的空 records/slots 子目录）以及
// 调用前已存在的目标目录本身都保持原样。因此失败后排除障碍即可用
// 同一路径重新创建，不会被上一次失败留下的标记拒绝。
func Create(dir string) (_ *Archive, err error) {
	// 本次调用新产生的内容，失败时按相反顺序撤回；调用前已存在的
	// 任何内容都不在撤回范围内。
	var created []string
	defer func() {
		if err != nil {
			for i := len(created) - 1; i >= 0; i-- {
				os.Remove(created[i])
			}
		}
	}()

	// 目标目录在调用前已存在时保留它（包括失败回退时）；不存在才
	// 由本次创建建立，并纳入回退范围。
	if _, statErr := os.Stat(dir); statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("检查存档目录 %s: %w", dir, statErr)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("建立存档目录 %s: %w", dir, err)
		}
		created = append(created, dir)
	}

	markerPath := filepath.Join(dir, markerName)
	f, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// 已有存档：原样保留既有标记（即使它无法解析），不覆盖，
			// 也不借回退删除；此时本次尚未产生任何新内容。
			return nil, &ConflictError{Reason: "目录已是一个存档: " + dir}
		}
		return nil, fmt.Errorf("写入存档标记 %s: %w", markerPath, err)
	}
	created = append(created, markerPath)
	markerData, _ := json.Marshal(archiveMarker{Format: archiveFormatVersion})
	if _, err := f.Write(markerData); err != nil {
		f.Close()
		return nil, fmt.Errorf("写入存档标记 %s: %w", markerPath, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("写入存档标记 %s: %w", markerPath, err)
	}

	for _, sub := range []string{recordsName, slotsName} {
		subPath := filepath.Join(dir, sub)
		if fi, statErr := os.Stat(subPath); statErr == nil {
			if !fi.IsDir() {
				return nil, fmt.Errorf("建立存档子目录 %s: 位置已被同名文件占用", subPath)
			}
			// 原本就存在的子目录直接沿用，回退时保留。
			continue
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("检查存档子目录 %s: %w", subPath, statErr)
		}
		if err := os.Mkdir(subPath, 0o755); err != nil {
			return nil, fmt.Errorf("建立存档子目录 %s: %w", subPath, err)
		}
		created = append(created, subPath)
	}

	// 预先建立锁文件并同步目录，使之后的打开更稳妥。锁位置已被占用
	// （例如已是一个目录）时明确报错，而不是留到首次保存才失败。
	lockPath := filepath.Join(dir, "lock")
	_, lockStatErr := os.Stat(lockPath)
	lockExisted := lockStatErr == nil
	if lockStatErr != nil && !errors.Is(lockStatErr, os.ErrNotExist) {
		return nil, fmt.Errorf("检查存档锁文件 %s: %w", lockPath, lockStatErr)
	}
	lf, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("建立存档锁文件 %s: %w", lockPath, err)
	}
	if err := lf.Close(); err != nil {
		return nil, fmt.Errorf("建立存档锁文件 %s: %w", lockPath, err)
	}
	if !lockExisted {
		created = append(created, lockPath)
	}

	if err := syncDir(dir); err != nil {
		return nil, fmt.Errorf("同步存档目录 %s: %w", dir, err)
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

// readSlotPointerLocked 在调用方已持锁的前提下读取并解析槽指针文件。
func (a *Archive) readSlotPointerLocked(slot string) (slotPointer, error) {
	data, err := os.ReadFile(a.slotPath(slot))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return slotPointer{}, &NotFoundError{Slot: slot}
		}
		return slotPointer{}, err
	}
	var p slotPointer
	if err := json.Unmarshal(data, &p); err != nil {
		return slotPointer{}, &CorruptError{Slot: slot, Reason: "槽指针无法解析"}
	}
	if p.Latest == "" {
		return slotPointer{}, &CorruptError{Slot: slot, Reason: "槽指针为空"}
	}
	return p, nil
}

// readLatestLocked 在调用方已持锁的前提下读取槽当前最新记录标识。
func (a *Archive) readLatestLocked(slot string) (RecordID, error) {
	p, err := a.readSlotPointerLocked(slot)
	if err != nil {
		return "", err
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
		Format:    recordFormatVersion,
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
// 要么没有该槽，要么指向一份完整记录，绝不会留下半写的指针。新槽的
// 历史索引只含其首条记录本身——分支槽不会把来源槽的记录计入候选。
func (a *Archive) createSlotPointer(slot string, id RecordID) error {
	slotsDir := filepath.Join(a.dir, slotsName)
	final := a.slotPath(slot)
	data, _ := json.MarshalIndent(slotPointer{
		Latest:  id,
		History: []RecordID{id},
	}, "", "  ")

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
// 写入才会发生；否则返回 *ConflictError——即使该标识对应的旧文件恰好
// 已损坏或被删除，过期请求也只报冲突，不改报文件错误。标识匹配时还要
// 确认这条当前记录本身可正常读取：文件缺失返回 *NotFoundError；格式版本
// 不受支持、截断、校验和不匹配、世界状态不合法或父记录缺失返回
// *CorruptError。绝不用
// 调用方传入的完整世界掩盖当前记录的问题，也不自动改选较老记录为父。
// 新记录的父记录就是被覆盖的那次记录。同一目录下并发覆盖同一父记录时
// 只有一个成功。被拒绝的覆盖不改变槽指向、历史与任何已有记录。
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

	pointer, err := a.readSlotPointerLocked(slot)
	if err != nil {
		return RecordInfo{}, err
	}
	if pointer.Latest != expected {
		return RecordInfo{}, &ConflictError{
			Slot:   slot,
			Reason: "所依据的记录已不是该槽最新记录",
		}
	}

	// 标识匹配后，当前记录本身必须可正常读取：格式受支持、完整、内容
	// 校验通过、世界状态合法且父记录关系成立。否则以它为父保存的新记录随后也
	// 无法读取，等于用调用方传入的完整世界掩盖了当前记录的问题；
	// 这里沿用与读取路径一致的校验，损坏或缺失时拒绝覆盖，槽指向、
	// 历史与已有记录保持原样，调用方可改走恢复预览与确认恢复。
	if _, err := a.loadAndVerifyLocked(expected); err != nil {
		if ce, ok := err.(*CorruptError); ok {
			ce.Slot = slot
		}
		return RecordInfo{}, err
	}

	// 覆盖保存传入世界的快照，以被覆盖的当前记录为父，沿用共同的
	// 新记录组装、落盘与槽指针提交路径。
	return a.appendSlotRecordLocked(slotSave{
		slot:          slot,
		oldPointer:    pointer,
		parent:        expected,
		historyAnchor: expected,
		state:         st,
	})
}

// commitSlotPointerLocked 在记录文件已完整落盘后，原子更新槽指针：指向
// 新记录，并把历史索引按保存次序前置新记录。历史索引只在此刻（记录已
// 完整生效）才更新，因此残留临时文件与写完但未完成保存的孤儿记录绝不会
// 进入恢复候选。
func (a *Archive) commitSlotPointerLocked(slot string, oldPointer slotPointer, oldLatest, newID RecordID) error {
	history, err := a.nextSlotHistoryLocked(oldPointer, oldLatest, newID)
	if err != nil {
		return err
	}
	return a.persistSlotPointerLocked(slot, slotPointer{Latest: newID, History: history})
}

// replaceSlotPointer 直接把槽指针指向 id，不附带历史索引。它仅用于测试
// 中模拟旧版本或外部写入器留下的指针；生产写入路径一律走
// commitSlotPointerLocked 以维护历史索引。
func (a *Archive) replaceSlotPointer(slot string, id RecordID) error {
	return a.persistSlotPointerLocked(slot, slotPointer{Latest: id})
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
		Format:    recordFormatVersion,
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

// assertInSlotHistoryLocked 确认 id 位于 slot 自身的历史次序中（分支槽
// 的历史只含它自己保存成功的记录，不含来源槽记录）。
func (a *Archive) assertInSlotHistoryLocked(slot string, id RecordID) error {
	history, err := a.slotHistoryLocked(slot)
	if err != nil {
		return err
	}
	for _, cid := range history {
		if cid == id {
			return nil
		}
	}
	return &NotFoundError{Slot: slot, Record: id}
}

// slotHistoryLocked 返回该槽按保存生效次序排列的记录标识（最新在前、
// 槽首记录在末尾）。
//
// 优先使用指针内嵌的历史索引；旧版本指针没有该索引时，在内存中沿
// “校验通过”的记录父链回退重建，绝不在读取路径上改写目录。重建在缺失、
// 无法解析或校验不过的记录处停止——无法确认归属的更老记录不再使用。
// 槽首标记只取自校验通过的信封，因此受损记录里被篡改的父标识或槽首
// 标记既不能把遍历引向别的槽，也不能遮住本槽更早的可用记录。
func (a *Archive) slotHistoryLocked(slot string) ([]RecordID, error) {
	p, err := a.readSlotPointerLocked(slot)
	if err != nil {
		return nil, err
	}
	if len(p.History) > 0 {
		// 以 Latest 为链头做一次去重规范化，容忍索引中的重复项。
		return dedupeRecordIDs(append([]RecordID{p.Latest}, p.History...)), nil
	}
	return a.rebuildHistoryLocked(p.Latest)
}

// rebuildHistoryLocked 从 latest 起沿校验通过的父链在内存中重建历史，
// 只返回仍能确认属于本槽的记录；在缺失、无法解析或校验不过的记录处停止。
func (a *Archive) rebuildHistoryLocked(latest RecordID) ([]RecordID, error) {
	var chain []RecordID
	seen := map[RecordID]bool{}
	cur := latest
	for cur != "" {
		if seen[cur] {
			return nil, &CorruptError{Record: cur, Reason: "历史链出现环"}
		}
		seen[cur] = true
		env, err := a.loadAndVerifyLocked(cur)
		if err != nil {
			// 缺失、无法解析或校验不过：无法继续确认更老记录的归属，
			// 旧历史在此断裂；已确认的部分仍可使用。
			return chain, nil
		}
		chain = append(chain, cur)
		if env.SlotFirst {
			return chain, nil
		}
		cur = env.Parent
	}
	return chain, nil
}

// dedupeRecordIDs 按首次出现次序去重记录标识。
func dedupeRecordIDs(ids []RecordID) []RecordID {
	seen := map[RecordID]bool{}
	out := ids[:0]
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// persistSlotPointerLocked 以原子改名写出槽指针。
func (a *Archive) persistSlotPointerLocked(slot string, p slotPointer) error {
	data, _ := json.MarshalIndent(p, "", "  ")
	return writeAtomic(filepath.Join(a.dir, slotsName), slot+".json", data)
}

// nextSlotHistory 在一次以 oldLatest 为被覆盖/升级记录的成功写入后，
// 生成新指针应持有的历史次序：新记录在最前，其余沿用此前已确认属于
// 本槽的记录。旧指针无索引时在内存中回退重建（不完整也照样承接），
// 从而让旧历史在第一次成功覆盖或升级后获得完整恢复能力。
func (a *Archive) nextSlotHistoryLocked(oldPointer slotPointer, oldLatest, newID RecordID) ([]RecordID, error) {
	base := oldPointer.History
	if len(base) == 0 {
		rebuilt, err := a.rebuildHistoryLocked(oldLatest)
		if err != nil {
			return nil, err
		}
		base = rebuilt
	}
	return dedupeRecordIDs(append([]RecordID{newID}, base...)), nil
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
