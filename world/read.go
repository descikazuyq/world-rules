package world

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Record 是一次成功保存留下的、可供读取的记录。
type Record struct {
	// ID 是记录标识。
	ID RecordID
	// Parent 是父记录标识。只有直接建立的槽首记录没有父记录（为空）；
	// 分支槽的首条记录以来源槽记录为父，因此“槽首记录”并不等于
	// “没有父记录”。
	Parent RecordID
	// SlotFirst 表示这是其所在槽的首条记录（直接建立的根记录，或从
	// 其他槽分出的分支首记录）；它不代表 Parent 一定为空。
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

// loadAndVerifyLocked 读取记录文件，校验其记录格式版本、内容校验和（校验和
// 已包含父记录关系）、父记录是否存在，以及世界数据是否自洽。
//
// 记录格式只支持 recordFormatVersion：格式编号缺失、为零、为负或为其他
// 正整数的记录按损坏处理，即使校验和匹配、世界状态合法、规则版本可被
// 调用方接受也不交付——目录能打开不代表每条记录都是当前功能理解的格式。
// 格式校验先于内容校验和：校验和载荷的布局本身由格式定义，未知格式的
// 记录无法解释其校验语义。格式问题与规则版本分别判断，格式错误一律报
// CorruptError，绝不报成 VersionRejectedError。
//
// 对完整世界状态的认可与 WorldFromState 重建世界保持一致：时间片为负的
// 记录属于损坏记录——内容校验和匹配只说明内容完整，规则版本被接受也不
// 能使负时间片合法；这样的记录不能被读回，也不能作为恢复或分支的来源。
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
	if env.Format != recordFormatVersion {
		return nil, &CorruptError{Record: id, Reason: fmt.Sprintf("不支持的记录格式版本: %d", env.Format)}
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
	// 时间片为负的记录与 WorldFromState 的拒绝保持一致：即使校验和匹配、
	// 其余状态合法，也按损坏处理，不自动把时间改成零。
	if env.State.Time < 0 {
		return nil, &CorruptError{Record: id, Reason: fmt.Sprintf("记录中的时间片为负: %d", env.State.Time)}
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
// 版本不在集合内时返回 *VersionRejectedError；记录损坏（格式版本不受支持、
// 校验和不符、父记录缺失、无法解析、时间片为负）时返回 *CorruptError。
// 格式与版本分别判断：格式不受支持的记录一律报损坏，不报版本拒绝。
// 读取不会改写存档。
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

// Record 按标识读取槽的一份历史记录（覆盖、升级或确认恢复之后，较早的
// 旧记录仍可按其标识读取）。
//
// 与 Latest 一样检查记录格式版本、内容校验和（含父记录关系）与规则版本，
// 且记录必须属于该槽的历史链。acceptedVersions 必须显式给出调用方可以
// 接受的规则版本：记录完好但版本不在集合内时返回 *VersionRejectedError。
// 读取不会切换槽当前记录，不会修补记录，也不会替换记录中的规则版本。
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

// History 浏览槽已经保存生效的历史，返回每条记录的元信息（记录标识、
// 父标识、槽首标记、规则版本），供先浏览、再用 [Archive.Record] 按标识
// 读取完整世界。
//
// 次序按该槽“保存生效的先后”排列：最近一次成功保存的记录在最前，槽首
// 记录在末尾。它既不按记录里的世界时间片排序，也不按记录文件的修改时间
// 排序——确认恢复允许时间片回到较早来源的时刻，那次保存产生的新记录仍排
// 在最前，而原先已经生效的其他保存按原次序保留。
//
// 父标识表示“这条记录由哪一次保存承接而来”，与列表次序是两回事。槽首
// 标记不能统一解释成“父标识为空”：只有直接建立的槽首记录没有父记录；
// 分支槽的首条记录虽然标记为槽首，却以来源槽的那条记录为父记录。分支槽
// 只列分支自身保存生效的记录，不会因为分支首记录的父指向来源槽，就把
// 来源槽的历史补进列表。
//
// 列表只做完整性检查，不筛选规则版本：History 没有可接受版本参数，一份
// 记录只要确实保存生效且通过与 Record 一致的完整性检查——标识与文件一致、
// 记录格式受支持、内容校验和正确（含父记录关系）、世界状态合法、直接父
// 记录文件存在——即使规则版本较旧或陌生也照常列出。因此“出现在列表里”
// 只说明记录完好，不保证调用方立即可用：用 Record 读取时仍必须显式给出
// 可接受版本集合，版本不被接受会得到 *VersionRejectedError，不能据此认为
// 历史列错了。
//
// 带历史索引的槽中，单条损坏、缺失或无法解析的记录只被略过，列表继续
// 返回其余合格记录，最新或某条中间记录出问题不会让整个浏览失败；但直接
// 父记录文件被删除的子记录因不满足父关系要求也被略过，而父文件仍在、
// 只是内容损坏时，不因此略过自身完好的子记录。旧版本写出的无历史索引槽
// 沿校验通过的父链在内存中重建，只列仍能确认归属本槽的记录，重建在损坏
// 处停止，不能承诺越过损坏处继续寻找更老的记录。
//
// 槽存在但没有任何合格记录时，返回成功的非 nil 空列表；槽不存在返回
// *NotFoundError，槽指针无法解析返回 *CorruptError，三者互不等同，不能把
// 不存在或损坏当成空列表。浏览只做只读遍历，不切换槽当前记录、不修补或
// 删除记录、不改写历史与世界规则。
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
		// 与 Record 相同的完整性检查（不做规则版本取舍）：任何一条
		// 记录损坏、缺失或无法解析都只略过它本身，不读其中的父标识或
		// 槽首标记去扩大或缩小遍历范围，也不让整个列表失败。
		env, verifyErr := a.loadAndVerifyLocked(id)
		if verifyErr != nil {
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
// 找到本槽更早的可用记录，而不会被引向别的槽或遮住更早的可用记录。遍历
// 次序与世界时间片的大小无关；时间片为负的记录属于损坏记录，同样被越过。
// 格式版本不受支持的记录（即使校验和匹配、状态合法）同样被越过，连续
// 多份未知格式记录也不会遮住其后格式受支持的完好记录。
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

	env, err := a.firstRecoverableLocked(history, acceptedVersions)
	if err != nil {
		var ue *UnrecoverableError
		if errors.As(err, &ue) {
			ue.Slot = slot
		}
		return Record{}, err
	}
	return envToRecord(env), nil
}

// firstRecoverableLocked 在调用方已持锁的前提下，按给定的保存生效次序
// （最新在前）返回第一份“内容与父关系校验通过且规则版本可接受”的记录。
//
// 文件缺失、截断、格式版本不受支持、校验和不符、记录标识不符、父关系异常、
// 时间片为负或世界状态不自洽的记录一律越过（不读其中的父标识或槽首标记）；
// 版本不被接受的记录也越过且不自动升级。次序完全由调用方给出（来自槽指针
// 历史索引或沿可信父链的重建），因此不会被引向别的槽。没有任何可用记录时
// 返回包装了 ErrUnrecoverable 的 *UnrecoverableError（Slot 字段由调用方
// 补全）。
func (a *Archive) firstRecoverableLocked(history []RecordID, acceptedVersions []string) (*envelope, error) {
	var lastReason string
	for _, id := range history {
		env, verifyErr := a.loadAndVerifyLocked(id)
		if verifyErr != nil {
			lastReason = verifyErr.Error()
			continue
		}
		if !versionAccepted(env.State.Rules.Version, acceptedVersions) {
			lastReason = (&VersionRejectedError{
				Record:  id,
				Version: env.State.Rules.Version,
			}).Error()
			continue
		}
		return env, nil
	}

	if lastReason == "" {
		lastReason = "槽中没有任何可确认归属的历史记录"
	}
	return nil, &UnrecoverableError{Reason: lastReason}
}
