package world

import (
	"errors"
	"fmt"
)

// ConflictError 表示保存与存档当前状态冲突：槽重名、覆盖所依据的
// 记录已不是该槽最新记录等。调用方应当重新读取后再决定是否重试。
type ConflictError struct {
	Slot   string
	Reason string
}

func (e *ConflictError) Error() string {
	if e.Slot == "" {
		return "world: 存档冲突: " + e.Reason
	}
	return fmt.Sprintf("world: 存档槽 %q 冲突: %s", e.Slot, e.Reason)
}

// NotFoundError 表示指定的槽或记录不存在。
type NotFoundError struct {
	Slot   string
	Record RecordID
}

func (e *NotFoundError) Error() string {
	switch {
	case e.Record != "":
		return fmt.Sprintf("world: 记录不存在: %s", e.Record)
	case e.Slot != "":
		return fmt.Sprintf("world: 存档槽不存在: %q", e.Slot)
	default:
		return "world: 槽或记录不存在"
	}
}

// CorruptError 表示记录内容损坏：校验和不符、父记录链断裂、数据无法
// 解析、世界数据不自洽，或世界时间片为负等。
type CorruptError struct {
	Slot   string
	Record RecordID
	Reason string
}

func (e *CorruptError) Error() string {
	where := e.Slot
	if e.Record != "" {
		if where != "" {
			where += "/"
		}
		where += string(e.Record)
	}
	if where == "" {
		where = "?"
	}
	return fmt.Sprintf("world: 记录损坏(%s): %s", where, e.Reason)
}

// VersionRejectedError 表示记录自身完好，但其规则版本不在调用方
// 明确给出的可接受版本集合内。
type VersionRejectedError struct {
	Slot     string
	Record   RecordID
	Version  string
	Accepted []string
}

func (e *VersionRejectedError) Error() string {
	return fmt.Sprintf("world: 记录 %s 的规则版本 %q 不在可接受版本集合 %v 内",
		e.Record, e.Version, e.Accepted)
}

// ErrUnrecoverable 表示槽中找不到任何一份校验通过且版本可接受的
// 历史记录，无法恢复。调用方可用 errors.Is 判断。
var ErrUnrecoverable = errors.New("world: 没有可恢复的历史记录")

// UnrecoverableError 包装 ErrUnrecoverable 并给出具体原因。
type UnrecoverableError struct {
	Slot   string
	Reason string
}

func (e *UnrecoverableError) Error() string {
	return fmt.Sprintf("world: 槽 %q 不可恢复: %s", e.Slot, e.Reason)
}

func (e *UnrecoverableError) Unwrap() error { return ErrUnrecoverable }
