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

## 读档后继续操作同一存档槽

继续操作分三步，各自作用不同：**读取**（`Latest` / `Record`）只读出
记录，不改写存档；**内存提交**（`World.Apply`）只改变内存中的世界；
**覆盖保存**（`Replace`）才把内存状态写回存档槽。

1. 打开存档后调用 `Latest(slot, acceptedVersions)`，显式给出可接受的
   规则版本集合，读出当前记录。记录完整保留保存时的种子、完整规则、
   角色与物品内容以及时间片。
2. 用 `WorldFromState(record.State)` 承接记录中的完整状态重建世界，
   其中的时间片原样保留。`NewWorld` 建立的是时间片从零开始的全新
   世界，只接受 `InitialData`（不含时间片），不能用来承接存档状态。
3. 在重建出的世界上用 `Apply` 提交新动作。`Commit.Time` 是提交后要
   到达的**目标绝对时间片**，不是增加的步数，且不能早于读回的时间片。
   `Apply` 成功后只有内存世界改变，存档仍保持此前内容。
4. 用 `Replace(slot, world, expectedRecordID)` 覆盖保存到原槽，
   `expectedRecordID` 必须是本次读取得到的记录标识。覆盖成功会得到
   一个独立的新记录标识，其父记录是被覆盖的原记录；此后 `Latest`
   能看到更新后的状态，原记录仍可按标识用 `Record` 读取。

两个直接影响这次操作的失败条件：

- 记录的规则版本不在可接受集合内时，读取返回 `*VersionRejectedError`。
  记录本身完好，但不能把它当成新世界继续——应调整可接受版本，或改走
  升级（`CheckUpgrade` / `Upgrade`）、迁移（`PreviewMigration` /
  `Migrate`）流程。
- 读到的标识已经过期（槽在此期间被其他写入覆盖）时，`Replace` 返回
  `*ConflictError`，已有当前记录保持原样。调用方需要重新读取当前
  记录，再决定是重试还是放弃。

下面是一个完整可运行的示例（与 `world/example_continue_test.go`
相同，`go test` 会校验其输出）：

```go
package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	world "github.com/descikazuyq/world-rules/world"
)

func main() {
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
}
```

输出：

```text
读回: 是首次保存的记录=true 种子=42 版本=v1 时间片=5
读回: 阿黎在集市 面包=2
Apply 后存档仍是: 时间片=5 阿黎在集市
覆盖: 新标识不同于原记录=true 父记录是原记录=true
覆盖后当前: 时间片=9 阿黎在港口 面包=2 药水=2
原记录仍可读: 时间片=5 阿黎在集市
版本拒绝: true (记录版本=v1)
标识过期: true
冲突后当前记录未变: 时间片=9 阿黎在港口
```
