package world

// slotSave 描述“在一个已存在的槽中追加一条新记录”所需的、与具体操作
// 无关的要素。普通覆盖、规则升级、显式迁移与确认恢复在通过各自的前提
// 检查后，统一用它来完成新记录的组装、落盘与槽指针提交；各操作特有的
// 状态处理（快照、换规则原样保留、转换结果、整份复制）与提交条件仍由
// 调用方在持锁前提下先行确定。
type slotSave struct {
	// slot 是新记录所属的槽名。
	slot string
	// oldPointer 是提交前读到的槽指针；历史索引为空（旧格式裸指针）时
	// 会沿可信父链在内存中重建，承接旧历史。
	oldPointer slotPointer
	// parent 是新记录的父标识：覆盖/升级/迁移为提交所依据的当前（来源）
	// 记录；确认恢复为选中的来源记录（即使它就是当前记录）。
	parent RecordID
	// historyAnchor 是历史承接点：旧指针无内嵌历史索引时，从它开始沿
	// 可信父链重建此前的保存次序。覆盖/升级/迁移即被提交的当前记录；
	// 确认恢复允许当前记录已损坏或被删除，故以槽指针的当前指向为准。
	historyAnchor RecordID
	// state 是新记录保存的完整世界状态，内容含义由各操作决定。
	state State
}

// appendSlotRecordLocked 集中四个追加型保存操作的共同职责，调用方必须已
// 持有排他锁并完成各自的前提校验（槽存在、乐观锁一致、阻碍检查、来源
// 归属与版本校验等）。
//
// 它依次：生成独立的新记录标识；组装信封并计算内容校验和（含父关系）；
// 以临时文件写全 + 原子改名落盘；记录完整生效后才把新记录按保存次序前置
// 进历史并原子更新槽指针。旧历史沿用原保存次序，只做承接不重排——既不按
// 时间片或文件时间排序，也不沿来源的父链截断。崩溃在指针更新前发生时，
// 槽保持原指向与原历史，新记录因未进入历史索引而不会成为恢复候选。
//
// 返回的 RecordInfo 的标识、父记录与规则版本与实际落盘的记录一致；这些
// 追加产生的记录都不是槽首记录。
func (a *Archive) appendSlotRecordLocked(sv slotSave) (RecordInfo, error) {
	id, err := newRecordID()
	if err != nil {
		return RecordInfo{}, err
	}
	env := &envelope{
		Format: recordFormatVersion,
		ID:     id,
		Parent: sv.parent,
		// 追加记录即使复制自槽首来源，也只是本槽保存次序最前的一条
		// 普通记录，不沿用来源的槽首标记。
		SlotFirst: false,
		State:     sv.state,
	}
	env.Checksum = computeChecksum(env)
	if err := writeRecord(a.dir, env); err != nil {
		return RecordInfo{}, err
	}
	if err := a.commitSlotPointerLocked(sv.slot, sv.oldPointer, sv.historyAnchor, id); err != nil {
		return RecordInfo{}, err
	}
	return RecordInfo{
		ID:      id,
		Parent:  sv.parent,
		Version: sv.state.Rules.Version,
	}, nil
}
