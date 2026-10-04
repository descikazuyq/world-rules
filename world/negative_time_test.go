package world

import (
	"errors"
	"strings"
	"testing"
)

// writeNegativeTimeRecord 直接写入一条“校验和正确、父记录存在、其余世界
// 状态合法，但世界时间片为负”的记录（模拟旧版本缺陷留下的存档），并把
// 槽指针指向它；返回坏记录标识与其父（更早的一份合法记录）标识。
func writeNegativeTimeRecord(t *testing.T, a *Archive, dir, slot string) (badID, goodID RecordID) {
	t.Helper()
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: noLimitRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	good, err := a.Save(slot, w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	// 只把时间片改成负数：内容仍可解析，规则、角色、物品都合法。
	st := w.Snapshot()
	st.Time = -1
	env := &envelope{
		Format: archiveFormatVersion,
		ID:     RecordID("r" + strings.Repeat("ef", 16)),
		Parent: good.ID,
		State:  st,
	}
	env.Checksum = computeChecksum(env)
	if err := writeRecord(dir, env); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}
	if err := a.persistSlotPointerLocked(slot, slotPointer{
		Latest:  env.ID,
		History: []RecordID{env.ID, good.ID},
	}); err != nil {
		t.Fatalf("persistSlotPointerLocked: %v", err)
	}
	return env.ID, good.ID
}

// assertNegativeTimeCorrupt 确认 err 是保留了记录标识、说明时间片为负的
// *CorruptError，而不是版本拒绝或其他错误。
func assertNegativeTimeCorrupt(t *testing.T, err error, badID RecordID) {
	t.Helper()
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("应返回 *CorruptError，得到 %T: %v", err, err)
	}
	if ce.Record != badID {
		t.Fatalf("损坏错误应保留被拒绝的记录标识 %s，得到 %s", badID, ce.Record)
	}
	if !strings.Contains(ce.Reason, "时间片") || !strings.Contains(ce.Reason, "-1") {
		t.Fatalf("损坏错误应说明时间片为负(-1)，得到 %q", ce.Reason)
	}
	var vre *VersionRejectedError
	if errors.As(err, &vre) {
		t.Fatalf("负时间片不应按规则版本拒绝: %v", err)
	}
}

// 校验和匹配但时间片为负的记录：直接读取报损坏（错误保留记录标识并说明
// 时间片为负），恢复读取与预览按保存次序跳过它，分支、确认恢复、升级与
// 迁移都不能把它复制成新记录；规则版本是否在可接受集合内都不改变结论。
func TestReadRejectsNegativeTimeRecord(t *testing.T) {
	a, dir := newTestArchive(t)
	badID, goodID := writeNegativeTimeRecord(t, a, a.Dir(), "slot")
	versions := []string{"v1"}

	// 版本可接受也不能使负时间片合法：直接读取仍按损坏拒绝。
	if _, err := a.Latest("slot", versions); err == nil {
		t.Fatalf("直接读取负时间片记录应失败")
	} else {
		assertNegativeTimeCorrupt(t, err, badID)
	}
	if _, err := a.Record("slot", badID, versions); err == nil {
		t.Fatalf("按标识读取负时间片记录应失败")
	} else {
		assertNegativeTimeCorrupt(t, err, badID)
	}
	// 版本不在可接受集合时也不改变拒绝类型：仍是损坏，而非版本拒绝。
	if _, err := a.Latest("slot", []string{"v9"}); err == nil {
		t.Fatalf("负时间片记录即使版本不被接受也应按损坏失败")
	} else {
		assertNegativeTimeCorrupt(t, err, badID)
	}

	// 时间片为零的更早合法记录仍可按标识正常读取。
	good, err := a.Record("slot", goodID, versions)
	if err != nil {
		t.Fatalf("更早的零时间片合法记录应可读取: %v", err)
	}
	if good.State.Time != 0 {
		t.Fatalf("合法记录时间片应为 0，得到 %d", good.State.Time)
	}

	// 恢复读取按本槽保存次序跳过坏记录，选中最近一份合法记录。
	rec, err := a.RecoverLatest("slot", versions)
	if err != nil {
		t.Fatalf("RecoverLatest 应跳过负时间片记录: %v", err)
	}
	if rec.ID != goodID {
		t.Fatalf("恢复应选中更早的合法记录 %s，得到 %s", goodID, rec.ID)
	}
	// 预览的当前标识仍是槽实际指向的坏标识，来源标识对应选中的好记录。
	preview, err := a.PreviewRecovery("slot", versions)
	if err != nil {
		t.Fatalf("PreviewRecovery 应跳过负时间片记录: %v", err)
	}
	if preview.Current != badID || preview.Source != goodID {
		t.Fatalf("预览当前/来源应为 %s/%s，得到 %s/%s",
			badID, goodID, preview.Current, preview.Source)
	}
	if preview.State.Time != 0 {
		t.Fatalf("预览返回状态应来自合法记录（时间片 0），得到 %d", preview.State.Time)
	}

	// 分支不能把负时间片来源复制成新记录，也不创建目标槽。
	if _, err := a.Branch("slot", badID, "dst"); err == nil {
		t.Fatalf("以负时间片记录为来源的分支应被拒绝")
	} else {
		assertNegativeTimeCorrupt(t, err, badID)
	}
	if _, err := a.readLatestLocked("dst"); err == nil {
		t.Fatalf("被拒绝的分支不应创建目标槽")
	}

	// 确认恢复不能以负时间片记录为来源，即使它是槽当前指向。
	if _, err := a.ConfirmRecovery("slot", badID, badID, versions); err == nil {
		t.Fatalf("以负时间片记录为来源的确认恢复应被拒绝")
	} else {
		assertNegativeTimeCorrupt(t, err, badID)
	}

	// 规则升级与显式迁移也不能承接负时间片来源。
	target := noLimitRules()
	target.Version = "v2"
	if _, err := a.CheckUpgrade("slot", versions, target); err == nil {
		t.Fatalf("对负时间片记录的升级检查应失败")
	} else {
		assertNegativeTimeCorrupt(t, err, badID)
	}
	if _, err := a.Upgrade("slot", versions, target, badID); err == nil {
		t.Fatalf("对负时间片记录的升级应失败")
	} else {
		assertNegativeTimeCorrupt(t, err, badID)
	}
	if _, err := a.PreviewMigration("slot", versions, target, nil, nil); err == nil {
		t.Fatalf("对负时间片记录的迁移预览应失败")
	} else {
		assertNegativeTimeCorrupt(t, err, badID)
	}
	if _, err := a.Migrate("slot", versions, target, nil, nil, badID); err == nil {
		t.Fatalf("对负时间片记录的迁移应失败")
	} else {
		assertNegativeTimeCorrupt(t, err, badID)
	}

	// 全部拒绝后槽当前记录与历史保持不变。
	latest, err := a.readLatestLocked("slot")
	if err != nil {
		t.Fatalf("readLatestLocked: %v", err)
	}
	if latest != badID {
		t.Fatalf("槽当前记录不应改变，得到 %s", latest)
	}
	history, err := a.History("slot")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 2 || history[0].ID != badID || history[1].ID != goodID {
		t.Fatalf("历史不应改变: %+v", history)
	}

	// 普通读取与恢复预览只读：坏记录文件既没被修补（时间片仍为 -1），
	// 校验和也仍是原样，没有被悄悄重写成零时间片的新记录。
	env, err := loadRecord(recordPath(dir, badID))
	if err != nil {
		t.Fatalf("loadRecord: %v", err)
	}
	if env.State.Time != -1 {
		t.Fatalf("损坏记录不应被悄悄修补，时间片得到 %d", env.State.Time)
	}
	if computeChecksum(env) != env.Checksum {
		t.Fatalf("损坏记录文件不应被只读操作重写")
	}
}

// 槽中只有负时间片记录时，恢复读取与预览返回 ErrUnrecoverable。
func TestRecoverUnrecoverableWhenOnlyNegativeTimeRecord(t *testing.T) {
	a, dir := newTestArchive(t)
	w, err := NewWorld(InitialData{
		Seed:  1,
		Rules: noLimitRules(),
		Characters: []Character{
			{ID: "hero", Location: "hall", Items: []CharacterItem{{Item: "gold", Count: 1}}},
		},
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	st := w.Snapshot()
	st.Time = -3
	env := &envelope{
		Format:    archiveFormatVersion,
		ID:        RecordID("r" + strings.Repeat("ab", 16)),
		SlotFirst: true,
		State:     st,
	}
	env.Checksum = computeChecksum(env)
	if err := writeRecord(dir, env); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}
	if err := a.createSlotPointer("slot", env.ID); err != nil {
		t.Fatalf("createSlotPointer: %v", err)
	}
	if _, err := a.RecoverLatest("slot", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("没有可用记录应返回 ErrUnrecoverable，得到 %v", err)
	}
	if _, err := a.PreviewRecovery("slot", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("没有可用记录时预览应返回 ErrUnrecoverable，得到 %v", err)
	}
}

// 旧格式裸指针（无历史索引）指向负时间片记录时，历史重建在该校验不过的
// 记录处停止：不信任其中的父标识、不沿父链继续到更早的好记录，也不跨入
// 别的槽——沿用损坏记录既有的历史归属限制。
func TestRecoverNegativeTimeStopsLegacyPointerRebuild(t *testing.T) {
	a, _ := newTestArchive(t)
	badID, goodID := writeNegativeTimeRecord(t, a, a.Dir(), "slot")

	// 换成旧版本写出的裸指针：只有 Latest，没有 History。
	if err := a.replaceSlotPointer("slot", badID); err != nil {
		t.Fatalf("replaceSlotPointer: %v", err)
	}
	if _, err := a.RecoverLatest("slot", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("旧格式指针在负时间片记录处应停止重建并返回 ErrUnrecoverable，得到 %v", err)
	}
	if _, err := a.PreviewRecovery("slot", []string{"v1"}); !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("旧格式指针预览同样应返回 ErrUnrecoverable，得到 %v", err)
	}
	// 重建在坏记录处停止后，不信任其中的父标识：更早的好记录不再能经
	// 本槽历史确认归属（沿用损坏链头既有的归属限制，不沿坏记录回退）。
	if _, err := a.Record("slot", goodID, []string{"v1"}); err == nil {
		t.Fatalf("旧格式指针在坏记录处停止后，更早记录不应再经本槽历史可读")
	}
	// 以那份更早记录为来源的分支同样不被允许。
	if _, err := a.Branch("slot", goodID, "legacybranch"); err == nil {
		t.Fatalf("旧格式指针停止重建后不应接受无法确认归属的分支来源")
	}
}

// 负时间片记录作为父记录不妨碍读取它更早的合法父记录：单条状态有效性
// 判断不要求父子时间片递增，此处只确认合法记录不被父/子关系牵连。
func TestNegativeTimeRecordDoesNotInvalidateSibling(t *testing.T) {
	a, _ := newTestArchive(t)
	badID, goodID := writeNegativeTimeRecord(t, a, a.Dir(), "slot")

	// 以更早的合法记录为来源分支仍然允许，分支不受坏记录影响。
	info, err := a.Branch("slot", goodID, "goodbranch")
	if err != nil {
		t.Fatalf("以合法记录为来源的分支应成功: %v", err)
	}
	if info.Parent != goodID {
		t.Fatalf("分支首记录应以合法记录为父，得到 %s", info.Parent)
	}
	if rec, err := a.Latest("goodbranch", []string{"v1"}); err != nil {
		t.Fatalf("分支槽应可读取: %v", err)
	} else if rec.State.Time != 0 {
		t.Fatalf("分支记录时间片应为 0，得到 %d", rec.State.Time)
	}

	// 原槽指向仍是坏记录，且仍按损坏拒绝。
	if _, err := a.Latest("slot", []string{"v1"}); err == nil {
		t.Fatalf("原槽最新记录仍为负时间片记录，应继续拒绝")
	} else {
		assertNegativeTimeCorrupt(t, err, badID)
	}
}
