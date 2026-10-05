package world_test

import (
	"errors"
	"fmt"
	"log"
	"os"

	world "github.com/descikazuyq/world-rules/world"
)

// 读档后继续操作同一存档槽：保存一份已有位置、物品和非零时间片的
// 世界，重新打开存档并显式指定可接受的规则版本，读出当前记录后用
// WorldFromState 重建世界继续提交，最后带上读到的记录标识覆盖保存，
// 并演示版本拒绝与标识过期两种失败。
func Example_continueAfterRead() {
	rules := world.Rules{
		Version:   "v1",
		Locations: []string{"广场", "集市", "港口"},
		Edges: []world.Edge{
			{From: "广场", To: "集市"},
			{From: "集市", To: "港口"},
		},
		ItemKinds:   []string{"面包", "药水"},
		CarryLimits: map[string]int{"阿黎": 10},
	}

	// 建立世界并推进到时间片 5：阿黎沿道路从广场走到集市，吃掉一个面包。
	w, err := world.NewWorld(world.InitialData{
		Seed:  42,
		Rules: rules,
		Characters: []world.Character{
			{ID: "阿黎", Location: "广场", Items: []world.CharacterItem{{Item: "面包", Count: 3}}},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	if _, err := w.Apply(world.Commit{
		Moves:       []world.Move{{Character: "阿黎", To: "集市"}},
		ItemChanges: []world.ItemChange{{Character: "阿黎", Item: "面包", Delta: -1}},
		Time:        5, // 提交时间是目标绝对时间片，不是增加的步数
	}); err != nil {
		log.Fatal(err)
	}

	// 首次保存：创建存档槽 main，角色已带位置、物品和非零时间片。
	dir, err := os.MkdirTemp("", "world-archive-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	arc, err := world.Create(dir)
	if err != nil {
		log.Fatal(err)
	}
	first, err := arc.Save("main", w)
	if err != nil {
		log.Fatal(err)
	}

	// 重新打开存档，显式给出可接受的规则版本集合，读取当前记录。
	reopened, err := world.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	accepted := []string{"v1"}
	rec, err := reopened.Latest("main", accepted)
	if err != nil {
		log.Fatal(err)
	}
	st := rec.State
	fmt.Printf("读回: 是首次保存的记录=%v 种子=%d 版本=%s 时间片=%d\n",
		rec.ID == first.ID, st.Seed, st.Rules.Version, st.Time)
	fmt.Printf("读回: 阿黎在%s 面包=%d\n", st.Characters[0].Location, st.Characters[0].Items[0].Count)

	// WorldFromState 承接记录中的完整状态（含时间片），重建可继续操作
	// 的世界；NewWorld 建立的则是时间从零开始的新世界，不能用于读档。
	w2, err := world.WorldFromState(st)
	if err != nil {
		log.Fatal(err)
	}

	// 继续操作：沿道路走到港口、获得两瓶药水，提交到绝对时间片 9
	// （不能早于读回的时间片 5）。Apply 只改内存世界，存档保持原样。
	if _, err := w2.Apply(world.Commit{
		Moves:       []world.Move{{Character: "阿黎", To: "港口"}},
		ItemChanges: []world.ItemChange{{Character: "阿黎", Item: "药水", Delta: 2}},
		Time:        9,
	}); err != nil {
		log.Fatal(err)
	}
	before, err := reopened.Latest("main", accepted)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Apply 后存档仍是: 时间片=%d 阿黎在%s\n",
		before.State.Time, before.State.Characters[0].Location)

	// 覆盖保存到原槽：带上本次读取得到的记录标识。成功得到独立的新
	// 标识，父记录是被覆盖的原记录。
	second, err := reopened.Replace("main", w2, rec.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("覆盖: 新标识不同于原记录=%v 父记录是原记录=%v\n",
		second.ID != rec.ID, second.Parent == rec.ID)

	// 之后读取当前记录能看到更新后的状态，原记录仍可按标识读取。
	cur, err := reopened.Latest("main", accepted)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("覆盖后当前: 时间片=%d 阿黎在%s 面包=%d 药水=%d\n",
		cur.State.Time, cur.State.Characters[0].Location,
		cur.State.Characters[0].Items[0].Count, cur.State.Characters[0].Items[1].Count)
	old, err := reopened.Record("main", rec.ID, accepted)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("原记录仍可读: 时间片=%d 阿黎在%s\n",
		old.State.Time, old.State.Characters[0].Location)

	// 失败条件一：记录的规则版本不在可接受集合内时读取报告版本拒绝，
	// 不能把记录当成新世界继续。
	_, err = reopened.Latest("main", []string{"v9"})
	var verErr *world.VersionRejectedError
	fmt.Printf("版本拒绝: %v (记录版本=%s)\n", errors.As(err, &verErr), verErr.Version)

	// 失败条件二：读到的标识已经过期（槽已被覆盖）时再次覆盖报告冲突，
	// 已有当前记录保持原样，调用方需重新读取后再决定后续操作。
	_, err = reopened.Replace("main", w2, rec.ID)
	var conflict *world.ConflictError
	fmt.Printf("标识过期: %v\n", errors.As(err, &conflict))
	still, err := reopened.Latest("main", accepted)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("冲突后当前记录未变: 时间片=%d 阿黎在%s\n",
		still.State.Time, still.State.Characters[0].Location)

	// Output:
	// 读回: 是首次保存的记录=true 种子=42 版本=v1 时间片=5
	// 读回: 阿黎在集市 面包=2
	// Apply 后存档仍是: 时间片=5 阿黎在集市
	// 覆盖: 新标识不同于原记录=true 父记录是原记录=true
	// 覆盖后当前: 时间片=9 阿黎在港口 面包=2 药水=2
	// 原记录仍可读: 时间片=5 阿黎在集市
	// 版本拒绝: true (记录版本=v1)
	// 标识过期: true
	// 冲突后当前记录未变: 时间片=9 阿黎在港口
}
