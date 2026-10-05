package world_test

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/descikazuyq/world-rules/world"
)

// Example_browseHistory 演示先用 History 浏览存档槽的历史列表（记录标识、
// 父标识、槽首标记、规则版本），再从中选取一份旧记录用 Record 读取当时的
// 完整世界。示例自行建立存档并产生三次保存，不依赖预置数据或手工填写的
// 记录标识；浏览与读取都是只读操作，不切换槽当前记录、不修补记录，也不
// 替换世界规则。
func Example_browseHistory() {
	const slot = "adventure"
	acceptedV1 := []string{"v1"}

	// 示例规则：三个地点、两条无向道路、两种物品，hero 携带总量上限 10。
	rules := world.Rules{
		Version:   "v1",
		Locations: []string{"village", "forest", "mine"},
		Edges: []world.Edge{
			{From: "village", To: "forest"},
			{From: "forest", To: "mine"},
		},
		ItemKinds:   []string{"wood", "ore"},
		CarryLimits: map[string]int{"hero": 10},
	}

	dir, err := os.MkdirTemp("", "world-history-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// 1) 建立世界并产生三次保存：首次保存创建槽（槽首记录），之后两次
	// 带记录标识覆盖，各得到一条独立的新记录。
	w, err := world.NewWorld(world.InitialData{
		Seed:  20240801,
		Rules: rules,
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
	arch0, err := world.Create(dir)
	if err != nil {
		log.Fatal(err)
	}

	// 保存 1：village->forest，wood +1，目标绝对时间片 4。
	if _, err := w.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "wood", Delta: 1}},
		Time:        4,
	}); err != nil {
		log.Fatal(err)
	}
	save1, err := arch0.Save(slot, w)
	if err != nil {
		log.Fatal(err)
	}
	printContinueState("保存1", w.Snapshot())

	// 保存 2：forest->mine，ore +2，目标时间片 7；以保存 1 为父。
	if _, err := w.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "mine"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "ore", Delta: 2}},
		Time:        7,
	}); err != nil {
		log.Fatal(err)
	}
	save2, err := arch0.Replace(slot, w, save1.ID)
	if err != nil {
		log.Fatal(err)
	}
	printContinueState("保存2", w.Snapshot())

	// 保存 3：mine->forest，ore -1，目标时间片 9；以保存 2 为父。
	if _, err := w.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "ore", Delta: -1}},
		Time:        9,
	}); err != nil {
		log.Fatal(err)
	}
	save3, err := arch0.Replace(slot, w, save2.ID)
	if err != nil {
		log.Fatal(err)
	}
	printContinueState("保存3", w.Snapshot())

	// 2) 另一次会话：重新打开存档，浏览历史列表。列表按保存生效的先后
	// 排列，最近保存的在前；每项给出记录标识、父标识、槽首标记与规则
	// 版本。浏览是只读操作。
	arch, err := world.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	infos, err := arch.History(slot)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("历史条数: %d\n", len(infos))
	fmt.Printf("历史[0]: 是保存3=%t 父为保存2=%t 槽首=%t 版本=%s\n",
		infos[0].ID == save3.ID, infos[0].Parent == save2.ID,
		infos[0].SlotFirst, infos[0].Version)
	fmt.Printf("历史[1]: 是保存2=%t 父为保存1=%t 槽首=%t 版本=%s\n",
		infos[1].ID == save2.ID, infos[1].Parent == save1.ID,
		infos[1].SlotFirst, infos[1].Version)
	// 只有直接建立的槽首记录父标识为空。
	fmt.Printf("历史[2]: 是保存1=%t 父标识为空=%t 槽首=%t 版本=%s\n",
		infos[2].ID == save1.ID, infos[2].Parent == "",
		infos[2].SlotFirst, infos[2].Version)

	// 3) 从列表中选取中间那份旧记录，用 Record 按标识读取当时的完整
	// 世界：非零时间片 7 与当时的角色状态原样取回，与当前记录（时间片
	// 9、位置与物品不同）相互独立。
	pick := infos[1]
	old, err := arch.Record(slot, pick.ID, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("选取: 标识与列表一致=%t 父与列表一致=%t\n",
		old.ID == pick.ID, old.Parent == pick.Parent)
	printContinueState("旧记录", old.State)

	// 列表中的版本只是元信息，History 不筛选版本；用 Record 读取时必须
	// 显式给出可接受版本，版本不被接受得到版本拒绝，这不说明列表有错。
	if _, err := arch.Record(slot, pick.ID, []string{"v9"}); err != nil {
		var vr *world.VersionRejectedError
		if !errors.As(err, &vr) {
			log.Fatal(err)
		}
		fmt.Printf("版本拒绝: 记录版本=%s\n", vr.Version)
	}

	// 浏览与读取都不切换槽当前记录：Latest 仍返回最近一次保存。
	cur, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("当前仍是保存3=%t\n", cur.ID == save3.ID)
	printContinueState("当前", cur.State)

	// Output:
	// 保存1: time=4 hero@forest wood=3 ore=1
	// 保存2: time=7 hero@mine wood=3 ore=3
	// 保存3: time=9 hero@forest wood=3 ore=2
	// 历史条数: 3
	// 历史[0]: 是保存3=true 父为保存2=true 槽首=false 版本=v1
	// 历史[1]: 是保存2=true 父为保存1=true 槽首=false 版本=v1
	// 历史[2]: 是保存1=true 父标识为空=true 槽首=true 版本=v1
	// 选取: 标识与列表一致=true 父与列表一致=true
	// 旧记录: time=7 hero@mine wood=3 ore=3
	// 版本拒绝: 记录版本=v1
	// 当前仍是保存3=true
	// 当前: time=9 hero@forest wood=3 ore=2
}
