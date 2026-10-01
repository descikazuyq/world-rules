package world

import (
	"fmt"
	"os"
	"syscall"
)

// fileLock 是基于 flock 的跨进程互斥锁。同一目录上的多个进程或
// 多个实例在同一把锁上排队，保证同一份存档的写入被串行化。
type fileLock struct {
	f *os.File
}

func acquireLock(dir string) (*fileLock, error) {
	path := dir + "/lock"
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("无法打开存档锁: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("无法获取存档锁: %w", err)
	}
	return &fileLock{f: f}, nil
}

func (l *fileLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	return l.f.Close()
}
