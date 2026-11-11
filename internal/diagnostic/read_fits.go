package diagnostic

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MaxFITSPixels bounds offline allocations, including later previews, for the
// 32-bit Windows build. Supported image axes are additionally limited to 65535.
const MaxFITSPixels = 32_000_000

// Image contains physical integer ADU values, never a display normalization.
type Image struct {
	Pixels        []uint16
	Width, Height int
	Metadata      map[string]any
}

// ReadFITS reads the first nonempty primary/image HDU. It accepts uncompressed,
// monochrome 2-D BITPIX 8/16/32 images with BSCALE=1 and integral BZERO whose
// physical values fit 0..65535. Floating-point, cubes, undefined pixels and other
// scaling are rejected explicitly instead of being clipped or rescaled.
// FITS reference: https://fits.gsfc.nasa.gov/fits_standard.html
func ReadFITS(path string) (Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return Image{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Image{}, err
	}
	if !info.Mode().IsRegular() {
		return Image{}, errors.New("FITS input must be a regular file")
	}
	for hdu := 0; hdu < 64; hdu++ {
		header, err := readFITSHeader(f, hdu == 0)
		if err != nil {
			return Image{}, fmt.Errorf("FITS HDU %d: %w", hdu, err)
		}
		if header["GROUPS"] == "T" {
			return Image{}, errors.New("FITS random groups are not supported")
		}
		bitpix, err := fitsInteger(header, "BITPIX", 0, true)
		if err != nil {
			return Image{}, err
		}
		bpp := int64(0)
		switch bitpix {
		case 8, 16, 32, 64:
			bpp = bitpix / 8
		case -32, -64:
			bpp = -bitpix / 8
		default:
			return Image{}, fmt.Errorf("unsupported FITS BITPIX %d", bitpix)
		}
		naxis, err := fitsInteger(header, "NAXIS", 0, true)
		if err != nil || naxis < 0 || naxis > 9 {
			return Image{}, fmt.Errorf("invalid or unsupported FITS NAXIS %q", header["NAXIS"])
		}
		axes := make([]int64, int(naxis))
		count := int64(0)
		if naxis > 0 {
			count = 1
		}
		for i := range axes {
			axes[i], err = fitsInteger(header, fmt.Sprintf("NAXIS%d", i+1), 0, true)
			if err != nil || axes[i] < 0 {
				return Image{}, fmt.Errorf("invalid FITS NAXIS%d", i+1)
			}
			count, err = fitsSizeProduct(count, axes[i])
			if err != nil {
				return Image{}, err
			}
		}
		pcount, err := fitsInteger(header, "PCOUNT", 0, false)
		if err != nil || pcount < 0 || pcount > math.MaxInt64-count {
			return Image{}, errors.New("invalid or overflowing FITS PCOUNT")
		}
		gcount, err := fitsInteger(header, "GCOUNT", 1, false)
		if err != nil || gcount < 1 {
			return Image{}, errors.New("invalid FITS GCOUNT")
		}
		dataSize, err := fitsSizeProduct(count+pcount, gcount)
		if err == nil {
			dataSize, err = fitsSizeProduct(dataSize, bpp)
		}
		if err != nil || dataSize > math.MaxInt64-2879 {
			return Image{}, errors.New("FITS data size overflow")
		}
		dataStart, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return Image{}, err
		}
		if dataStart > info.Size() || dataSize > info.Size()-dataStart {
			return Image{}, errors.New("truncated FITS image or extension data")
		}
		imageHDU := hdu == 0 || strings.TrimSpace(header["XTENSION"]) == "IMAGE"
		if imageHDU && count > 0 {
			if naxis != 2 || pcount != 0 || gcount != 1 {
				return Image{}, errors.New("only monochrome two-dimensional FITS images are supported")
			}
			if axes[0] > 65535 || axes[1] > 65535 || count > MaxFITSPixels {
				return Image{}, fmt.Errorf("FITS image exceeds the limit of %d pixels or 65535 pixels per axis", MaxFITSPixels)
			}
			if bitpix != 8 && bitpix != 16 && bitpix != 32 {
				return Image{}, fmt.Errorf("FITS BITPIX %d cannot be analyzed as raw unsigned 16-bit ADU; supported values are 8, 16 and 32", bitpix)
			}
			scale, err := fitsExactInteger(header, "BSCALE", 1)
			if err != nil || scale != 1 {
				return Image{}, errors.New("FITS analysis requires BSCALE=1; scaled images are not silently rescaled")
			}
			zero, err := fitsExactInteger(header, "BZERO", 0)
			if err != nil || zero < -4294967295 || zero > 4294967295 {
				return Image{}, errors.New("FITS BZERO must be an integer within the supported 32-bit storage range")
			}
			blank, err := fitsInteger(header, "BLANK", 0, false)
			if err != nil {
				return Image{}, err
			}
			_, hasBlank := header["BLANK"]
			im := Image{Pixels: make([]uint16, int(count)), Width: int(axes[0]), Height: int(axes[1])}
			row := make([]byte, int(axes[0]*bpp))
			for y := 0; y < im.Height; y++ {
				if _, err := io.ReadFull(f, row); err != nil {
					return Image{}, fmt.Errorf("read FITS row %d: %w", y, err)
				}
				for x := 0; x < im.Width; x++ {
					var raw int64
					switch bitpix {
					case 8:
						raw = int64(row[x])
					case 16:
						raw = int64(int16(binary.BigEndian.Uint16(row[x*2:])))
					case 32:
						raw = int64(int32(binary.BigEndian.Uint32(row[x*4:])))
					}
					if hasBlank && raw == blank {
						return Image{}, fmt.Errorf("undefined FITS pixel at column %d, row %d", x, y)
					}
					value := raw + zero // bounded above: cannot overflow int64.
					if value < 0 || value > 65535 {
						return Image{}, fmt.Errorf("FITS pixel at column %d, row %d is %d ADU, outside 0..65535; no clipping applied", x, y, value)
					}
					im.Pixels[y*im.Width+x] = uint16(value)
				}
			}
			im.Metadata = fitsImageMetadata(path, hdu, header)
			return im, nil
		}
		padded := (dataSize + 2879) / 2880 * 2880
		if padded > info.Size()-dataStart {
			return Image{}, errors.New("truncated FITS extension padding")
		}
		if _, err := f.Seek(padded, io.SeekCurrent); err != nil {
			return Image{}, err
		}
	}
	return Image{}, errors.New("no supported image found within 64 FITS HDUs")
}

func readFITSHeader(r io.Reader, primary bool) (map[string]string, error) {
	h := make(map[string]string)
	block := make([]byte, 2880)
	for n := 0; n < 128; n++ {
		if _, err := io.ReadFull(r, block); err != nil {
			return nil, fmt.Errorf("read header: %w", err)
		}
		if n == 0 {
			want := "XTENSION"
			if primary {
				want = "SIMPLE  "
			}
			if string(block[:8]) != want {
				return nil, fmt.Errorf("expected %s as the first FITS card", strings.TrimSpace(want))
			}
		}
		for offset := 0; offset < 2880; offset += 80 {
			card := string(block[offset : offset+80])
			key := strings.TrimSpace(card[:8])
			if key == "END" {
				if primary && h["SIMPLE"] != "T" {
					return nil, errors.New("FITS SIMPLE must be T")
				}
				return h, nil
			}
			if card[8:10] != "= " {
				continue
			}
			value, err := fitsValue(card[10:])
			if err != nil {
				return nil, fmt.Errorf("FITS %s: %w", key, err)
			}
			if _, exists := h[key]; exists && (key == "BITPIX" || key == "NAXIS" || strings.HasPrefix(key, "NAXIS") || key == "BSCALE" || key == "BZERO" || key == "BLANK" || key == "PCOUNT" || key == "GCOUNT" || key == "XTENSION" || key == "SIMPLE" || key == "GROUPS") {
				return nil, fmt.Errorf("duplicate structural FITS keyword %s", key)
			}
			h[key] = value
		}
	}
	return nil, errors.New("FITS header exceeds 128 blocks without END")
}

func fitsValue(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "'") {
		v, _, _ := strings.Cut(s, "/")
		return strings.TrimSpace(v), nil
	}
	var value strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] != '\'' {
			value.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '\'' {
			value.WriteByte('\'')
			i++
			continue
		}
		return strings.TrimRight(value.String(), " "), nil
	}
	return "", errors.New("unterminated quoted value")
}

func fitsInteger(h map[string]string, key string, fallback int64, required bool) (int64, error) {
	s, ok := h[key]
	if !ok && !required {
		return fallback, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid FITS integer %s=%q", key, s)
	}
	return n, nil
}

func fitsExactInteger(h map[string]string, key string, fallback int64) (int64, error) {
	s, ok := h[key]
	if !ok {
		return fallback, nil
	}
	// Header cards are short, but absurd exponents must be rejected before
	// big.Rat could allocate enormous integers for an untrusted input.
	text := strings.ReplaceAll(strings.ToUpper(s), "D", "E")
	for _, char := range text {
		if !strings.ContainsRune("0123456789+-.E", char) {
			return 0, fmt.Errorf("invalid FITS number in %s", key)
		}
	}
	if _, err := strconv.ParseFloat(text, 64); err != nil {
		return 0, fmt.Errorf("invalid FITS number in %s", key)
	}
	if pos := strings.IndexByte(text, 'E'); pos >= 0 {
		exp, err := strconv.Atoi(text[pos+1:])
		if err != nil || exp < -100 || exp > 100 {
			return 0, fmt.Errorf("unsupported FITS exponent in %s", key)
		}
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		return 0, fmt.Errorf("FITS %s must be an exact integer", key)
	}
	return r.Num().Int64(), nil
}

func fitsSizeProduct(a, b int64) (int64, error) {
	if a < 0 || b < 0 || b != 0 && a > math.MaxInt64/b {
		return 0, errors.New("FITS dimension product overflow")
	}
	return a * b, nil
}

func fitsImageMetadata(path string, hdu int, header map[string]string) map[string]any {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	m := map[string]any{"source_path": abs, "fits_hdu_index": hdu, "fits_header": header, "pixel_values": "physical FITS values; no clipping, normalization or orientation change"}
	for _, key := range []string{"IMAGETYP", "DATE-OBS", "INSTRUME", "SWCREATE", "BUNIT"} {
		if s, ok := header[key]; ok {
			m[key] = s
		}
	}
	for _, key := range []string{"EXPTIME", "CCD-TEMP", "XBINNING", "YBINNING"} {
		if s, ok := header[key]; ok {
			v, err := strconv.ParseFloat(strings.ReplaceAll(strings.ToUpper(s), "D", "E"), 64)
			if err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
				m[key] = v
			}
		}
	}
	return m
}
