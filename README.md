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
  项违规整次提交失败，世界保持提交前状态。
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
  覆盖或升级后旧历史即获得同样的恢复能力。读取不改写任何数据。
- `PreviewRecovery(slot, acceptedVersions)` 在不改变存档的前提下返回槽
  当前指向的记录标识、选中的来源记录标识及其完整世界状态；选择规则与
  `RecoverLatest` 相同，当前记录完好且版本可接受时可以选它本身。槽不
  存在报不存在、指针无法解析报损坏、找不到可用记录返回
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

