//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// Prevent selecting console text from pausing acquisition in classic conhost.
func setupConsole() {
	dll := syscall.NewLazyDLL("kernel32.dll")
	h, _, _ := dll.NewProc("GetStdHandle").Call(uintptr(0xfffffff6))
	var mode uint32
	ok, _, _ := dll.NewProc("GetConsoleMode").Call(h, uintptr(unsafe.Pointer(&mode)))
	if ok != 0 {
		dll.NewProc("SetConsoleMode").Call(h, uintptr((mode|0x80)&^0x40))
	}
}
