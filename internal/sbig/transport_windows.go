//go:build windows

package sbig

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

type windowsTransport struct {
	dll  *syscall.DLL
	proc *syscall.Proc
	info map[string]any
}

var kernel = syscall.NewLazyDLL("kernel32.dll") // Windows KnownDLL, never an application directory DLL.

func systemDirectory() (string, error) {
	b := make([]uint16, 32768)
	n, _, e := kernel.NewProc("GetSystemDirectoryW").Call(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	if n == 0 || n >= uintptr(len(b)) {
		return "", fmt.Errorf("GetSystemDirectoryW: %w", e)
	}
	return syscall.UTF16ToString(b[:n]), nil
}

// LoadLibraryEx uses only the requested DLL's directory and Windows' registered
// default directories for dependencies; neither cwd nor PATH participates.
func loadDLLAbsolute(path string) (*syscall.DLL, error) {
	p, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	h, _, e := kernel.NewProc("LoadLibraryExW").Call(uintptr(unsafe.Pointer(p)), 0, 0x100|0x1000)
	if h == 0 {
		return nil, e
	}
	return &syscall.DLL{Name: path, Handle: syscall.Handle(h)}, nil
}

func loadTransport(requested string) (transport, error) {
	system, err := systemDirectory()
	if err != nil {
		return nil, err
	}
	var candidates []string
	if requested != "" {
		if !filepath.IsAbs(requested) {
			return nil, errors.New("--dll must be an absolute DLL path")
		}
		candidates = []string{filepath.Clean(requested)}
	} else {
		exe, e := os.Executable()
		if e != nil {
			return nil, e
		}
		candidates = []string{filepath.Join(filepath.Dir(exe), "sbigudrv.dll"), filepath.Join(system, "sbigudrv.dll")}
	}
	var attempts []error
	for _, path := range candidates {
		dll, e := loadDLLAbsolute(path)
		if e != nil {
			hint := ""
			if errors.Is(e, syscall.Errno(193)) {
				hint = " (DLL/executable architecture mismatch: use the matching x86 or x64 build)"
			}
			attempts = append(attempts, fmt.Errorf("load %s: %w%s", path, e, hint))
			continue
		}
		proc, e := dll.FindProc("SBIGUnivDrvCommand")
		if e != nil && runtime.GOARCH == "386" {
			proc, e = dll.FindProc("_SBIGUnivDrvCommand@12")
		}
		if e != nil {
			_ = dll.Release()
			attempts = append(attempts, fmt.Errorf("SBIGUnivDrvCommand in %s: %w", path, e))
			continue
		}
		actual := path
		b := make([]uint16, 32768)
		if n, _, _ := kernel.NewProc("GetModuleFileNameW").Call(uintptr(dll.Handle), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b))); n > 0 && n < uintptr(len(b)) {
			actual = syscall.UTF16ToString(b[:n])
		}
		info := map[string]any{"path": actual, "requested_path": requested, "process_architecture": runtime.GOARCH, "file_version": fileVersion(actual, system)}
		if f, e := os.Open(actual); e == nil {
			h := sha256.New()
			_, e = io.Copy(h, f)
			_ = f.Close()
			if e == nil {
				info["sha256"] = hex.EncodeToString(h.Sum(nil))
			} else {
				info["sha256_error"] = e.Error()
			}
		} else {
			info["sha256_error"] = e.Error()
		}
		if len(attempts) > 0 {
			info["earlier_load_attempts"] = fmt.Sprint(errors.Join(attempts...))
		}
		return &windowsTransport{dll: dll, proc: proc, info: info}, nil
	}
	return nil, fmt.Errorf("could not load the SBIG Universal Driver; install SBIG Driver Checker, or supply an absolute --dll path matching this %s executable: %w", runtime.GOARCH, errors.Join(attempts...))
}

func bytePointer(b []byte) uintptr {
	if len(b) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&b[0]))
}
func (t *windowsTransport) call(command uint16, p, r []byte) int16 {
	// The SDK returns a signed 16-bit status. GetLastError is unrelated to its
	// status, so the third return from Proc.Call is intentionally ignored.
	status, _, _ := t.proc.Call(uintptr(command), bytePointer(p), bytePointer(r))
	runtime.KeepAlive(p)
	runtime.KeepAlive(r)
	return int16(status)
}
func (t *windowsTransport) close() error             { return t.dll.Release() }
func (t *windowsTransport) metadata() map[string]any { return t.info }

func fileVersion(path, system string) string {
	dll, e := loadDLLAbsolute(filepath.Join(system, "version.dll"))
	if e != nil {
		return "unavailable: " + e.Error()
	}
	defer dll.Release()
	getSize, e := dll.FindProc("GetFileVersionInfoSizeW")
	if e != nil {
		return "unavailable"
	}
	getInfo, e := dll.FindProc("GetFileVersionInfoW")
	if e != nil {
		return "unavailable"
	}
	query, e := dll.FindProc("VerQueryValueW")
	if e != nil {
		return "unavailable"
	}
	p, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return "unavailable"
	}
	var ignored uint32
	n, _, _ := getSize.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&ignored)))
	if n == 0 || n > 16*1024*1024 {
		return "unavailable"
	}
	b := make([]byte, n)
	ok, _, _ := getInfo.Call(uintptr(unsafe.Pointer(p)), 0, n, bytePointer(b))
	if ok == 0 {
		return "unavailable"
	}
	root, _ := syscall.UTF16PtrFromString("\\")
	var value unsafe.Pointer
	var length uint32
	ok, _, _ = query.Call(bytePointer(b), uintptr(unsafe.Pointer(root)), uintptr(unsafe.Pointer(&value)), uintptr(unsafe.Pointer(&length)))
	if ok == 0 || length < 16 || value == nil {
		return "unavailable"
	}
	// VS_FIXEDFILEINFO begins with signature, structure version, FileVersionMS,
	// FileVersionLS; read the fixed byte layout independently of Go alignment.
	data := unsafe.Slice((*byte)(value), 16)
	if u32(data, 0) != 0xFEEF04BD {
		return "unavailable"
	}
	ms, ls := u32(data, 8), u32(data, 12)
	version := fmt.Sprintf("%d.%d.%d.%d", ms>>16, ms&65535, ls>>16, ls&65535)
	runtime.KeepAlive(b)
	return version
}
