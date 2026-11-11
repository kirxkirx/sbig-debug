//go:build windows

package sbig

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

func windowsDirectory() (string, error) {
	b := make([]uint16, 32768)
	n, _, e := kernel.NewProc("GetWindowsDirectoryW").Call(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	if n == 0 || n >= uintptr(len(b)) {
		return "", fmt.Errorf("GetWindowsDirectoryW: %w", e)
	}
	return syscall.UTF16ToString(b[:n]), nil
}

// Inventory includes default and explicitly requested paths even when missing.
// Application copies are inspected only; loading one requires an explicit DLL.
func Inventory(requested string) []DLLInfo {
	var list []DLLInfo
	seen := map[string]bool{}
	system, systemErr := systemDirectory()
	add := func(path, source string) {
		path = filepath.Clean(path)
		key := strings.ToLower(path)
		if seen[key] {
			return
		}
		seen[key] = true
		d := inspectDLL(path, source)
		if d.Exists && d.Architecture != "" && systemErr == nil {
			d.Version = fileVersion(path, system)
		}
		list = append(list, d)
	}
	if requested != "" {
		if filepath.IsAbs(requested) {
			add(requested, "explicit")
		} else {
			list = append(list, DLLInfo{Path: requested, Source: "explicit", Errors: []string{"--dll must be an absolute DLL path"}})
		}
	}
	if exe, err := os.Executable(); err == nil {
		add(filepath.Join(filepath.Dir(exe), "sbigudrv.dll"), "beside executable")
	}
	if systemErr == nil {
		add(filepath.Join(system, "sbigudrv.dll"), "process system directory")
	} else {
		list = append(list, DLLInfo{Source: "process system directory", Errors: []string{systemErr.Error()}})
	}
	if win, err := windowsDirectory(); err == nil {
		add(filepath.Join(win, "System", "sbigudrv.dll"), "legacy Windows System")
		add(filepath.Join(win, "SysWOW64", "sbigudrv.dll"), "32-bit system directory")
		if runtime.GOARCH == "386" {
			add(filepath.Join(win, "Sysnative", "sbigudrv.dll"), "64-bit system directory via Sysnative")
		}
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		root := os.Getenv(env)
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		for _, pattern := range []string{
			`N.I.N.A*\*SBIG*.dll`, `N.I.N.A*\*\*SBIG*.dll`,
			`N.I.N.A*\*\*\*SBIG*.dll`, `N.I.N.A*\*\*\*\*SBIG*.dll`,
			`SBIG\SBIGUDrv.dll`, `SBIG\*\SBIGUDrv.dll`, `SBIG\*\*\SBIGUDrv.dll`,
			`Diffraction Limited\*\SBIGUDrv.dll`, `Diffraction Limited\*\*\SBIGUDrv.dll`,
			`Common Files\ASCOM\Camera\*SBIG*\SBIGUDrv.dll`,
		} {
			paths, _ := filepath.Glob(filepath.Join(root, pattern))
			for _, path := range paths {
				add(path, "installed application; select with --dll")
			}
		}
	}
	return list
}
