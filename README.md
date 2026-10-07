# 本地世界规则与存档

带规则约束的本地世界与命名存档槽。

## 使用

```bash
go test ./...
```

## 概念速览

- `NewWorld(InitialData)` 建立世界：地图种子、非空规则版本、地点与
  连通关系、角色、物品；时间片从 0 开始。初始数据不合法会返回错误，
  世界不会建立。
- `GenerateWorld(GenerateRequest)` 按种子生成地图并建立世界：给出
  地点列表、必有道路、禁用道路与目标道路总数，以及规则版本、物品
  种类、携带上限和初始角色。生成的无向道路图包含全部必有道路、避开
  全部禁用道路、全图连通且道路数量严格等于目标值；同一种子与约束的
  结果完全一致，与输入次序、重复、正反写法及进程重启无关。约束不合法
  返回 `*RuleError`；该限制不影响 `NewWorld` 与旧存档，生成的地图随
  存档保存，读档不重新生成。
- `World.Apply(Commit)` 原子提交移动、物品数量变化与时间推进。任一
  项违规整次提交失败，世界保持提交前状态。同一角色同一物品在一次
  提交中列出多条增减时，按原有数量与全部增减合并后的最终结果判断，
  与条目次序无关，详见下文“一次提交中多次增减同一种物品”。
- `Create(dir)` / `Open(dir)` 管理存档目录。
- `Save(slot, world)` 首次保存创建命名槽，重名拒绝。
- `Replace(slot, world, expectedRecordID)` 乐观锁覆盖，父记录为被
  覆盖的记录；并发覆盖同一父记录只有一个成功。
- `Branch(srcSlot, recordID, dstSlot)` 从历史记录分出新槽，完整复制
  当时的种子、规则与状态，两槽此后互不影响。
- `Latest` / `Record` / `RecoverLatest` 读取时校验内容校验和（含父
  记录关系）与调用方给定的可接受规则版本集合。槽指针内嵌按保存生效
  次序排列的历史索引（与文件修改时间、世界时间片大小无关）；最新记录
  被删除、截断成无法解析的内容，或连同若干中间记录一起损坏/版本不被
  接受时，`RecoverLatest` 仍按该次序回溯到本槽最近一份校验通过且版本
  可接受的已保存记录，不读取受损记录里的父标识或槽首标记，也不会被
  引向别的槽。分支只在分支自身的记录中查找，越界不查；没有任何可用
  记录时返回 `ErrUnrecoverable`。旧版本裸指针可直接打开，第一次成功
  覆盖或升级后旧历史即获得同样的恢复能力。读取不改写任何数据。槽指针
  顶层同一字段（`latest`/`history`，含仅大小写不同或 Unicode 转义还原
  后同名的写法）重复出现时含义有歧义，整份指针按损坏处理：所有读取、
  浏览与恢复入口都返回带槽名的 `*CorruptError`，恢复不挑任一份历史
  继续，写入也拒绝承接且不修补指针；字段只出现一次时原有大小写与转义
  写法照常可读，`history` 数组内的重复记录标识仍按原规则去重。
- `History(slot)` 在不需要可接受版本、也不改写存档的前提下浏览一个槽
  已经保存生效的历史，按保存生效次序（最近保存的在前）返回每条记录的
  标识、父标识、槽首标记与规则版本；元信息不含完整世界，选定一份后再
  用 `Record(slot, id, acceptedVersions)` 按标识读取当时的完整世界。
  用法与各种边界见下文“浏览一个槽已保存的历史”。
- `PreviewRecovery(slot, acceptedVersions)` 在不改变存档的前提下返回槽
  当前指向的记录标识、选中的来源记录标识及其完整世界状态；选择规则与
  `RecoverLatest` 相同，当前记录完好且版本可接受时可以选它本身。槽不
  存在报不存在、指针无法解析或顶层字段重复报损坏（字段重复时不返回当前
  标识与来源）、找不到可用记录返回
  `ErrUnrecoverable`。
- `ConfirmRecovery(slot, current, source, acceptedVersions)` 把预览选中
  的来源正式保存回本槽：任一标识为空或槽已不再指向 `current` 时冲突
  （当前记录已损坏或被删除也不影响比较）；来源在排他锁下重新校验，他槽
  记录与未生效的孤儿记录按不存在拒绝，来源被删、损坏或版本不被接受分别
  返回已有错误，不改选其他来源。成功后产生一条以来源为父的独立新记录，
  种子、完整规则、时间片、角色及物品原样复制（允许时间片回到来源时刻），
  旧历史按原次序保留、新记录排在最前；并发确认及确认与覆盖或升级竞争时
  基于同一当前标识最多一个成功。写入中断后要么保持确认前状态，要么同时
  看到新记录与更新后的历史。
- `CheckUpgrade(slot, acceptedVersions, targetRules)` 检查槽当前最新
  记录能否在目标规则下继续使用，返回记录标识、旧版本、目标版本、是否
  兼容及具体阻碍；检查不改变世界、历史或槽当前记录。
- `Upgrade(slot, acceptedVersions, targetRules, expectedRecordID)`
  在检查通过后用目标规则生成一条新记录，以被升级记录为父，种子、时间
  片、角色位置及物品数量和排列保持原样；预期标识为空或过期时冲突，
  并发升级只有一个成功。
- `PreviewMigration(slot, acceptedVersions, targetRules, locationMappings, itemMappings)`
  预览把槽当前最新记录按对应关系显式迁移到目标规则：返回来源记录标识、
  旧新版本、转换后的完整状态及是否可提交。转换只作用于原始名称一次，
  未指定的名称保持原样；物品合并数量、地点改名，种子/时间片/角色标识
  与排列不变；仍有阻碍时一次列出全部。预览不改动存档。
- `Migrate(slot, acceptedVersions, targetRules, locationMappings, itemMappings, sourceRecordID)`
  在预览后正式迁移：排他锁下重新读取和计算，以来源为父生成新记录，
  保存目标规则与转换后的状态并加入保存次序；来源标识为空或槽已更新时
  冲突，仍有阻碍时拒绝保存。与覆盖、升级、确认恢复或另一次迁移竞争时
  同一来源最多一个成功。
- 写入通过目录内 flock 串行化，并以“临时文件写全 + 原子改名/硬链接”
  落盘，崩溃重开后只能看到旧的或新的完整记录。

## 读档后继续同一存档槽

`Save` 只能创建槽，`Latest`/`Record` 又只读不改；要在一份已保存的世界
上接着玩并把新进展写回**同一个槽**，需要三步：**读取记录 → 承接状态重建
世界 → 带记录标识覆盖保存**。三者职责不同，不要混用：

- **读取**（`Latest(slot, acceptedVersions)`、
  `Record(slot, id, acceptedVersions)`）：校验记录格式、内容校验和（含父
  记录关系）、世界状态与调用方给出的可接受规则版本集合，返回一份
  `Record`（记录标识 `ID`、父标识 `Parent` 与完整状态 `State`）。读取
  绝不改写存档。
- **内存提交**（`WorldFromState(record.State)` 后 `world.Apply(commit)`）：
  成功的 `Apply` 只改变内存里的世界，槽内记录保持保存时的内容。
- **覆盖保存**（`Replace(slot, world, record.ID)`）：把内存世界快照写成
  一条新记录。第三个参数必须是本次读取所得、且仍是该槽最新的记录标识；
  成功后得到一个**独立的新标识**，新记录以原记录为父，槽当前指向新记录，
  而被覆盖的原记录仍可用 `Record(slot, 旧标识, …)` 按标识读取。

### 用哪个入口建立世界

- `NewWorld(InitialData)` 建立**时间片从 0 开始的新世界**：只有手头有
  种子、规则和初始角色、确实要从头开始时才用它。它不会、也不能保留某份
  旧存档的时间片。
- `WorldFromState(State)` 用于**读档后继续**：入参是读回记录里的完整
  状态，种子、完整规则（地点、道路、物品种类、携带上限）、角色与物品的
  内容和排列，以及已保存的非零时间片全部原样承接，入参会被深拷贝。状态
  不自洽或时间片为负时返回 `*RuleError`，不会产生可用世界。

因此读档继续只能走 `Latest`/`Record` + `WorldFromState`；记录中保存的
地图随存档原样取回，读档不会按种子重新生成。

### 提交时间是绝对时间片

`Commit.Time` 是本次提交后期望到达的**目标绝对时间片**，不是“增加几步”。
它不能早于世界当前（也就是读回记录中）的时间片，否则整次提交失败并返回
`*RuleError`，世界保持提交前状态。移动必须沿记录规则中的道路，物品必须
是规则允许的种类，最终数量不能为负、真实总量不能超过携带上限；任一项
违规，移动、物品变化与时间推进一起回滚。

### 两个直接影响继续操作的失败条件

1. **规则版本不被接受**：记录本身完好，但其规则版本不在调用方显式给出的
   可接受集合内时，`Latest`/`Record` 返回 `*VersionRejectedError`（可用
   `errors.As` 判断）。这时不能把该记录当成新世界继续——既不应跳过版本
   校验，也不应改走 `NewWorld`（那会丢失已保存的时间片与状态）。需要先
   按“规则升级/显式迁移”处理，或把该版本加入可接受集合后重新读取。
2. **记录标识已过期**：读到记录后槽已被其他写入（另一次 `Replace`、
   `Upgrade`、`Migrate` 或 `ConfirmRecovery`）推进，再用旧标识
   `Replace` 会返回 `*ConflictError`，已有当前记录保持原样，本次内存世界
   不会落盘。调用方应当重新 `Latest` 读取当前记录，比对状态后再决定
   后续操作；示例末尾演示了“重新读取 → 用新标识覆盖”的正确重试。

### 完整示例

下面的程序可直接阅读使用（可运行版本在
`world/example_continue_test.go`，`go test ./...` 会校验其输出）：首次
保存时角色已有位置、物品和非零时间片；重新打开存档、显式指定可接受规则
版本后读回并重建世界，完成一次合法移动与物品数量变化，再覆盖回原槽，
并演示版本拒绝与标识过期冲突两种失败。

```go
package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/descikazuyq/world-rules/world"
)

func printState(tag string, s world.State) {
	c := s.Characters[0]
	fmt.Printf("%s: time=%d %s@%s", tag, s.Time, c.ID, c.Location)
	for _, it := range c.Items {
		fmt.Printf(" %s=%d", it.Item, it.Count)
	}
	fmt.Println()
}

func main() {
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

	// 1) NewWorld 建立时间从 0 开始的新世界。
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

	// 推进到非零时间片：沿 village-forest 移动并改变物品数量，总量 4 <= 10。
	if _, err := w0.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "wood", Delta: 1}},
		Time:        4, // 目标绝对时间片，不是增加的步数
	}); err != nil {
		log.Fatal(err)
	}

	// 2) 首次保存创建命名槽（保存时角色已有位置、物品和非零时间片）。
	arch0, err := world.Create(dir)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := arch0.Save(slot, w0); err != nil {
		log.Fatal(err)
	}

	// 3) 另一次会话：重新打开存档，显式给出可接受的规则版本并读回当前
	// 记录。读取不改写存档。
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
	fmt.Printf("读档: seed=%d rules=%s time=%d places=%v kinds=%v\n",
		s.Seed, s.Rules.Version, s.Time, s.Rules.Locations, s.Rules.ItemKinds)
	printState("读档", s)
	// 读档: seed=20240801 rules=v1 time=4 places=[village forest mine] kinds=[wood ore]
	// 读档: time=4 hero@forest wood=3 ore=1

	// 4) WorldFromState 承接完整状态（含时间片 4）重建可继续的世界。
	w, err := world.WorldFromState(rec.State)
	if err != nil {
		log.Fatal(err)
	}

	// 目标时间片 3 早于读回的 4：即使没有移动或物品变化也必须失败。
	if _, err := w.Apply(world.Commit{Time: 3}); err != nil {
		fmt.Println("拒绝时间倒退:", err)
	}

	// 一次合法的继续：沿 forest-mine 道路移动，ore +2（1->3，总量 6<=10），
	// 目标绝对时间片为 7。
	if _, err := w.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "mine"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "ore", Delta: 2}},
		Time:        7,
	}); err != nil {
		log.Fatal(err)
	}
	printState("继续", w.Snapshot())
	// 继续: time=7 hero@mine wood=3 ore=3

	// Apply 只改变内存世界：槽内当前记录仍是覆盖前的内容。
	stored, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printState("存档未变", stored.State)
	// 存档未变: time=4 hero@forest wood=3 ore=1

	// 5) 覆盖回原槽：带上本次读取所得的记录标识。成功后得到独立的新
	// 标识，新记录以读回记录为父。
	next, err := arch.Replace(slot, w, rec.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("覆盖: 新标识独立=%t 以读回记录为父=%t\n",
		next.ID != rec.ID, next.Parent == rec.ID)
	// 覆盖: 新标识独立=true 以读回记录为父=true

	cur, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printState("当前", cur.State)
	// 当前: time=7 hero@mine wood=3 ore=3

	// 被覆盖的原记录仍可按其标识读取。
	old, err := arch.Record(slot, rec.ID, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printState("旧记录", old.State)
	// 旧记录: time=4 hero@forest wood=3 ore=1

	// 失败条件一：规则版本不在可接受集合内 -> 版本拒绝，不能当成新世界继续。
	if _, err := arch.Latest(slot, []string{"v9"}); err != nil {
		var vr *world.VersionRejectedError
		if !errors.As(err, &vr) {
			log.Fatal(err)
		}
		fmt.Println("版本拒绝: 记录版本=", vr.Version)
	}
	// 版本拒绝: 记录版本= v1

	// 失败条件二：读回的标识已经过期 -> 覆盖报冲突，已有当前记录保持原样。
	if _, err := arch.Replace(slot, w, rec.ID); err != nil {
		var ce *world.ConflictError
		if !errors.As(err, &ce) {
			log.Fatal(err)
		}
		fmt.Println("冲突: 过期标识覆盖被拒绝")
	}
	unchanged, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printState("当前仍在", unchanged.State)
	// 冲突: 过期标识覆盖被拒绝
	// 当前仍在: time=7 hero@mine wood=3 ore=3

	// 正确后续：重新读取当前记录后，用新标识覆盖（这里再提交一次合法
	// 变化：mine->forest，ore -1，目标时间片 9）。
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
	printState("最终", final.State)
	// 重读后重试: 父记录为重读标识=true
	// 最终: time=9 hero@forest wood=3 ore=2
}
```

## 一次提交中多次增减同一种物品

一次 `Commit` 的 `ItemChanges` 里可以为**同一角色的同一种物品**列出
多条增减（例如先 `-2` 再 `+5`）。判断成败的依据不是逐条落数，而是
**原有数量与本次全部增减合并后的最终数量**：

- 合并使用精确整数运算：中途暂时为负、甚至暂时超出 `int` 范围都**不
  失败**；只有合并后的最终数量必须落在规则内——是 `int` 可表示的
  **非负**整数，超出即整次提交失败。
- 增减条目的**先后次序不影响**成败与最终数量：`1 - 2 + 5` 与
  `1 + 5 - 2` 得到同一个 4。因此“减少条目排在前面”不会产生一个可被
  读取的负数量世界——`Apply` 要么整体成功，要么让世界保持提交前状态，
  不存在只应用了前几条变化的中间世界。
- 携带上限按**本次变化全部完成后的真实总量**判断：等于上限允许，大于
  上限才拒绝。
- 引用**不存在的角色**或**规则不允许的物品**，即使其正负变化互相抵消
  （净变化为零），仍按条目拒绝，整次提交失败。

这也意味着**拆开提交与合并提交不等价**：把 `-2`、`+5` 拆成两次
`Apply`，第一次提交的最终数量就是 `1 - 2 = -1`，当即失败且世界不变；
事后的 `+5` 是另一次独立提交，无法把已失败的第一步补成一次成功提交。
需要“先扣后补”时，应把两条变化放进**同一个** `Commit`。

与其他提交一样，`Commit.Time` 是目标**绝对**时间片；`Apply` 成功只
改变内存世界，写回存档仍使用已有的保存入口（`Save`/`Replace` 等）。

### 完整示例

下面的程序可直接运行（可运行版本在
`world/example_multidelta_test.go`，`go test ./...` 会校验其输出）：
角色持有一件木材，规则允许木材、角色所在地点与另一地点之间有道路，
携带上限 4 恰好容纳最终的四件木材。先在同一 `Commit` 中提交
“减少两件、增加五件”并沿道路移动、把时间片从 0 推进到 2；再从相同
初始状态交换两条增减的次序，得到相同结果；最后只提交减少两件，展示
规则错误与失败后保持原样的世界。

```go
package main

import (
	"errors"
	"fmt"
	"log"

	"github.com/descikazuyq/world-rules/world"
)

func printState(tag string, s world.State) {
	c := s.Characters[0]
	fmt.Printf("%s: time=%d %s@%s", tag, s.Time, c.ID, c.Location)
	for _, it := range c.Items {
		fmt.Printf(" %s=%d", it.Item, it.Count)
	}
	fmt.Println()
}

// newWorld 建立示例世界：hero 在 village 持有一件木材，village 与
// forest 之间有道路，规则只允许 wood，hero 携带上限 4。每次调用都
// 返回相同初始状态的全新世界。
func newWorld() *world.World {
	w, err := world.NewWorld(world.InitialData{
		Seed: 20240801,
		Rules: world.Rules{
			Version:     "v1",
			Locations:   []string{"village", "forest"},
			Edges:       []world.Edge{{From: "village", To: "forest"}},
			ItemKinds:   []string{"wood"},
			CarryLimits: map[string]int{"hero": 4},
		},
		Characters: []world.Character{{
			ID:       "hero",
			Location: "village",
			Items:    []world.CharacterItem{{Item: "wood", Count: 1}},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	return w
}

func main() {
	// 1) 减少排在前：wood -2 再 +5。若逐条落数，中途会出现 -1 的负数量；
	//    实际判断先把全部增减与原数量合并：1 - 2 + 5 = 4，合法（4 等于
	//    上限，允许）。不会存在一个可被读取的负数量世界。
	w1 := newWorld()
	printState("初始", w1.Snapshot())
	if _, err := w1.Apply(world.Commit{
		Moves: []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{
			{Character: "hero", Item: "wood", Delta: -2},
			{Character: "hero", Item: "wood", Delta: 5},
		},
		Time: 2, // 目标绝对时间片，不是增加的步数
	}); err != nil {
		log.Fatal(err)
	}
	printState("减少在前(-2,+5)", w1.Snapshot())
	// 初始: time=0 hero@village wood=1
	// 减少在前(-2,+5): time=2 hero@forest wood=4

	// 2) 相同初始状态，交换两条增减的次序：合并结果同为 1 + 5 - 2 = 4，
	//    成功结果完全相同。
	w2 := newWorld()
	if _, err := w2.Apply(world.Commit{
		Moves: []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{
			{Character: "hero", Item: "wood", Delta: 5},
			{Character: "hero", Item: "wood", Delta: -2},
		},
		Time: 2,
	}); err != nil {
		log.Fatal(err)
	}
	printState("增加在前(+5,-2)", w2.Snapshot())
	// 增加在前(+5,-2): time=2 hero@forest wood=4

	// 3) 相同初始状态，只提交减少两件（移动与时间目标同上）：1 - 2 = -1，
	//    最终数量为负，整次提交失败并返回 *world.RuleError；位置、数量与
	//    时间片都保持提交前状态。这也正是把 -2/+5 拆成两次 Apply 时第一
	//    步的结果：第一步失败后，事后的 +5 无法把它补成一次成功提交。
	w3 := newWorld()
	_, err := w3.Apply(world.Commit{
		Moves:       []world.Move{{Character: "hero", To: "forest"}},
		ItemChanges: []world.ItemChange{{Character: "hero", Item: "wood", Delta: -2}},
		Time:        2,
	})
	var re *world.RuleError
	if !errors.As(err, &re) {
		log.Fatalf("应返回 *world.RuleError，得到 %v", err)
	}
	fmt.Printf("只减两件: 错误类型=%T\n", err)
	fmt.Println("只减两件:", err)
	printState("失败后世界", w3.Snapshot())
	// 只减两件: 错误类型=*world.RuleError
	// 只减两件: world: 角色 "hero" 的物品 "wood" 数量不能为负: -1
	// 失败后世界: time=0 hero@village wood=1

	// 4) 引用规则不允许的物品或不存在的角色，即使正负变化互相抵消，
	//    整次提交仍然失败。
	w4 := newWorld()
	if _, err := w4.Apply(world.Commit{
		ItemChanges: []world.ItemChange{
			{Character: "hero", Item: "ore", Delta: 3},
			{Character: "hero", Item: "ore", Delta: -3},
		},
	}); err != nil {
		fmt.Println("抵消的非法物品:", err)
	}
	if _, err := w4.Apply(world.Commit{
		ItemChanges: []world.ItemChange{
			{Character: "ghost", Item: "wood", Delta: 2},
			{Character: "ghost", Item: "wood", Delta: -2},
		},
	}); err != nil {
		fmt.Println("抵消的未知角色:", err)
	}
	// 抵消的非法物品: world: 角色 "hero" 的物品变化引用了规则不允许的物品: "ore"
	// 抵消的未知角色: world: 物品变化引用了不存在的角色: "ghost"
}
```

## 浏览一个槽已保存的历史

前面的读档说明都假设已经知道要读哪份记录（槽当前记录，或某个已知
标识）。若想先看看一个槽里**到底保存过哪些历史**，再挑一份旧记录读取
当时的世界，分两步：

1. **浏览**：`History(slot)` 返回该槽已保存历史的元信息列表
   （`[]RecordInfo`），每项含记录标识 `ID`、父标识 `Parent`、槽首标记
   `SlotFirst` 与规则版本 `Version`。它不需要可接受版本参数，也不含
   完整世界。
2. **选定后读取**：从列表中挑出目标记录，用
   `Record(slot, id, acceptedVersions)` 按其标识取回当时的完整世界
   （含种子、完整规则、非零时间片与角色物品）。

### 列表次序与父记录关系

- 历史按**保存生效的先后**排列，最近一次成功保存的记录在最前，槽首
  记录在末尾。它**不按世界时间片排序，也不按记录文件修改时间排序**。
- 确认恢复后，新记录可以指向较早的来源（允许时间片回到来源时刻）；
  这条新记录仍排在列表最前，而原先已经生效的其他保存按原次序保留。
- 父标识 `Parent` 表示“这条记录承接自哪一次保存”，与列表位置是两件
  事；正常链上每条较新记录的父就是紧挨着它、上一次生效的保存。
- **不要把“槽首记录”统一解释成父标识为空**：只有直接建立的槽首记录
  没有父记录（`Save` 建立的根记录）。分支槽的首条记录虽标记为槽首，
  却以来源槽的那条记录为父记录，因此 `SlotFirst == true` 时 `Parent`
  仍可能非空。
- **分支槽只列分支自身保存生效的记录**。即使分支首条记录的父标识指向
  来源槽记录，也不会把来源槽的历史补进列表；来源槽自己的历史也不受
  分支影响。

### 列表里的记录能否立即使用

`History` 只做完整性检查，**不筛选规则版本**：一份记录只要确实保存
生效、且通过与 `Record` 一致的完整性检查（记录格式受支持、内容校验和
与父关系正确、世界状态合法、直接父记录文件存在），即使规则版本较旧或
陌生，也照常列出，元信息里的 `Version` 与记录本身一致。

因此“出现在列表里”只说明记录完好，**不代表调用方立即可用**。用
`Record` 读取时必须显式给出可接受版本集合；记录版本不在集合内时得到
`*VersionRejectedError`（可用 `errors.As` 判断）。这是读取方的版本
取舍，不是历史列表出错。

损坏与缺失记录的处理：

- 带历史索引的槽中，单条**损坏、缺失或无法解析**的记录只被略过，其余
  合格记录继续列出；最新或某条中间记录出问题不会让整个浏览失败。
- 某条记录的**直接父文件被删除**时，它因父关系不成立也被略过；但父
  文件**仍在、只是内容损坏**时，不因此略过自身完好的子记录。
- 旧版本写出的**无历史索引槽**沿校验通过的父链在内存中重建，只列能
  确认归属本槽的记录，重建在损坏处停止，**不能承诺越过损坏处继续寻找
  更老的记录**。

三种“看不到记录”的结果互不相同，不能混为一谈：

- 槽存在、但没有任何合格记录：返回**成功的空列表**（非 nil、长度 0）；
- 槽不存在：返回 `*NotFoundError`；
- 槽指针无法解析，或顶层 `latest`/`history` 字段重复（含大小写变体与
  Unicode 转义写法）：返回带槽名的 `*CorruptError`，不返回历史列表。

`History` 与 `Record` 都是**只读**操作：浏览和读取都不切换槽当前记录、
不修补或删除记录、也不替换世界规则。已有的“读档后继续”（`Latest`/
`Record` + `WorldFromState` + `Replace`）与 `Branch` 分支用法保持
不变。

### 完整示例

下面的程序可直接运行（可运行版本在
`world/example_history_test.go`，`go test ./...` 会校验其输出）：自建
存档并产生三次保存（首存、覆盖、规则升级），浏览历史的标识、父标识、
槽首标记与规则版本，再选列表末尾的槽首旧记录读取当时的完整世界，对照
它与当前记录的区别，并演示旧版本记录“在列表中但读取需版本可接受”、
分支只列自身记录，以及整个过程不改变槽当前记录。

```go
package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/descikazuyq/world-rules/world"
)

func printState(tag string, s world.State) {
	c := s.Characters[0]
	fmt.Printf("%s: rules=%s time=%d %s@%s", tag, s.Rules.Version, s.Time, c.ID, c.Location)
	for _, it := range c.Items {
		fmt.Printf(" %s=%d", it.Item, it.Count)
	}
	fmt.Println()
}

func main() {
	const slot = "adventure"
	acceptedV1 := []string{"v1"}
	acceptedV1V2 := []string{"v1", "v2"}

	// v1：三个地点、两条无向道路、两种物品，hero 携带总量上限 10。
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
	// v2 与 v1 兼容（地点、道路、物品与上限不变，只改版本号）。
	rulesV2 := rulesV1
	rulesV2.Version = "v2"

	dir, err := os.MkdirTemp("", "world-history-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// 1) 产生三次保存：首存 -> 覆盖 -> 规则升级。
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
	r0, err := arch.Latest(slot, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	w, err := world.WorldFromState(r0.State)
	if err != nil {
		log.Fatal(err)
	}
	// 第二次保存：继续到时间片 7（forest->mine，ore +2）后覆盖回原槽。
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

	// 第三次保存：状态可被 v2 原样承接时升级规则，时间片与状态不变。
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

	// 2) 浏览历史：无需可接受版本，按保存生效次序，最近保存的在前。
	infos, err := arch.History(slot)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("历史条数: %d\n", len(infos))
	for i, info := range infos {
		fmt.Printf("历史[%d]: 版本=%s 槽首=%t 父标识为空=%t\n",
			i, info.Version, info.SlotFirst, info.Parent == "")
	}
	// 父记录关系与列表次序一致：每条较新记录的父就是上一次生效的保存。
	fmt.Printf("父记录关系: 第0条父=第1条=%t 第1条父=第2条=%t\n",
		infos[0].Parent == infos[1].ID, infos[1].Parent == infos[2].ID)
	// 本槽由 Save 直接建立，故末条槽首记录没有父记录。
	fmt.Printf("槽首即直接建立: 末条槽首=%t 末条父为空=%t\n",
		infos[len(infos)-1].SlotFirst, infos[len(infos)-1].Parent == "")
	// 历史条数: 3
	// 历史[0]: 版本=v2 槽首=false 父标识为空=false
	// 历史[1]: 版本=v1 槽首=false 父标识为空=false
	// 历史[2]: 版本=v1 槽首=true 父标识为空=true
	// 父记录关系: 第0条父=第1条=true 第1条父=第2条=true
	// 槽首即直接建立: 末条槽首=true 末条父为空=true

	// 3) 选列表末尾的槽首旧记录，用 Record 显式接受版本后读取完整世界。
	//    标识直接取自列表，无需手工填写，也不依赖预置数据。
	chosen := infos[len(infos)-1]
	old, err := arch.Record(slot, chosen.ID, acceptedV1)
	if err != nil {
		log.Fatal(err)
	}
	printState("选中的旧记录", old.State)
	fmt.Printf("选中记录: 与列表标识一致=%t 槽首=%t\n", old.ID == chosen.ID, old.SlotFirst)
	// 选中的旧记录: rules=v1 time=4 hero@forest wood=3 ore=1
	// 选中记录: 与列表标识一致=true 槽首=true

	// 当前记录仍是升级后的 v2：浏览和按标识读取都不切换槽当前记录。
	cur, err := arch.Latest(slot, acceptedV1V2)
	if err != nil {
		log.Fatal(err)
	}
	printState("当前记录", cur.State)
	fmt.Printf("读取旧记录不切换当前: 当前仍是最新一条=%t\n", cur.ID == infos[0].ID)
	// 当前记录: rules=v2 time=7 hero@mine wood=3 ore=3
	// 读取旧记录不切换当前: 当前仍是最新一条=true

	// 4) History 不筛选版本（v2、v1 记录都在列表中）；但 Record 读取必须
	//    显式接受版本。用只接受 v1 的集合读 v2 最新记录 -> 版本拒绝，这不
	//    是历史列表出错。
	if _, err := arch.Record(slot, infos[0].ID, acceptedV1); err != nil {
		var vr *world.VersionRejectedError
		if !errors.As(err, &vr) {
			log.Fatal(err)
		}
		fmt.Printf("版本拒绝: 记录版本=%s 未在可接受集合内=%t\n", vr.Version, true)
	}
	// 版本拒绝: 记录版本=v2 未在可接受集合内=true

	// 5) 从选中的槽首旧记录分出新槽：分支首条记录以来源记录为父，但它是
	//    新槽的槽首记录；分支历史只列分支自身保存的记录。
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
	// 来源槽自己的历史不受分支影响，仍是三条且次序不变。
	mainHist, err := arch.History(slot)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("来源槽历史不变: 条数=%d 次序不变=%t\n",
		len(mainHist), mainHist[0].ID == infos[0].ID && mainHist[2].ID == infos[2].ID)
	// 分支历史条数: 2（不含来源槽历史）
	// 分支历史[0]: 版本=v1 槽首=false 父标识为空=false
	// 分支历史[1]: 版本=v1 槽首=true 父标识为空=false
	// 分支首条: 槽首=true 父为来源记录=true 父非空=true
	// 分支只列自身记录: true
	// 来源槽历史不变: 条数=3 次序不变=true

	// 6) 槽不存在报 *NotFoundError，不能当成成功的空列表。
	if _, err := arch.History("no-such-slot"); err != nil {
		fmt.Printf("槽不存在: %t\n", errors.As(err, new(*world.NotFoundError)))
	}
	// 槽不存在: true

	// 7) 浏览与读取结束后，槽当前记录及其世界仍是开始浏览时的样子：
	//    不切换当前、不修补记录、不替换规则。
	after, err := arch.Latest(slot, acceptedV1V2)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("浏览后当前未变: 标识相同=%t\n", after.ID == cur.ID)
	printState("浏览后当前", after.State)
	// 浏览后当前未变: 标识相同=true
	// 浏览后当前: rules=v2 time=7 hero@mine wood=3 ore=3
}
```

## 把旧存档中的两种物品合并为新种类

`CheckUpgrade`/`Upgrade` 只做**规则版本切换**：状态原样承接，不替使用
者改变任何物品。当新规则**删除了旧物品种类、又引入了合并后的新种类**
时（旧木材、旧树枝都改成木料，石头保留），直接升级只会得到“旧物品不
被允许”的阻碍；这正是 `PreviewMigration`/`Migrate` 显式迁移的用途：
调用方给出旧名称到新名称的**对应关系**，转换在保存前显式发生。

从一份**已保存记录**开始，流程是：**预览 → 使用者确认 → 带来源标识
正式写回原槽**。

- **预览**（`PreviewMigration(slot, acceptedVersions, targetRules,
  locationMappings, itemMappings)`）：读取槽当前最新记录，按对应关系
  计算转换后的完整状态，返回来源记录标识 `RecordID`、旧新版本、转换
  结果、是否可提交 `Committable` 与全部阻碍 `Blockers`。预览**只读**，
  不改变槽指向、历史或任何记录文件。
- **正式迁移**（`Migrate(slot, acceptedVersions, targetRules,
  locationMappings, itemMappings, sourceRecordID)`）：仍需重新传入
  **目标规则、对应关系和来源标识**；排他锁下重新读取来源、重新计算
  转换结果，绝不使用调用方可能改过的预览状态。成功后产生一条**以来源
  为父的独立新记录**，槽指向新记录，旧记录原样保留。

### 对应关系与合并条目的位置

- 转换只作用于**原始名称一次**，不沿对应关系连续替换；**未指定的名称
  保持原样**（石头不在对应关系里，迁移后仍是石头）。
- 多个旧条目转换成同一种物品时**合并数量**；合并后的条目保留在原列表
  中该名称**首次出现的位置**，只留一个条目。原列表为
  `old_wood(2), stone(1), stick(3)`，旧木材与旧树枝都对应到木料时，
  结果是 `lumber(5), stone(1)`——木料占旧木材的首位，石头次序不变。
- 种子、时间片、角色标识与角色位置、角色排列都保持原样。

### 可接受版本集合与目标版本各管什么

二者是两个独立参数：

- **可接受版本集合**是**读取来源**时的版本取舍：迁移读取的是槽里的
  v1 记录，所以集合里放 `v1`；迁移后读回的是 v2 新记录，集合就要改
  放 `v2`（否则得到 `*VersionRejectedError`）。
- **目标规则**描述迁移后的世界规则（版本 `v2`、地点、物品种类、携带
  上限等），它不替你接受来源版本，也不被可接受集合改变。

### 有阻碍时：预览能看，提交被拒

转换后若角色所在地点缺失、持有新规则不允许的物品，或携带总量超过目标
上限，预览**仍正常返回**转换结果，只是 `Committable == false`，并一次
列出全部阻碍；这时调用 `Migrate` 会得到 `*RuleError`，返回值里附带同
一份预览与阻碍，**槽当前记录和历史保持原样**，没有新记录产生。预览
状态的规则已显示为 v2，**不代表原槽已切换版本**——只有成功的
`Migrate` 才会写入 v2 记录。调整目标规则（如放宽携带上限）后重新预览，
用**此次预览**给出的来源标识迁移即可。

### 完整示例

下面的程序可直接运行（可运行版本在
`world/example_migrate_test.go`，`go test ./...` 会校验其输出）：准备
一个规则 v1、时间片非零的命名槽，同一角色依次持有旧木材 2 件、石头
1 件、旧树枝 3 件；v2 删除两种旧物品、只允许木料与石头。先把目标上限
设为 5，展示“预览正常但不可提交、迁移被规则错误拒绝且存档不变”，再
把上限调整为 6 完成迁移并读回核对。数量、阻碍与记录关系都由实际返回
值打印；记录标识是随机生成的，示例只以“独立、为父子关系”等关系比较
表达，不写死标识字面值。

```go
package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/descikazuyq/world-rules/world"
)

func printState(tag string, s world.State) {
	c := s.Characters[0]
	fmt.Printf("%s: seed=%d rules=%s time=%d %s@%s", tag, s.Seed, s.Rules.Version, s.Time, c.ID, c.Location)
	for _, it := range c.Items {
		fmt.Printf(" %s=%d", it.Item, it.Count)
	}
	fmt.Println()
}

func main() {
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
	printState("准备", src.State)

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
	printState("预览(上限5)", pv.State)
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
	printState("预览后原槽", cur.State)

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
	printState("拒绝后原槽", still.State)
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
	printState("迁移后当前", migrated.State)
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
	printState("旧记录按v1读取", old.State)

	// 可接受版本集合是读取方的显式取舍：新记录是 v2，只接受 v1 时
	// 读当前记录会被版本拒绝，必须把 v2 放进集合（正如迁移时放入 v1）。
	if _, err := arch.Latest(slot, acceptedV1); err != nil {
		var vr *world.VersionRejectedError
		if !errors.As(err, &vr) {
			log.Fatal(err)
		}
		fmt.Printf("新记录按v1读取: 版本拒绝 记录版本=%s\n", vr.Version)
	}
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
```

