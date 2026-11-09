package diagnostic

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeDistinguishesSaturationFromBoundary(t *testing.T) {
	uniform := make([]uint16, 24)
	for i := range uniform {
		uniform[i] = 65535
	}
	s := Analyze(uniform, 6, 4)
	if !s.Valid || s.Median != 65535 || s.SaturatedFraction != 1 || s.StdDev != 0 || s.BoundaryRow != -1 || s.IdenticalAdjacentRows != 3 {
		t.Fatalf("unexpected uniform stats: %+v", withoutHistogram(s))
	}
	striped := make([]uint16, 24)
	for i := range striped {
		if i >= 12 {
			striped[i] = 60000
		} else {
			striped[i] = 1000
		}
	}
	s = Analyze(striped, 6, 4)
	if s.SaturatedFraction != 0 || s.BoundaryRow != 2 || s.StrongestRowJump != 59000 || s.Median != 30500 || s.NearSaturatedFraction != 0 {
		t.Fatalf("unexpected boundary stats: %+v", withoutHistogram(s))
	}
	if s.SHA256 == Analyze(uniform, 6, 4).SHA256 {
		t.Fatal("different pixel arrays have same hash")
	}
}

func TestAnalyzeExactDistribution(t *testing.T) {
	s := Analyze([]uint16{0, 1, 2, 3}, 2, 2)
	if !s.Valid || s.Mean != 1.5 || s.Median != 1.5 || s.Min != 0 || s.Max != 3 || math.Abs(s.StdDev-math.Sqrt(1.25)) > 1e-12 || s.ZeroFraction != 0.25 || s.UniqueValues != 4 {
		t.Fatalf("bad distribution: %+v", withoutHistogram(s))
	}
	if len(s.Rows) != 2 || s.Rows[0].Mean != 0.5 || s.Rows[1].Mean != 2.5 || s.Columns[0].Mean != 1 {
		t.Fatal("row or column statistics incorrect")
	}
	if math.Abs(s.P01-0.03) > 1e-12 || math.Abs(s.P99-2.97) > 1e-12 {
		t.Fatal("quantile interpolation incorrect")
	}
	for _, tc := range []struct {
		p    []uint16
		w, h int
	}{{nil, 0, 1}, {[]uint16{1}, 2, 2}, {nil, -1, 1}, {nil, int(^uint(0) >> 1), 2}} {
		if Analyze(tc.p, tc.w, tc.h).Valid {
			t.Fatal("invalid dimensions accepted")
		}
	}
}

func TestFITSUnsignedEncodingAndPadding(t *testing.T) {
	var buf bytes.Buffer
	pixels := []uint16{0, 1, 32767, 32768, 65534, 65535}
	if err := writeFITS(&buf, pixels, 3, 2, map[string]any{"exposure_seconds": 5.25, "camera": "O'Brien camera"}); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	if len(data) != 5760 {
		t.Fatalf("FITS length %d, want 5760", len(data))
	}
	header := string(data[:2880])
	for _, card := range []string{"SIMPLE  =                    T", "BITPIX  =                   16", "NAXIS1  =                    3", "NAXIS2  =                    2", "BZERO   =                32768", "BSCALE  =                    1", "EXPTIME =                 5.25", "'O''Brien camera'"} {
		if !strings.Contains(header, card) {
			t.Fatalf("missing FITS card %q", card)
		}
	}
	want := []byte{0x80, 0x00, 0x80, 0x01, 0xff, 0xff, 0x00, 0x00, 0x7f, 0xfe, 0x7f, 0xff}
	if !bytes.Equal(data[2880:2892], want) {
		t.Fatalf("wrong signed FITS pixels: %x", data[2880:2892])
	}
	for i, want := range pixels {
		got := int32(int16(binary.BigEndian.Uint16(data[2880+2*i:2882+2*i]))) + 32768
		if got != int32(want) {
			t.Fatalf("decoded pixel %d: got %d want %d", i, got, want)
		}
	}
	for _, b := range data[2892:] {
		if b != 0 {
			t.Fatal("nonzero FITS data padding")
		}
	}
}

func TestSaveFramePreservesPixelsAndProtectsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	pixels := []uint16{0, 1000, 32768, 65535}
	f, err := SaveFrame(dir, "frame", pixels, 2, 2, map[string]any{"exposure_seconds": 0.01})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{f.FITS, f.PNG, f.StretchedPNG, f.RowsCSV, f.ColumnsCSV, f.HistogramCSV} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Size() == 0 {
			t.Fatalf("missing or empty %s", name)
		}
	}
	file, err := os.Open(filepath.Join(dir, f.PNG))
	if err != nil {
		t.Fatal(err)
	}
	im, err := png.Decode(file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range pixels {
		r, g, b, _ := im.At(i%2, i/2).RGBA()
		if r != uint32(want) || g != r || b != r {
			t.Fatalf("fixed PNG changed pixel %d", i)
		}
	}
	if _, err := SaveFrame(dir, "frame", pixels, 2, 2, nil); err == nil {
		t.Fatal("overwriting existing frame was accepted")
	}
	if _, err := SaveFrame(dir, "../escape", pixels, 2, 2, nil); err == nil {
		t.Fatal("path traversal accepted")
	}
}

// Keep failure messages readable rather than printing the complete histogram.
func withoutHistogram(s Stats) any {
	return struct {
		Valid                                                                            bool
		Mean, Median, StdDev, SaturatedFraction, NearSaturatedFraction, StrongestRowJump float64
		BoundaryRow, IdenticalAdjacentRows                                               int
	}{s.Valid, s.Mean, s.Median, s.StdDev, s.SaturatedFraction, s.NearSaturatedFraction, s.StrongestRowJump, s.BoundaryRow, s.IdenticalAdjacentRows}
}
