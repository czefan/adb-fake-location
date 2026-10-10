//go:build linux || darwin

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

func AcquireInstanceLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("已有工具实例运行: %w", err)
	}
	var once sync.Once
	return func() { once.Do(func() { _ = f.Close() }) }, nil
}

func HideCommandWindow(cmd *exec.Cmd) {}
