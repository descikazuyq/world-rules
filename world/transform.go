package world

// sourceForTransformLocked 在调用方已持锁（共享或排他均可）的前提下，执行
// 规则升级与显式迁移共同的“转换前来源判断”：
//
//   - 只读取槽当前指向的最新记录，来源有问题时不改选更早的历史记录；槽不
//     存在返回 *NotFoundError，槽指针无法解析返回 *CorruptError。
//   - 完整性检查（记录格式、标识一致性、内容校验和、直接父记录、世界状态
//     自洽）不通过时返回带槽名与记录标识的 *CorruptError；来源文件缺失
//     返回 *NotFoundError。
//   - 来源完好但规则版本不在调用方给出的集合内时返回 *VersionRejectedError，
//     错误中保存可接受版本集合的副本，调用方事后修改入参不影响已返回的错误。
//   - 完整性问题先于版本取舍报告：损坏记录不会被当成仅仅版本不被接受。
//   - 来源版本可被接受后，目标版本与来源版本按字符串相等判断；相同则返回
//     *RuleError。
//
// 通过全部条件后返回来源记录标识与其校验后的信封；之后升级与迁移再各自
// 计算阻碍。检查、预览与提交共用本函数，因此提交时仍会在锁下重新判断
// 来源，不采信调用方带回的状态。
func (a *Archive) sourceForTransformLocked(slot string, acceptedVersions []string, targetVersion string) (RecordID, *envelope, error) {
	latest, err := a.readLatestLocked(slot)
	if err != nil {
		return "", nil, err
	}
	env, err := a.loadAndVerifyLocked(latest)
	if err != nil {
		if ce, ok := err.(*CorruptError); ok {
			ce.Slot = slot
		}
		return "", nil, err
	}
	if !versionAccepted(env.State.Rules.Version, acceptedVersions) {
		return "", nil, &VersionRejectedError{
			Slot:     slot,
			Record:   latest,
			Version:  env.State.Rules.Version,
			Accepted: append([]string(nil), acceptedVersions...),
		}
	}
	if targetVersion == env.State.Rules.Version {
		return "", nil, &RuleError{Reason: "目标规则版本必须与来源记录版本不同"}
	}
	return latest, env, nil
}
