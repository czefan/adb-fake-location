//go:build windows

package platform

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
)

func AcquireInstanceLock(path string) (func(), error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("无法取得会话锁，请确认没有另一个修订版实例运行: %w", err)
	}
	var once sync.Once
	return func() { once.Do(func() { _ = syscall.CloseHandle(handle) }) }, nil
}

func HideCommandWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
