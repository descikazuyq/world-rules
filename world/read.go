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

// History 返回槽的历史记录元信息，按该槽保存生效的次序排列（最新在
// 前），到槽首条记录为止；分支槽只含它自己保存成功的记录，不会跨入
// 分支来源槽。次序来自槽指针内嵌的历史索引，与文件修改时间无关。该
// 方法只做只读遍历，不改写任何数据；无法解析的记录会被略过。
func (a *Archive) History(slot string) ([]RecordInfo, error) {
	if !validSlotName(slot) {
		return nil, &NotFoundError{Slot: slot}
	}
	lock, err := acquireSharedLock(a.dir)
	if err != nil {
		return nil, err
	}
	defer lock.release()

	history, err := a.slotHistoryLocked(slot)
	if err != nil {
		return nil, err
	}
	infos := make([]RecordInfo, 0, len(history))
	for _, id := range history {
		env, err := loadRecord(recordPath(a.dir, id))
		if err != nil {
			continue
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

// RecoverLatest 返回该槽“按保存生效次序最近的一份”内容校验通过且规则
// 版本可接受的已保存记录。
//
// 遍历次序完全来自槽指针维护的历史索引（最新在前），既不依据文件修改
// 时间，也不读取受损记录里的父标识或槽首标记：因此最新记录被删除、
// 截断成无法解析的内容，或连同若干中间记录一起损坏时，仍能越过它们
// 找到本槽更早的可用记录，而不会被引向别的槽或遮住更早的可用记录。
// 与世界时间片的大小无关。
//
// 分支槽只在分支自身已成功保存的记录中查找；即使分支首条记录损坏也不
// 越过分支边界，其他槽即使种子和规则相同也不会替代。版本不被接受的
// 记录一律跳过且不自动升级。只有记录文件写完且指针已更新（即保存完整
// 生效）的记录才在候选中：残留临时文件、写完但尚未完成保存的孤儿记录
// 不会被选中。
//
// 槽不存在返回 *NotFoundError；槽指针不可读返回 *CorruptError；槽中没有
// 任何版本可接受的完好记录时返回包装了 ErrUnrecoverable 的
// *UnrecoverableError，可用 errors.Is(err, ErrUnrecoverable) 判断。
// 恢复读取不会改写槽、指针、历史记录或规则，重复读取也不消耗历史。
func (a *Archive) RecoverLatest(slot string, acceptedVersions []string) (Record, error) {
	if !validSlotName(slot) {
		return Record{}, &NotFoundError{Slot: slot}
	}
	lock, err := acquireSharedLock(a.dir)
	if err != nil {
		return Record{}, err
	}
	defer lock.release()

	history, err := a.slotHistoryLocked(slot)
	if err != nil {
		return Record{}, err
	}

	var lastReason string
	for _, id := range history {
		env, verifyErr := a.loadAndVerifyLocked(id)
		if verifyErr != nil {
			// 文件缺失、无法解析、校验和不符、记录标识不符、父记录关系
			// 异常或世界状态不自洽：受损记录不能成为恢复结果，越过它
			// 继续看更早的记录（不读其中的父标识或槽首标记）。
			lastReason = verifyErr.Error()
			continue
		}
		if !versionAccepted(env.State.Rules.Version, acceptedVersions) {
			// 版本不被接受：跳过，不自动升级。
			lastReason = (&VersionRejectedError{
				Record:  id,
				Version: env.State.Rules.Version,
			}).Error()
			continue
		}
		return envToRecord(env), nil
	}

	if lastReason == "" {
		lastReason = "槽中没有任何可确认归属的历史记录"
	}
	return Record{}, &UnrecoverableError{Slot: slot, Reason: lastReason}
}
