package world

import (
	"errors"
	"os"
	"syscall"
)

// Record 是一次成功保存留下的、可供读取的记录。
type Record struct {
	// ID 是记录标识。
	ID RecordID
	// Parent 是父记录标识；槽首条记录为空。
	Parent RecordID
	// SlotFirst 表示这是其所在槽的首条记录。
	SlotFirst bool
	// State 是记录保存时的完整世界数据（深拷贝，修改不影响存档）。
	State State
}

func acquireSharedLock(dir string) (*fileLock, error) {
	path := dir + "/lock"
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		f.Close()
		return nil, err
	}
	return &fileLock{f: f}, nil
}

// loadAndVerifyLocked 读取记录文件，校验其内容校验和（校验和已包含
// 父记录关系）、父记录是否存在，以及世界数据是否自洽。
func (a *Archive) loadAndVerifyLocked(id RecordID) (*envelope, error) {
	env, err := loadRecord(recordPath(a.dir, id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &NotFoundError{Record: id}
		}
		return nil, &CorruptError{Record: id, Reason: err.Error()}
	}
	if env.ID != id {
		return nil, &CorruptError{Record: id, Reason: "记录标识与文件名不一致"}
	}
	if got := computeChecksum(env); got != env.Checksum {
		return nil, &CorruptError{Record: id, Reason: "内容校验和不匹配（可能已损坏或被改动）"}
	}
	// 父记录关系必须落到一个真实存在的记录上。
	if env.Parent != "" {
		if _, err := os.Stat(recordPath(a.dir, env.Parent)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, &CorruptError{Record: id, Reason: "父记录缺失: " + string(env.Parent)}
			}
			return nil, err
		}
	}
	if err := validateInitialData(InitialData{
		Seed:       env.State.Seed,
		Rules:      env.State.Rules,
		Characters: env.State.Characters,
	}); err != nil {
		return nil, &CorruptError{Record: id, Reason: "记录中的世界数据不合法: " + err.Error()}
	}
	return env, nil
}

func versionAccepted(version string, accepted []string) bool {
	for _, v := range accepted {
		if v == version {
			return true
		}
	}
	return false
}

func envToRecord(env *envelope) Record {
	return Record{
		ID:        env.ID,
		Parent:    env.Parent,
		SlotFirst: env.SlotFirst,
		State:     cloneState(env.State),
	}
}

// Latest 读取槽当前最新记录。
//
// acceptedVersions 是调用方明确给出的可接受规则版本集合：记录完好但
// 版本不在集合内时返回 *VersionRejectedError；记录损坏（校验和不符、
// 父记录缺失、无法解析）时返回 *CorruptError。读取不会改写存档。
func (a *Archive) Latest(slot string, acceptedVersions []string) (Record, error) {
	if !validSlotName(slot) {
		return Record{}, &NotFoundError{Slot: slot}
	}
	lock, err := acquireSharedLock(a.dir)
	if err != nil {
		return Record{}, err
	}
	defer lock.release()

	id, err := a.readLatestLocked(slot)
	if err != nil {
		return Record{}, err
	}
	env, err := a.loadAndVerifyLocked(id)
	if err != nil {
		if ce, ok := err.(*CorruptError); ok {
			ce.Slot = slot
		}
		return Record{}, err
	}
	if !versionAccepted(env.State.Rules.Version, acceptedVersions) {
		return Record{}, &VersionRejectedError{
			Slot:     slot,
			Record:   id,
			Version:  env.State.Rules.Version,
			Accepted: append([]string(nil), acceptedVersions...),
		}
	}
	return envToRecord(env), nil
}

// Record 按标识读取槽的一份历史记录（覆盖后的旧记录仍可读取）。
//
// 与 Latest 一样检查内容校验和（含父记录关系）与规则版本，且记录必须
// 属于该槽的历史链。读取不会改写存档，也不会改变规则版本。
func (a *Archive) Record(slot string, id RecordID, acceptedVersions []string) (Record, error) {
	if !validSlotName(slot) {
		return Record{}, &NotFoundError{Slot: slot}
	}
	lock, err := acquireSharedLock(a.dir)
	if err != nil {
		return Record{}, err
	}
	defer lock.release()

	if err := a.assertInSlotHistoryLocked(slot, id); err != nil {
		return Record{}, err
	}
	env, err := a.loadAndVerifyLocked(id)
	if err != nil {
		if ce, ok := err.(*CorruptError); ok {
			ce.Slot = slot
		}
		return Record{}, err
	}
	if !versionAccepted(env.State.Rules.Version, acceptedVersions) {
		return Record{}, &VersionRejectedError{
			Slot:     slot,
			Record:   id,
			Version:  env.State.Rules.Version,
			Accepted: append([]string(nil), acceptedVersions...),
		}
	}
	return envToRecord(env), nil
}

// History 返回槽的历史记录元信息，最新在前，到该槽首条记录为止
// （不会跨入分支来源槽）。历史次序由槽指针内嵌的历史列表决定。
// 该方法只做只读遍历，不改写任何数据。
func (a *Archive) History(slot string) ([]RecordInfo, error) {
	if !validSlotName(slot) {
		return nil, &NotFoundError{Slot: slot}
	}
	lock, err := acquireSharedLock(a.dir)
	if err != nil {
		return nil, err
	}
	defer lock.release()

	chain, err := a.effectiveHistoryLocked(slot)
	if err != nil {
		return nil, err
	}
	infos := make([]RecordInfo, 0, len(chain))
	for _, id := range chain {
		env, err := loadRecord(recordPath(a.dir, id))
		if err != nil {
			break
		}
		infos = append(infos, RecordInfo{
			ID:        env.ID,
			Parent:    env.Parent,
			SlotFirst: env.SlotFirst,
			Version:   env.State.Rules.Version,
		})
	}
	return infos, nil
}

// RecoverLatest 在最新记录损坏、缺失或版本不被接受时，沿该槽按保存
// 生效次序排列的历史（最新在前）寻找最近一份“内容校验通过且规则版本
// 可接受”的记录并返回。
//
// 历史次序由槽指针内嵌的历史列表决定，不依赖可能损坏的父指针，因此
// 即使最新记录被截断到无法解析、被删除，或父标识/槽首标记被篡改，
// 也能越过它继续查找本槽更早的可用记录，且不会被引向别的槽。分支只
// 查找分支自身已保存的记录，不跨入来源槽。没有任何可接受记录时返回
// 包装了 ErrUnrecoverable 的 *UnrecoverableError，可用 errors.Is(err,
// ErrUnrecoverable) 判断。恢复读取不会改写槽、历史记录或规则。
func (a *Archive) RecoverLatest(slot string, acceptedVersions []string) (Record, error) {
	if !validSlotName(slot) {
		return Record{}, &NotFoundError{Slot: slot}
	}
	lock, err := acquireSharedLock(a.dir)
	if err != nil {
		return Record{}, err
	}
	defer lock.release()

	history, err := a.effectiveHistoryLocked(slot)
	if err != nil {
		return Record{}, err
	}

	var lastReason string
	for _, id := range history {
		env, err := a.loadAndVerifyLocked(id)
		if err != nil {
			if lastReason == "" {
				lastReason = err.Error()
			}
			continue
		}
		if !versionAccepted(env.State.Rules.Version, acceptedVersions) {
			if lastReason == "" {
				lastReason = (&VersionRejectedError{
					Record:  id,
					Version: env.State.Rules.Version,
				}).Error()
			}
			continue
		}
		return envToRecord(env), nil
	}

	if lastReason == "" {
		lastReason = "空槽或没有任何历史记录"
	}
	return Record{}, &UnrecoverableError{
		Slot:   slot,
		Reason: lastReason,
	}
}
