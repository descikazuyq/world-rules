package world_test

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/descikazuyq/world-rules/world"
)

// printContinueState 以固定格式打印一行世界状态，便于对照读档前后的变化。
func printContinueState(tag string, s world.State) {
	c := s.Characters[0]
	fmt.Printf("%s: time=%d %s@%s", tag, s.Time, c.ID, c.Location)
	for _, it := range c.Items {
		fmt.Printf(" %s=%d", it.Item, it.Count)
	}
	fmt.Println()
}

// Example_continueSavedGame 演示读档后在同一存档槽上继续：
// 首次保存（角色已有位置、物品和非零时间片）-> 重新打开存档 ->
// 显式给出可接受规则版本并读回当前记录 -> WorldFromState 承接完整状态 ->
// 内存中 Apply 一次合法移动与物品变化 -> 带读回记录标识 Replace 覆盖回原槽
// -> 分别读取新当前记录与被覆盖的旧记录；并演示版本拒绝与标识过期冲突。
func Example_continueSavedGame() {
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

	dir, err := os.MkdirTemp("", "world-continue-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// 1) 准备世界：NewWorld 的时间片固定从 0 开始。
	w0, err := world.NewWorld(world.InitialData{
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
	printContinueState("新建", w0.Snapshot())

	// 推进到非零时间片：沿 village-forest 移动并改变物品数量，总量 4 <= 10。
	if _, err := w0.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "wood", Delta: 1}},
		Time:        4,
	}); err != nil {
		log.Fatal(err)
	}

	// 2) 首次保存：创建命名槽，产生槽首记录；保存时角色已有位置、物品和
	// 非零时间片。
	arch0, err := world.Create(dir)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := arch0.Save(slot, w0); err != nil {
		log.Fatal(err)
	}
	printContinueState("准备", w0.Snapshot())

	// 3) 另一次会话：重新打开这份存档，显式给出可接受的规则版本，读回槽
	// 当前记录。读取不改写存档。
	arch, err := world.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	rec, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}

	// 读回的记录保留种子、完整规则、角色物品内容与已保存的时间片。
	s := rec.State
	var roads []string
	for _, e := range s.Rules.Edges {
		roads = append(roads, e.From+"-"+e.To)
	}
	fmt.Printf("读档: seed=%d rules=%s time=%d\n", s.Seed, s.Rules.Version, s.Time)
	fmt.Printf("读档: places=%v roads=%v kinds=%v hero上限=%d\n",
		s.Rules.Locations, roads, s.Rules.ItemKinds, s.Rules.CarryLimits["hero"])
	printContinueState("读档", s)

	// 4) 承接完整状态重建可继续的世界：保留时间片 4。继续存档只能用
	// WorldFromState；NewWorld 建立的是时间从零开始的新世界。
	w, err := world.WorldFromState(rec.State)
	if err != nil {
		log.Fatal(err)
	}

	// Commit.Time 是目标绝对时间片，不是增加的步数：3 早于读回的 4，
	// 即使没有任何移动或物品变化也必须失败。
	if _, err := w.Apply(world.Commit{Time: 3}); err != nil {
		fmt.Printf("拒绝时间倒退: %v\n", err)
	}

	// 一次合法的继续：沿 forest-mine 道路移动，ore +2（1->3，总量 6 <= 10），
	// 目标绝对时间片为 7。
	if _, err := w.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "mine"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "ore", Delta: 2}},
		Time:        7,
	}); err != nil {
		log.Fatal(err)
	}
	printContinueState("继续", w.Snapshot())

	// Apply 成功只改变内存世界：槽内当前记录仍是覆盖前的内容。
	stored, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printContinueState("存档未变", stored.State)

	// 5) 覆盖回原槽必须带上本次读回的记录标识：成功后得到独立的新标识，
	// 新记录以读回记录为父；first/rec 等旧记录不受影响。
	next, err := arch.Replace(slot, w, rec.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("覆盖: 新标识独立=%t 以读回记录为父=%t\n",
		next.ID != rec.ID, next.Parent == rec.ID)

	cur, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printContinueState("当前", cur.State)

	// 被覆盖的原记录仍可按其标识读取。
	old, err := arch.Record(slot, rec.ID, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printContinueState("旧记录", old.State)

	// 失败条件一：记录规则版本不在可接受集合内 -> 版本拒绝，不能把这份
	// 记录当成新世界继续。
	if _, err := arch.Latest(slot, []string{"v9"}); err != nil {
		var vr *world.VersionRejectedError
		if !errors.As(err, &vr) {
			log.Fatal(err)
		}
		fmt.Printf("版本拒绝: %t 记录版本=%s\n", true, vr.Version)
	}

	// 失败条件二：读回的标识已经过期（槽刚被本次覆盖推进）-> 覆盖报冲突，
	// 已有当前记录保持原样。
	if _, err := arch.Replace(slot, w, rec.ID); err != nil {
		var ce *world.ConflictError
		if !errors.As(err, &ce) {
			log.Fatal(err)
		}
		fmt.Printf("冲突: 过期标识覆盖被拒绝=%t\n", true)
	}
	unchanged, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printContinueState("当前仍在", unchanged.State)

	// 正确后续：重新读取当前记录后再决定。这里在内存世界上再做一次合法
	// 提交（mine->forest，ore -1，目标时间片 9），用重新读回的标识覆盖，
	// 新记录以重读标识为父。
	if _, err := w.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "ore", Delta: -1}},
		Time:        9,
	}); err != nil {
		log.Fatal(err)
	}
	retried, err := arch.Replace(slot, w, unchanged.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("重读后重试: 父记录为重读标识=%t\n", retried.Parent == unchanged.ID)
	final, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printContinueState("最终", final.State)

	// Output:
	// 新建: time=0 hero@village wood=2 ore=1
	// 准备: time=4 hero@forest wood=3 ore=1
	// 读档: seed=20240801 rules=v1 time=4
	// 读档: places=[village forest mine] roads=[village-forest forest-mine] kinds=[wood ore] hero上限=10
	// 读档: time=4 hero@forest wood=3 ore=1
	// 拒绝时间倒退: world: 时间不能倒退: 当前 4, 提交目标 3
	// 继续: time=7 hero@mine wood=3 ore=3
	// 存档未变: time=4 hero@forest wood=3 ore=1
	// 覆盖: 新标识独立=true 以读回记录为父=true
	// 当前: time=7 hero@mine wood=3 ore=3
	// 旧记录: time=4 hero@forest wood=3 ore=1
	// 版本拒绝: true 记录版本=v1
	// 冲突: 过期标识覆盖被拒绝=true
	// 当前仍在: time=7 hero@mine wood=3 ore=3
	// 重读后重试: 父记录为重读标识=true
	// 最终: time=9 hero@forest wood=3 ore=2
}
