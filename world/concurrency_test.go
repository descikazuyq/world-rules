package world

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// TestConcurrentReplaceAcrossProcesses 启动多个进程同时用同一个父记录
// 标识覆盖同一槽，要求恰好一个成功，其余全部收到冲突。
func TestConcurrentReplaceAcrossProcesses(t *testing.T) {
	if os.Getenv("WORLD_TEST_HELPER") != "" {
		t.Skip("子进程模式")
	}
	a, dir := newTestArchive(t)
	w := baseWorld(t)
	info, err := a.Save("s", w)
	if err != nil {
		t.Fatal(err)
	}

	const n = 8
	cmds := make([]*exec.Cmd, n)
	for i := range cmds {
		c := exec.Command(os.Args[0], "-test.run=TestMain", "-test.v")
		c.Env = append(os.Environ(),
			"WORLD_TEST_HELPER=concurrent_replace",
			"WORLD_TEST_DIR="+dir,
			"WORLD_TEST_PARENT="+string(info.ID),
		)
		cmds[i] = c
	}

	wins, conflicts := 0, 0
	for _, c := range cmds {
		err := c.Run()
		switch {
		case err == nil:
			wins++
		case c.ProcessState.ExitCode() == 3:
			conflicts++
		default:
			t.Fatalf("子进程意外失败: %v", err)
		}
	}
	if wins != 1 || conflicts != n-1 {
		t.Fatalf("期望恰好 1 成功 %d 冲突，得到 %d 成功 %d 冲突", n-1, wins, conflicts)
	}

	// 最终槽只能指向一个记录，且该记录完整可读、父记录为首存记录。
	a2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a2.Latest("s", []string{"v1"})
	if err != nil {
		t.Fatalf("并发后最新记录应完好: %v", err)
	}
	if rec.Parent != info.ID {
		t.Fatalf("胜出记录的父应为 %s，得到 %s", info.ID, rec.Parent)
	}
	hist, err := a2.History("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("并发覆盖只应留下一条新记录，历史长度应为 2，得到 %d", len(hist))
	}
}

// helperConcurrentReplace 在子进程中执行一次基于固定父记录的覆盖。
// 成功退出码 0；冲突退出码 3；其他错误退出码 1。
func helperConcurrentReplace() {
	dir := os.Getenv("WORLD_TEST_DIR")
	parent := RecordID(os.Getenv("WORLD_TEST_PARENT"))

	a, err := Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	rec, err := a.Latest("s", []string{"v1"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	w, err := WorldFromState(rec.State)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := a.Replace("s", w, parent); err != nil {
		var ce *ConflictError
		if errors.As(err, &ce) {
			os.Exit(3)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
