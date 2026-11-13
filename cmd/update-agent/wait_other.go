//go:build !windows

package main

import (
	"fmt"
	"time"
)

func waitProcess(pid int, _ time.Duration) error {
	if pid != 0 {
		return fmt.Errorf("--wait-pid is currently supported only on Windows")
	}
	return nil
}
