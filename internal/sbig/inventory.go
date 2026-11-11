package sbig

import (
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
)

// DLLInfo describes a file without loading or executing it. Paths are those
// visible to this process; Windows may redirect System32 for a 32-bit process.
type DLLInfo struct {
	Path           string   `json:"path"`
	Source         string   `json:"source"`
	Exists         bool     `json:"exists"`
	Architecture   string   `json:"architecture,omitempty"`
	MatchesProcess bool     `json:"matches_process_architecture"`
	Version        string   `json:"file_version,omitempty"`
	SHA256         string   `json:"sha256,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

func inspectDLL(path, source string) DLLInfo {
	d := DLLInfo{Path: path, Source: source}
	f, err := os.Open(path)
	if err != nil {
		d.Errors = append(d.Errors, err.Error())
		return d
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		d.Errors = append(d.Errors, err.Error())
		return d
	}
	if !st.Mode().IsRegular() {
		d.Errors = append(d.Errors, "not a regular file")
		return d
	}
	d.Exists = true
	// Bound work on a malformed candidate rather than hashing arbitrary data.
	if st.Size() > 256*1024*1024 {
		d.Errors = append(d.Errors, "DLL exceeds 256 MiB inspection limit")
		return d
	}
	p, err := pe.NewFile(f)
	if err != nil {
		d.Errors = append(d.Errors, "PE header: "+err.Error())
	} else {
		switch p.Machine {
		case pe.IMAGE_FILE_MACHINE_I386:
			d.Architecture = "386"
		case pe.IMAGE_FILE_MACHINE_AMD64:
			d.Architecture = "amd64"
		case pe.IMAGE_FILE_MACHINE_ARM64:
			d.Architecture = "arm64"
		default:
			d.Architecture = fmt.Sprintf("PE machine 0x%04x", p.Machine)
		}
		d.MatchesProcess = d.Architecture == runtime.GOARCH
		// NewFile does not own f, so Close does not close the underlying reader.
		_ = p.Close()
	}
	if _, err = f.Seek(0, io.SeekStart); err == nil {
		h := sha256.New()
		_, err = io.Copy(h, io.LimitReader(f, 256*1024*1024+1))
		if err == nil {
			d.SHA256 = hex.EncodeToString(h.Sum(nil))
		}
	}
	if err != nil {
		d.Errors = append(d.Errors, "SHA-256: "+err.Error())
	}
	return d
}
