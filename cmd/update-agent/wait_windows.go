package main

import (
	"fmt"
	"syscall"
	"time"
)

// OpenProcess + WaitForSingleObject waits on the existing process handle, not
// a name or a shell command. It never kills another process or requests admin.
func waitProcess(pid int, timeout time.Duration) error {
	if pid == 0 {
		return nil
	}
	h, e := syscall.OpenProcess(0x00100000, false, uint32(pid))
	if e == syscall.Errno(87) {
		return nil
	}
	if e != nil {
		return e
	}
	defer syscall.CloseHandle(h)
	result, e := syscall.WaitForSingleObject(h, uint32(timeout/time.Millisecond))
	if e != nil {
		return e
	}
	if result != 0 {
		return fmt.Errorf("main application has not exited; update cancelled")
	}
	return nil
}
