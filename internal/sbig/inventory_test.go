package sbig

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInspectDLLIdentifiesPEWithoutLoading(t *testing.T) {
	for _, tc := range []struct {
		machine uint16
		arch    string
	}{{0x14c, "386"}, {0x8664, "amd64"}} {
		// A minimal PE/COFF header has no executable content or exported entry
		// point. Inventory must inspect bytes, never try to load or execute it.
		b := make([]byte, 128)
		copy(b, "MZ")
		binary.LittleEndian.PutUint32(b[0x3c:], 64)
		copy(b[64:], "PE\x00\x00")
		binary.LittleEndian.PutUint16(b[68:], tc.machine)
		path := filepath.Join(t.TempDir(), "test.dll")
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		d := inspectDLL(path, "test")
		if !d.Exists || d.Architecture != tc.arch || d.MatchesProcess != (tc.arch == runtime.GOARCH) || len(d.Errors) != 0 || d.SHA256 != fmt.Sprintf("%x", sha256.Sum256(b)) {
			t.Fatalf("inventory: %+v", d)
		}
	}
}

func TestInspectDLLRecordsMissingAndInvalidFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.dll")
	if d := inspectDLL(path, "test"); d.Exists || len(d.Errors) == 0 {
		t.Fatal(d)
	}
	if err := os.WriteFile(path, []byte("invalid PE"), 0600); err != nil {
		t.Fatal(err)
	}
	if d := inspectDLL(path, "test"); !d.Exists || len(d.Errors) == 0 || d.Architecture != "" || d.SHA256 == "" {
		t.Fatal(d)
	}
}
