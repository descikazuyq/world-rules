package world_test

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/descikazuyq/world-rules/world"
)

// printMigrationItems 按物品列表次序打印一行角色物品，便于看到合并条目
// 保留在原列表中的位置。
func printMigrationItems(tag string, c world.Character) {
	fmt.Printf("%s: %s@%s", tag, c.ID, c.Location)
	for _, it := range c.Items {
		fmt.Printf(" %s=%d", it.Item, it.Count)
	}
	fmt.Println()
}

// Example_mergeItemKinds 演示把旧存档中的两种物品显式合并为新种类：
//
//	v1 存档中 hero 依次持有旧木材 2、石头 1、旧树枝 3（时间片非零）；
//	v2 删除两种旧物品、允许木料与石头，把旧木材与旧树枝都对应到木料，
//	石头不指定对应关系而保持原样。先把目标上限设为 5：预览正常返回、
//	给出总量 6 超过上限 5 的阻碍且不可提交，Migrate 同样被规则错误拒绝，
//	槽当前记录与历史保持原样。再把目标上限放宽到 6，重新预览并用本次
//	预览返回的来源记录标识正式迁移：新记录版本为 v2、木料 5 在前、石头
//	1 在后，种子、时间片与角色位置不变；新记录拥有独立标识并以迁移前
//	记录为父，旧记录仍能按 v1 读取。修改预览返回的数量不会成为保存内容。
func Example_mergeItemKinds() {
	const slot = "camp"
	acceptedV1 := []string{"v1"}
	acceptedV2 := []string{"v2"}

	// v1 旧规则：地点 yard，三种物品——旧木材 old_wood、石头 stone、
	// 旧树枝 old_branch，hero 携带总量上限 10。
	rulesV1 := world.Rules{
		Version:     "v1",
		Locations:   []string{"yard"},
		ItemKinds:   []string{"old_wood", "stone", "old_branch"},
		CarryLimits: map[string]int{"hero": 10},
	}

	dir, err := os.MkdirTemp("", "world-migrate-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// 1) 建立世界并保存为命名槽：hero 依次持有旧木材 2、石头 1、旧树枝 3，
	//    旧规则允许这些物品与数量（总量 6 <= 10）。
	w0, err := world.NewWorld(world.InitialData{
		Seed:  20240807,
		Rules: rulesV1,
		Characters: []world.Character{{
			ID:       "hero",
			Location: "yard",
			Items: []world.CharacterItem{
				{Item: "old_wood", Count: 2},
				{Item: "stone", Count: 1},
				{Item: "old_branch", Count: 3},
			},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	// 推进到非零时间片 4（只推进时间，物品与位置不变）。
	if _, err := w0.Apply(world.Commit{Time: 4}); err != nil {
		log.Fatal(err)
	}
	arch0, err := world.Create(dir)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := arch0.Save(slot, w0); err != nil {
		log.Fatal(err)
	}

	// 另一次会话：重新打开存档，读出待迁移的 v1 记录。
	arch, err := world.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	src, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}

	// v2 目标规则：删除两种旧物品，只允许木料 lumber 与石头 stone；先把
	// hero 上限设为 5——转换后木料 5 + 石头 1 = 6，会超出上限。
	rulesV2Limit5 := world.Rules{
		Version:     "v2",
		Locations:   []string{"yard"},
		ItemKinds:   []string{"lumber", "stone"},
		CarryLimits: map[string]int{"hero": 5},
	}
	// 对应关系：旧木材、旧树枝都对应到木料；石头不指定，保持原样。
	itemMaps := []world.NameMapping{
		{From: "old_wood", To: "lumber"},
		{From: "old_branch", To: "lumber"},
	}

	// 2) 先预览，不写入。接受来源的 v1；目标版本由目标规则给出（v2）。
	pv, err := arch.PreviewMigration(slot, acceptedV1, rulesV2Limit5, nil, itemMaps)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("预览: 来源版本=%s 目标版本=%s 可提交=%t 来源标识即当前记录=%t\n",
		pv.OldVersion, pv.TargetVersion, pv.Committable, pv.RecordID == src.ID)
	printMigrationItems("预览物品", pv.State.Characters[0])
	// 合并条目保留在原列表中首次出现的位置：old_wood 在索引 0，
	// 故 lumber=5 在最前；未指定的 stone 保持在中间。
	for _, b := range pv.Blockers {
		fmt.Printf("阻碍: 角色=%s 类型=%s 总量=%d 上限=%d\n",
			b.Character, b.Kind, b.Total, b.Limit)
	}
	// 预览不改动存档：原记录仍是 v1，且保留三个条目。
	stored, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("预览后原槽未切换: 仍为 v1=%t 条目数=%d\n",
		stored.State.Rules.Version == "v1", len(stored.State.Characters[0].Items))
	// 预览: 来源版本=v1 目标版本=v2 可提交=false 来源标识即当前记录=true
	// 预览物品: hero@yard lumber=5 stone=1
	// 阻碍: 角色=hero 类型=limit 总量=6 上限=5
	// 预览后原槽未切换: 仍为 v1=true 条目数=3

	// 3) 不可提交时仍尝试正式迁移：排他锁下重新计算，得到规则错误，
	//    结果中附带同样的阻碍；槽当前记录与历史保持原样。
	bad, err := arch.Migrate(slot, acceptedV1, rulesV2Limit5, nil, itemMaps, pv.RecordID)
	var re *world.RuleError
	if !errors.As(err, &re) {
		log.Fatalf("应返回 *world.RuleError，得到 %v", err)
	}
	fmt.Printf("迁移被拒: 错误类型=%T 阻碍数=%d\n", err, len(bad.Preview.Blockers))
	histAfterReject, err := arch.History(slot)
	if err != nil {
		log.Fatal(err)
	}
	curStillV1, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("拒绝后原样: 当前标识未变=%t 历史条数=%d\n",
		curStillV1.ID == src.ID, len(histAfterReject))
	// 迁移被拒: 错误类型=*world.RuleError 阻碍数=1
	// 拒绝后原样: 当前标识未变=true 历史条数=1

	// 4) 把目标上限调整为 6（总量 6 等于上限，允许），重新预览。
	rulesV2 := rulesV2Limit5
	rulesV2.CarryLimits = map[string]int{"hero": 6}
	pv2, err := arch.PreviewMigration(slot, acceptedV1, rulesV2, nil, itemMaps)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("重新预览: 可提交=%t 阻碍数=%d\n", pv2.Committable, len(pv2.Blockers))
	// 即使修改本次预览返回的数量，正式迁移也以锁下重新计算的结果为准，
	// 修改不会成为保存内容。
	pv2.State.Characters[0].Items[0].Count = 999

	// 用本次预览给出的来源记录标识完成正式迁移：目标规则、对应关系仍要
	// 原样传入。
	res, err := arch.Migrate(slot, acceptedV1, rulesV2, nil, itemMaps, pv2.RecordID)
	if err != nil {
		log.Fatal(err)
	}
	// 重新预览: 可提交=true 阻碍数=0

	// 5) 读回新记录时接受 v2：新版本、合并后的物品；种子、时间片与角色
	//    位置保持原样。
	cur, err := arch.Latest(slot, acceptedV2)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("迁移后: 版本=%s 种子=%d 时间片=%d\n",
		cur.State.Rules.Version, cur.State.Seed, cur.State.Time)
	printMigrationItems("迁移后物品", cur.State.Characters[0])
	// 新记录拥有独立标识，以迁移前记录为父。
	fmt.Printf("记录关系: 新标识独立=%t 以迁移前记录为父=%t\n",
		res.Record.ID != pv2.RecordID, res.Record.Parent == pv2.RecordID)
	fmt.Printf("槽当前即新记录=%t 新记录版本=%s\n",
		cur.ID == res.Record.ID, res.Record.Version)
	// 旧记录仍能按旧版本 v1 读取，物品仍是原来的三个条目。
	old, err := arch.Record(slot, pv2.RecordID, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("旧记录可读: 版本=%s 条目数=%d\n",
		old.State.Rules.Version, len(old.State.Characters[0].Items))

	// Output:
	// 预览: 来源版本=v1 目标版本=v2 可提交=false 来源标识即当前记录=true
	// 预览物品: hero@yard lumber=5 stone=1
	// 阻碍: 角色=hero 类型=limit 总量=6 上限=5
	// 预览后原槽未切换: 仍为 v1=true 条目数=3
	// 迁移被拒: 错误类型=*world.RuleError 阻碍数=1
	// 拒绝后原样: 当前标识未变=true 历史条数=1
	// 重新预览: 可提交=true 阻碍数=0
	// 迁移后: 版本=v2 种子=20240807 时间片=4
	// 迁移后物品: hero@yard lumber=5 stone=1
	// 记录关系: 新标识独立=true 以迁移前记录为父=true
	// 槽当前即新记录=true 新记录版本=v2
	// 旧记录可读: 版本=v1 条目数=3
}
