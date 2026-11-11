//go:build windows

package main

import (
	"fmt"
	"syscall"
)

// Both acquisition and release must run on the same locked OS thread. Preserve
// that thread's previous requirements, without forcing the display to stay on.
func preventIdleSleep() (func() error, error) {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")
	previous, _, err := proc.Call(0x80000001) // ES_CONTINUOUS | ES_SYSTEM_REQUIRED
	if previous == 0 {
		return nil, fmt.Errorf("SetThreadExecutionState: %v", err)
	}
	return func() error {
		if ok, _, e := proc.Call(previous | 0x80000000); ok == 0 {
			return fmt.Errorf("restore Windows idle-sleep state: %v", e)
		}
		return nil
	}, nil
}
