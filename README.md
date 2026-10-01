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
- `World.Apply(Commit)` 原子提交移动、物品数量变化与时间推进。任一
  项违规整次提交失败，世界保持提交前状态。
- `Create(dir)` / `Open(dir)` 管理存档目录。
- `Save(slot, world)` 首次保存创建命名槽，重名拒绝。
- `Replace(slot, world, expectedRecordID)` 乐观锁覆盖，父记录为被
  覆盖的记录；并发覆盖同一父记录只有一个成功。
- `Branch(srcSlot, recordID, dstSlot)` 从历史记录分出新槽，完整复制
  当时的种子、规则与状态，两槽此后互不影响。
- `CheckUpgrade(slot, accepted, targetRules)` 拿完整目标规则判断某槽
  最新存档能否继续使用，返回记录标识、旧/目标版本、是否兼容及全部
  有序阻碍；检查只读，不改写任何数据。
- `UpgradeRules(slot, accepted, targetRules, expectedRecordID)` 以
  乐观锁执行规则升级：追加一条以当前最新记录为父的新记录，只替换
  规则，种子、时间片、角色位置及物品数量和排列保持原样；有阻碍、
  标识过期或其他前提不满足时拒绝写入。
- `Latest` / `Record` / `RecoverLatest` 读取时校验内容校验和（含父
  记录关系）与调用方给定的可接受规则版本集合；`RecoverLatest` 在最新
  记录损坏或版本不被接受时回溯最近一份可用历史记录，没有则返回
  `ErrUnrecoverable`。读取不改写任何数据。
- 写入通过目录内 flock 串行化，并以“临时文件写全 + 原子改名/硬链接”
  落盘，崩溃重开后只能看到旧的或新的完整记录。
