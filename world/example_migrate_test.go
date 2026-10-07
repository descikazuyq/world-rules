package world_test

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/descikazuyq/world-rules/world"
)

// printMigrateState 以固定格式打印一行世界状态，便于对照迁移前后的变化。
func printMigrateState(tag string, s world.State) {
	c := s.Characters[0]
	fmt.Printf("%s: seed=%d rules=%s time=%d %s@%s", tag, s.Seed, s.Rules.Version, s.Time, c.ID, c.Location)
	for _, it := range c.Items {
		fmt.Printf(" %s=%d", it.Item, it.Count)
	}
	fmt.Println()
}

// Example_mergeItemsByMigration 演示把旧存档中的两种物品合并为一种新
// 种类：准备一个 v1、时间片非零的命名槽（同一角色依次持有旧木材 2 件、
// 石头 1 件、旧树枝 3 件），v2 删除两种旧物品、引入木料，旧木材与旧
// 树枝都对应到木料，石头保持原样。先在携带上限 5 下预览（正常返回但
// 不可提交，总量 6 > 上限 5），正式迁移被规则错误拒绝且槽与历史不变；
// 把上限调整为 6 后重新预览，用此次预览给出的来源记录标识完成迁移，
// 再读回原槽核对新版本、合并后的物品与不变的种子、时间片、角色位置。
func Example_mergeItemsByMigration() {
	const slot = "forge"
	// 迁移时可接受集合只放来源版本 v1；读回迁移后的新记录时只放 v2。
	acceptedV1 := []string{"v1"}
	acceptedV2 := []string{"v2"}

	// v1 旧规则：营地里允许旧木材、旧树枝、石头三种物品，hero 携带上限 6。
	rulesV1 := world.Rules{
		Version:     "v1",
		Locations:   []string{"camp"},
		ItemKinds:   []string{"old_wood", "stick", "stone"},
		CarryLimits: map[string]int{"hero": 6},
	}
	// v2 目标规则：删除两种旧木材，只允许木料与石头；携带上限由参数给出。
	rulesV2 := func(limit int) world.Rules {
		return world.Rules{
			Version:     "v2",
			Locations:   []string{"camp"},
			ItemKinds:   []string{"lumber", "stone"},
			CarryLimits: map[string]int{"hero": limit},
		}
	}
	// 旧木材与旧树枝都对应到木料；石头不出现在对应关系中，保持原样。
	itemMaps := []world.NameMapping{
		{From: "old_wood", To: "lumber"},
		{From: "stick", To: "lumber"},
	}

	dir, err := os.MkdirTemp("", "world-migrate-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// 1) 准备 v1 存档：同一角色的物品依次为旧木材 2、石头 1、旧树枝 3，
	//    旧规则允许这些物品与数量（总量 6 等于上限）。
	w0, err := world.NewWorld(world.InitialData{
		Seed:  20240801,
		Rules: rulesV1,
		Characters: []world.Character{{
			ID:       "hero",
			Location: "camp",
			Items: []world.CharacterItem{
				{Item: "old_wood", Count: 2},
				{Item: "stone", Count: 1},
				{Item: "stick", Count: 3},
			},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	// 推进到非零时间片（不移动、物品不变）。
	if _, err := w0.Apply(world.Commit{Time: 3}); err != nil {
		log.Fatal(err)
	}
	arch0, err := world.Create(dir)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := arch0.Save(slot, w0); err != nil {
		log.Fatal(err)
	}

	arch, err := world.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	src, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printMigrateState("准备", src.State)

	// 2) 为什么需要显式迁移：直接升级只能原样承接角色状态，不会替使用者
	//    合并物品。旧木材与旧树枝在 v2 中已不被允许，检查给出两条物品阻碍。
	check, err := arch.CheckUpgrade(slot, acceptedV1, rulesV2(6))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("直接升级可承接=%t 阻碍数=%d\n", check.Compatible, len(check.Blockers))
	for _, b := range check.Blockers {
		fmt.Printf("直接升级阻碍: 角色=%s 种类=%s 名称=%s\n", b.Character, b.Kind, b.Item)
	}

	// 3) 目标携带上限先设为 5：预览正常返回转换结果，但不可提交。
	//    旧木材(2) 与旧树枝(3) 合并为木料 5，保留在原列表中旧木材首次
	//    出现的位置（最前）；石头(1) 未指定对应关系，保持在原位。
	pv, err := arch.PreviewMigration(slot, acceptedV1, rulesV2(5), nil, itemMaps)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("预览(上限5): 来源=%s 目标=%s 可提交=%t 阻碍数=%d\n",
		pv.OldVersion, pv.TargetVersion, pv.Committable, len(pv.Blockers))
	printMigrateState("预览(上限5)", pv.State)
	for _, b := range pv.Blockers {
		fmt.Printf("预览阻碍: 角色=%s 种类=%s 总量=%d 上限=%d\n",
			b.Character, b.Kind, b.Total, b.Limit)
	}

	// 预览只读：原记录仍是三个条目、仍按 v1 保存——预览状态里出现 v2
	// 规则不代表原槽已经切换版本。
	cur, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("预览后原槽: 记录版本=%s 物品条目数=%d\n",
		cur.State.Rules.Version, len(cur.State.Characters[0].Items))
	printMigrateState("预览后原槽", cur.State)

	// 4) 带着预览给出的来源记录标识正式迁移：仍有阻碍时返回规则错误，
	//    结果里附带同一份预览与阻碍，槽当前记录和历史都保持原样。
	res, err := arch.Migrate(slot, acceptedV1, rulesV2(5), nil, itemMaps, pv.RecordID)
	var re *world.RuleError
	if !errors.As(err, &re) {
		log.Fatalf("有阻碍时应返回 *world.RuleError，得到 %v", err)
	}
	fmt.Printf("上限5迁移: 错误类型=%T 结果阻碍数=%d\n", err, len(res.Preview.Blockers))
	fmt.Println("上限5迁移:", err)
	still, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printMigrateState("拒绝后原槽", still.State)
	hist, err := arch.History(slot)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("拒绝后历史条数: %d\n", len(hist))

	// 5) 目标上限调整为 6，重新预览：这次没有阻碍，可以提交。
	pv2, err := arch.PreviewMigration(slot, acceptedV1, rulesV2(6), nil, itemMaps)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("预览(上限6): 来源=%s 目标=%s 可提交=%t 阻碍数=%d\n",
		pv2.OldVersion, pv2.TargetVersion, pv2.Committable, len(pv2.Blockers))
	// 预览状态只是给调用方查看的深拷贝：改掉其中数量不会成为保存内容，
	// 正式迁移以排他锁下重新读取和计算的结果为准。
	pv2.State.Characters[0].Items[0].Count = 999
	fmt.Printf("修改预览数量: lumber=%d（只影响调用方拿到的副本）\n",
		pv2.State.Characters[0].Items[0].Count)

	// 6) 正式迁移仍要传入目标规则、对应关系和此次预览给出的来源标识。
	sourceID := pv2.RecordID
	done, err := arch.Migrate(slot, acceptedV1, rulesV2(6), nil, itemMaps, sourceID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("迁移: 新记录标识非空且独立=%t 以迁移前记录为父=%t\n",
		done.Record.ID != "" && done.Record.ID != sourceID, done.Record.Parent == sourceID)

	// 7) 读回原槽：用只接受 v2 的集合读当前记录。新版本、合并后的物品；
	//    种子、时间片与角色位置保持原样。
	migrated, err := arch.Latest(slot, acceptedV2)
	if err != nil {
		log.Fatal(err)
	}
	printMigrateState("迁移后当前", migrated.State)
	fmt.Printf("槽当前即迁移新记录=%t 物品种类=%v\n",
		migrated.ID == done.Record.ID, migrated.State.Rules.ItemKinds)

	// 新记录拥有独立标识、以迁移前记录为父；历史增加一条，新记录在最前。
	hist, err = arch.History(slot)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("历史条数: %d\n", len(hist))
	fmt.Printf("历史[0]: 版本=%s 是迁移新记录=%t\n", hist[0].Version, hist[0].ID == done.Record.ID)
	fmt.Printf("历史[1]: 版本=%s 是迁移来源=%t 新记录父即此条=%t\n",
		hist[1].Version, hist[1].ID == sourceID, hist[0].Parent == hist[1].ID)

	// 迁移前的旧记录仍能按旧版本读取，内容仍是三种旧物品。
	old, err := arch.Record(slot, sourceID, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printMigrateState("旧记录按v1读取", old.State)

	// 可接受版本集合是读取方的显式取舍：新记录是 v2，只接受 v1 时
	// 读当前记录会被版本拒绝，必须把 v2 放进集合（正如迁移时放入 v1）。
	if _, err := arch.Latest(slot, acceptedV1); err != nil {
		var vr *world.VersionRejectedError
		if !errors.As(err, &vr) {
			log.Fatal(err)
		}
		fmt.Printf("新记录按v1读取: 版本拒绝 记录版本=%s\n", vr.Version)
	}

	// Output:
	// 准备: seed=20240801 rules=v1 time=3 hero@camp old_wood=2 stone=1 stick=3
	// 直接升级可承接=false 阻碍数=2
	// 直接升级阻碍: 角色=hero 种类=item 名称=old_wood
	// 直接升级阻碍: 角色=hero 种类=item 名称=stick
	// 预览(上限5): 来源=v1 目标=v2 可提交=false 阻碍数=1
	// 预览(上限5): seed=20240801 rules=v2 time=3 hero@camp lumber=5 stone=1
	// 预览阻碍: 角色=hero 种类=limit 总量=6 上限=5
	// 预览后原槽: 记录版本=v1 物品条目数=3
	// 预览后原槽: seed=20240801 rules=v1 time=3 hero@camp old_wood=2 stone=1 stick=3
	// 上限5迁移: 错误类型=*world.RuleError 结果阻碍数=1
	// 上限5迁移: world: 迁移被阻碍: 转换后的状态无法在目标规则下提交（1 处阻碍）
	// 拒绝后原槽: seed=20240801 rules=v1 time=3 hero@camp old_wood=2 stone=1 stick=3
	// 拒绝后历史条数: 1
	// 预览(上限6): 来源=v1 目标=v2 可提交=true 阻碍数=0
	// 修改预览数量: lumber=999（只影响调用方拿到的副本）
	// 迁移: 新记录标识非空且独立=true 以迁移前记录为父=true
	// 迁移后当前: seed=20240801 rules=v2 time=3 hero@camp lumber=5 stone=1
	// 槽当前即迁移新记录=true 物品种类=[lumber stone]
	// 历史条数: 2
	// 历史[0]: 版本=v2 是迁移新记录=true
	// 历史[1]: 版本=v1 是迁移来源=true 新记录父即此条=true
	// 旧记录按v1读取: seed=20240801 rules=v1 time=3 hero@camp old_wood=2 stone=1 stick=3
	// 新记录按v1读取: 版本拒绝 记录版本=v2
}
