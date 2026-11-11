package diagnostic

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadFITSPreservesUnsignedPixelsAndMetadata(t *testing.T) {
	pixels := []uint16{0, 1, 32767, 32768, 65534, 65535}
	var file bytes.Buffer
	if err := writeFITS(&file, pixels, 3, 2, map[string]any{"IMAGETYP": "Dark Frame", "EXPTIME": 5.25, "camera": "O'Brien / test"}); err != nil {
		t.Fatal(err)
	}
	path := writeFITSFixture(t, file.Bytes())
	im, err := ReadFITS(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(im.Pixels, pixels) || im.Width != 3 || im.Height != 2 {
		t.Fatalf("changed pixels: %+v", im)
	}
	if im.Metadata["EXPTIME"] != 5.25 || im.Metadata["IMAGETYP"] != "Dark Frame" || im.Metadata["INSTRUME"] != "O'Brien / test" {
		t.Fatalf("lost metadata: %+v", im.Metadata)
	}
	if Analyze(im.Pixels, im.Width, im.Height).SHA256 != Analyze(pixels, 3, 2).SHA256 {
		t.Fatal("offline/live pixel hashes differ")
	}
}

func TestReadFITSIntegerFormatsAndImageExtension(t *testing.T) {
	for _, tc := range []struct {
		bitpix string
		data   []byte
		want   []uint16
	}{
		{"8", []byte{0, 255}, []uint16{0, 255}},
		{"16", []byte{0, 2, 0x7f, 0xff}, []uint16{2, 32767}},
		{"32", []byte{0, 0, 0, 1, 0, 0, 0xff, 0xff}, []uint16{1, 65535}},
	} {
		t.Run(tc.bitpix, func(t *testing.T) {
			primary := fitsFixture([]string{fitsCard("SIMPLE", "T", ""), fitsCard("BITPIX", "8", ""), fitsCard("NAXIS", "0", "")}, nil)
			table := fitsFixture([]string{fitsCard("XTENSION", "'BINTABLE'", ""), fitsCard("BITPIX", "8", ""), fitsCard("NAXIS", "2", ""), fitsCard("NAXIS1", "2", ""), fitsCard("NAXIS2", "1", ""), fitsCard("PCOUNT", "3", ""), fitsCard("GCOUNT", "1", "")}, []byte{0, 0, 0, 0, 0})
			image := fitsFixture([]string{fitsCard("XTENSION", "'IMAGE   '", ""), fitsCard("BITPIX", tc.bitpix, ""), fitsCard("NAXIS", "2", ""), fitsCard("NAXIS1", "2", ""), fitsCard("NAXIS2", "1", ""), fitsCard("PCOUNT", "0", ""), fitsCard("GCOUNT", "1", ""), fitsCard("BSCALE", "1.0D+00", "")}, tc.data)
			im, err := ReadFITS(writeFITSFixture(t, append(append(primary, table...), image...)))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(im.Pixels, tc.want) || im.Metadata["fits_hdu_index"] != 2 {
				t.Fatalf("wrong image or data: %+v", im)
			}
		})
	}
}

func TestReadFITSRejectsUnsafeOrLossyInputs(t *testing.T) {
	base := []string{fitsCard("SIMPLE", "T", ""), fitsCard("BITPIX", "16", ""), fitsCard("NAXIS", "2", ""), fitsCard("NAXIS1", "2", ""), fitsCard("NAXIS2", "1", "")}
	for _, tc := range []struct {
		name, key, value string
		data             []byte
	}{
		{"negative width", "NAXIS1", "-1", nil},
		{"invalid width", "NAXIS1", "abc", nil},
		{"overflow", "NAXIS1", "9223372036854775807", nil},
		{"oversized", "NAXIS1", "32000001", nil},
		{"truncated", "", "", nil},
		{"negative pixel", "", "", []byte{0xff, 0xff, 0, 0}},
		{"nonintegral zero", "BZERO", "0.5", []byte{0, 1, 0, 2}},
		{"nonintegral scale", "BSCALE", "0.5", []byte{0, 1, 0, 2}},
		{"integer scale", "BSCALE", "2", []byte{0, 1, 0, 2}},
		{"blank", "BLANK", "1", []byte{0, 1, 0, 2}},
		{"large physical value", "BZERO", "65535", []byte{0, 1, 0, 2}},
		{"huge exponent", "BZERO", "1D999999999", []byte{0, 1, 0, 2}},
		{"hex exponent", "BZERO", "0x1p999999999", []byte{0, 1, 0, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cards := append([]string{}, base...)
			if tc.key != "" {
				replaced := false
				for i, card := range cards {
					if strings.TrimSpace(card[:8]) == tc.key {
						cards[i] = fitsCard(tc.key, tc.value, "")
						replaced = true
					}
				}
				if !replaced {
					cards = append(cards, fitsCard(tc.key, tc.value, ""))
				}
			}
			_, err := ReadFITS(writeFITSFixture(t, fitsFixture(cards, tc.data)))
			if err == nil {
				t.Fatal("invalid or lossy input accepted")
			}
		})
	}
}

func TestReadFITSRejectsUnsupportedPixelTypesAndDuplicates(t *testing.T) {
	for _, bitpix := range []int{64, -32, -64} {
		data := fitsFixture([]string{fitsCard("SIMPLE", "T", ""), fitsCard("BITPIX", fmt.Sprint(bitpix), ""), fitsCard("NAXIS", "2", ""), fitsCard("NAXIS1", "1", ""), fitsCard("NAXIS2", "1", "")}, make([]byte, 8))
		if _, err := ReadFITS(writeFITSFixture(t, data)); err == nil {
			t.Fatalf("accepted BITPIX %d", bitpix)
		}
	}
	data := fitsFixture([]string{fitsCard("SIMPLE", "T", ""), fitsCard("BITPIX", "16", ""), fitsCard("NAXIS", "2", ""), fitsCard("NAXIS1", "1", ""), fitsCard("NAXIS1", "2", ""), fitsCard("NAXIS2", "1", "")}, []byte{0, 1, 0, 2})
	if _, err := ReadFITS(writeFITSFixture(t, data)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate dimensions accepted: %v", err)
	}
}

func TestAnalyzeMediansBitsRegionsAndBoundaries(t *testing.T) {
	pixels := []uint16{0, 2, 4, 6, 8, 10, 12, 14, 100, 102, 104, 106, 200, 202, 204, 206}
	s := Analyze(pixels, 4, 4)
	if s.Rows[0].Median != 3 || s.Rows[3].Median != 203 || s.Columns[0].Median != 54 || s.Columns[3].Median != 60 {
		t.Fatalf("wrong medians: rows=%+v columns=%+v", s.Rows, s.Columns)
	}
	if s.BitSetFraction[0] != 0 || s.BitSetFraction[1] != 0.5 || s.LeftHalf.Count != 8 || s.RightHalf.Count != 8 || s.LeftHalf.Median != 55 || s.RightHalf.Median != 59 || s.EvenColumns.Median != 56 || s.OddColumns.Median != 58 {
		t.Fatalf("wrong summaries: bits=%v left=%+v right=%+v even=%+v odd=%+v", s.BitSetFraction, s.LeftHalf, s.RightHalf, s.EvenColumns, s.OddColumns)
	}
	if len(s.RowMedianBoundaries) != 3 || s.RowMedianBoundaries[0].Row != 3 || s.RowMedianBoundaries[0].Delta != 100 || s.RowMedianBoundaries[1].Row != 2 || s.RowMedianBoundaries[2].Row != 1 {
		t.Fatalf("wrong boundary ranking: %+v", s.RowMedianBoundaries)
	}
	for _, dimensions := range [][2]int{{1, 3}, {3, 1}, {1, 1}} {
		p := make([]uint16, dimensions[0]*dimensions[1])
		for i := range p {
			p[i] = uint16(i + 1)
		}
		got := Analyze(p, dimensions[0], dimensions[1])
		if _, err := json.Marshal(got); err != nil {
			t.Fatalf("thin image summary is not JSON-safe: %v", err)
		}
	}
}

func TestReadFITS32BitDoesNotWrapTo16Bits(t *testing.T) {
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, 65536)
	file := fitsFixture([]string{fitsCard("SIMPLE", "T", ""), fitsCard("BITPIX", "32", ""), fitsCard("NAXIS", "2", ""), fitsCard("NAXIS1", "1", ""), fitsCard("NAXIS2", "1", "")}, data)
	if _, err := ReadFITS(writeFITSFixture(t, file)); err == nil {
		t.Fatal("out-of-range pixel wrapped to zero")
	}
}

func fitsFixture(cards []string, data []byte) []byte {
	cards = append(cards, fmt.Sprintf("%-80s", "END"))
	header := strings.Join(cards, "")
	header += strings.Repeat(" ", (2880-len(header)%2880)%2880)
	result := append([]byte(header), data...)
	if len(data) > 0 {
		result = append(result, make([]byte, (2880-len(data)%2880)%2880)...)
	}
	return result
}

func writeFITSFixture(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image.fits")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
