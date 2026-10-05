package world_test

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/descikazuyq/world-rules/world"
)

// printHistoryState 以固定格式打印一行世界状态，便于对照不同记录的区别。
func printHistoryState(tag string, s world.State) {
	c := s.Characters[0]
	fmt.Printf("%s: rules=%s time=%d %s@%s", tag, s.Rules.Version, s.Time, c.ID, c.Location)
	for _, it := range c.Items {
		fmt.Printf(" %s=%d", it.Item, it.Count)
	}
	fmt.Println()
}

// Example_browseSlotHistory 演示先浏览一个槽已保存的历史，再挑选一份旧
// 记录读取当时的完整世界：
//
//	Save -> Replace -> Upgrade 产生三次保存 -> History 浏览元信息
//	（记录标识、父标识、槽首标记、规则版本）-> 按列表中的标识用 Record
//	读取槽首旧记录 -> 对照当前记录 -> 演示旧版本记录在列表中但读取需版本
//	可接受 -> 从旧记录分支并只浏览分支自身的历史 -> 确认浏览与读取均未
//	切换槽当前记录。
func Example_browseSlotHistory() {
	const slot = "adventure"
	acceptedV1 := []string{"v1"}
	acceptedV1V2 := []string{"v1", "v2"}

	// 示例规则 v1：三个地点、两条无向道路、两种物品，hero 携带总量上限 10。
	rulesV1 := world.Rules{
		Version:   "v1",
		Locations: []string{"village", "forest", "mine"},
		Edges: []world.Edge{
			{From: "village", To: "forest"},
			{From: "forest", To: "mine"},
		},
		ItemKinds:   []string{"wood", "ore"},
		CarryLimits: map[string]int{"hero": 10},
	}
	// v2 与 v1 兼容（地点、道路、物品与上限都不变，只改版本号），升级可以
	// 原样承接角色状态。
	rulesV2 := rulesV1
	rulesV2.Version = "v2"

	dir, err := os.MkdirTemp("", "world-history-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// 1) 建立世界并产生三次保存：首存 -> 覆盖 -> 规则升级。
	w0, err := world.NewWorld(world.InitialData{
		Seed:  20240801,
		Rules: rulesV1,
		Characters: []world.Character{{
			ID:       "hero",
			Location: "village",
			Items: []world.CharacterItem{
				{Item: "wood", Count: 2},
				{Item: "ore", Count: 1},
			},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	// 推进到非零时间片 4：village->forest，wood +1。
	if _, err := w0.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "wood", Delta: 1}},
		Time:        4,
	}); err != nil {
		log.Fatal(err)
	}
	arch0, err := world.Create(dir)
	if err != nil {
		log.Fatal(err)
	}
	first, err := arch0.Save(slot, w0) // 第一次保存：槽首记录
	if err != nil {
		log.Fatal(err)
	}

	arch, err := world.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	// 读回槽首记录，承接状态后继续，再覆盖回原槽（第二次保存）。
	r0, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	w, err := world.WorldFromState(r0.State)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := w.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "mine"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "ore", Delta: 2}},
		Time:        7,
	}); err != nil {
		log.Fatal(err)
	}
	second, err := arch.Replace(slot, w, r0.ID)
	if err != nil {
		log.Fatal(err)
	}

	// 第三次保存：在状态可被 v2 原样承接时升级规则，时间片与状态不变。
	check, err := arch.CheckUpgrade(slot, acceptedV1, rulesV2)
	if err != nil {
		log.Fatal(err)
	}
	if !check.Compatible {
		log.Fatal("示例前提：v2 应兼容 v1 状态")
	}
	upgraded, err := arch.Upgrade(slot, acceptedV1, rulesV2, second.ID)
	if err != nil {
		log.Fatal(err)
	}

	// 2) 浏览历史：不需要可接受版本参数，列表按保存生效次序排列，最近保存
	// 的在前，与世界时间片大小或文件修改时间无关。
	infos, err := arch.History(slot)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("历史条数: %d\n", len(infos))
	for i, info := range infos {
		// 元信息只给标识、父标识、槽首标记与规则版本，读取完整世界要用
		// Record 按标识取回。
		fmt.Printf("历史[%d]: 版本=%s 槽首=%t 父标识为空=%t\n",
			i, info.Version, info.SlotFirst, info.Parent == "")
	}
	// 父记录关系与列表次序一致：每条较新记录的父就是它上一次生效的保存。
	fmt.Printf("父记录关系: 第0条父=第1条=%t 第1条父=第2条=%t\n",
		infos[0].Parent == infos[1].ID, infos[1].Parent == infos[2].ID)
	// 不能把“槽首记录”等同于“父标识为空”：只有直接建立的槽首记录没有父；
	// 本槽由 Save 直接建立，故末条记录两者同时成立。
	fmt.Printf("槽首即直接建立: 末条槽首=%t 末条父为空=%t\n",
		infos[len(infos)-1].SlotFirst, infos[len(infos)-1].Parent == "")

	// 3) 从浏览结果中选一份旧记录（这里选列表末尾的槽首记录），用 Record
	// 显式给出可接受版本后读取当时的完整世界。标识直接取自列表，无需手工
	// 填写，也不依赖预置数据。
	chosen := infos[len(infos)-1]
	old, err := arch.Record(slot, chosen.ID, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printHistoryState("选中的旧记录", old.State)
	fmt.Printf("选中记录: 与列表标识一致=%t 槽首=%t\n", old.ID == chosen.ID, old.SlotFirst)

	// 当前记录仍是升级后的 v2：浏览和按标识读取都不切换槽当前记录。
	cur, err := arch.Latest(slot, acceptedV1V2)
	if err != nil {
		log.Fatal(err)
	}
	printHistoryState("当前记录", cur.State)
	fmt.Printf("读取旧记录不切换当前: 当前仍是最新一条=%t\n", cur.ID == infos[0].ID)

	// 4) History 不筛选规则版本：完好但版本较旧/较新的记录都会列出（上面的
	// v2 与 v1 记录同时出现）。但 Record 读取必须显式接受该版本；用只接受
	// v1 的集合读取 v2 的最新记录，得到版本拒绝——这不是历史列表出错。
	if _, err := arch.Record(slot, infos[0].ID, acceptedV1); err != nil {
		var vr *world.VersionRejectedError
		if !errors.As(err, &vr) {
			log.Fatal(err)
		}
		fmt.Printf("版本拒绝: 记录版本=%s 未在可接受集合内=%t\n", vr.Version, true)
	}

	// 5) 从选中的槽首旧记录分出新槽。分支首条记录以来源记录为父记录，但它
	// 本身仍是新槽的槽首记录；分支槽的历史只列分支自身保存的记录。
	b0, err := arch.Branch(slot, chosen.ID, "branch")
	if err != nil {
		log.Fatal(err)
	}
	bFirst, err := arch.Latest("branch", acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	bw, err := world.WorldFromState(bFirst.State)
	if err != nil {
		log.Fatal(err)
	}
	// 在分支上继续一次：forest->village，时间片推进到 9，再覆盖保存。
	if _, err := bw.Apply(world.Commit{
		Moves: []world.Move{{Character: "hero", To: "village"}},
		Time:  9,
	}); err != nil {
		log.Fatal(err)
	}
	if _, err := arch.Replace("branch", bw, b0.ID); err != nil {
		log.Fatal(err)
	}

	bh, err := arch.History("branch")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("分支历史条数: %d（不含来源槽历史）\n", len(bh))
	for i, info := range bh {
		fmt.Printf("分支历史[%d]: 版本=%s 槽首=%t 父标识为空=%t\n",
			i, info.Version, info.SlotFirst, info.Parent == "")
	}
	// 分支首记录是槽首，却仍以来源槽记录为父——槽首不等于父标识为空。
	fmt.Printf("分支首条: 槽首=%t 父为来源记录=%t 父非空=%t\n",
		bh[len(bh)-1].SlotFirst, bh[len(bh)-1].Parent == chosen.ID,
		bh[len(bh)-1].Parent != "")
	// 来源槽的任何记录都不会被补进分支历史。
	mainIDs := map[world.RecordID]bool{first.ID: true, second.ID: true, upgraded.Record.ID: true}
	onlyOwn := true
	for _, info := range bh {
		if mainIDs[info.ID] {
			onlyOwn = false
		}
	}
	fmt.Printf("分支只列自身记录: %t\n", onlyOwn)
	// 来源槽自己的历史不受分支影响，仍是三条。
	mainHist, err := arch.History(slot)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("来源槽历史不变: 条数=%d 次序不变=%t\n",
		len(mainHist), mainHist[0].ID == infos[0].ID && mainHist[2].ID == infos[2].ID)

	// 6) 槽不存在与“存在但没有合格记录的空列表”是两种结果：不存在报
	// *NotFoundError，不能当成成功的空列表。
	if _, err := arch.History("no-such-slot"); err != nil {
		fmt.Printf("槽不存在: %t\n", errors.As(err, new(*world.NotFoundError)))
	}

	// 7) 全部浏览与读取结束后，槽当前记录及其世界仍是开始浏览时的样子：
	// 不切换当前、不修补记录、不替换规则。
	after, err := arch.Latest(slot, acceptedV1V2)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("浏览后当前未变: 标识相同=%t\n", after.ID == cur.ID)
	printHistoryState("浏览后当前", after.State)

	// Output:
	// 历史条数: 3
	// 历史[0]: 版本=v2 槽首=false 父标识为空=false
	// 历史[1]: 版本=v1 槽首=false 父标识为空=false
	// 历史[2]: 版本=v1 槽首=true 父标识为空=true
	// 父记录关系: 第0条父=第1条=true 第1条父=第2条=true
	// 槽首即直接建立: 末条槽首=true 末条父为空=true
	// 选中的旧记录: rules=v1 time=4 hero@forest wood=3 ore=1
	// 选中记录: 与列表标识一致=true 槽首=true
	// 当前记录: rules=v2 time=7 hero@mine wood=3 ore=3
	// 读取旧记录不切换当前: 当前仍是最新一条=true
	// 版本拒绝: 记录版本=v2 未在可接受集合内=true
	// 分支历史条数: 2（不含来源槽历史）
	// 分支历史[0]: 版本=v1 槽首=false 父标识为空=false
	// 分支历史[1]: 版本=v1 槽首=true 父标识为空=false
	// 分支首条: 槽首=true 父为来源记录=true 父非空=true
	// 分支只列自身记录: true
	// 来源槽历史不变: 条数=3 次序不变=true
	// 槽不存在: true
	// 浏览后当前未变: 标识相同=true
	// 浏览后当前: rules=v2 time=7 hero@mine wood=3 ore=3
}
