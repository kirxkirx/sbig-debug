package diagnostic

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// writeFITS emits the standard unsigned-16 convention: signed BITPIX=16 values
// with BZERO=32768, BSCALE=1. Pixels are in driver row order, without rotation,
// subtraction, defect repair, or scaling. Header and data are 2880-byte padded.
func writeFITS(dst io.Writer, pixels []uint16, width, height int, metadata map[string]any) error {
	if !validDimensions(pixels, width, height) {
		return fmt.Errorf("invalid FITS image dimensions")
	}
	w := bufio.NewWriter(dst)
	cards := []string{
		fitsCard("SIMPLE", "T", "conforms to FITS standard"),
		fitsCard("BITPIX", "16", "signed storage; unsigned through BZERO"),
		fitsCard("NAXIS", "2", ""), fitsCard("NAXIS1", strconv.Itoa(width), ""),
		fitsCard("NAXIS2", strconv.Itoa(height), ""), fitsCard("BSCALE", "1", ""),
		fitsCard("BZERO", "32768", "physical pixel range 0 through 65535"),
		fitsCard("BUNIT", fitsString("ADU"), ""),
	}
	// Explicit mapping prevents arbitrary metadata keys from creating invalid cards.
	fields := []struct {
		key     string
		aliases []string
		numeric bool
	}{
		{"EXPTIME", []string{"EXPTIME", "exposure_seconds", "exposure", "exposure_s"}, true},
		{"CCD-TEMP", []string{"CCD-TEMP", "ccd_temperature", "temperature_c", "ccd_temperature_c"}, true},
		{"XBINNING", []string{"XBINNING", "binning", "bin"}, true},
		{"YBINNING", []string{"YBINNING", "binning", "bin"}, true},
		{"DATE-OBS", []string{"DATE-OBS", "date_obs", "started_utc", "timestamp_utc"}, false},
		{"INSTRUME", []string{"INSTRUME", "camera", "camera_name"}, false},
		{"IMAGETYP", []string{"IMAGETYP", "image_type", "frame_type"}, false},
		{"SWCREATE", []string{"SWCREATE", "software"}, false},
	}
	for _, field := range fields {
		var v any
		for _, alias := range field.aliases {
			if value, ok := metadata[alias]; ok {
				v = value
				break
			}
		}
		if v == nil {
			continue
		}
		value := fmt.Sprint(v)
		if field.numeric {
			number, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				continue
			}
			value = strconv.FormatFloat(number, 'G', 14, 64)
		} else {
			value = fitsString(value)
		}
		cards = append(cards, fitsCard(field.key, value, ""))
	}
	orderComment := "COMMENT Pixel order is the driver readout order; no optical orientation assumed."
	if offline, _ := metadata["analysis_only"].(bool); offline {
		orderComment = "COMMENT Pixel order is the stored input order; no optical orientation assumed."
	}
	cards = append(cards, fmt.Sprintf("%-80s", orderComment))
	cards = append(cards, fmt.Sprintf("%-80s", "END"))
	header := strings.Join(cards, "")
	header += strings.Repeat(" ", (2880-len(header)%2880)%2880)
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	row := make([]byte, width*2)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			v := pixels[y*width+x] ^ 0x8000
			row[2*x], row[2*x+1] = byte(v>>8), byte(v)
		}
		if _, err := w.Write(row); err != nil {
			return err
		}
	}
	padding := (2880 - (len(pixels)*2)%2880) % 2880
	if _, err := w.Write(make([]byte, padding)); err != nil {
		return err
	}
	return w.Flush()
}

func fitsCard(key, value, comment string) string {
	s := fmt.Sprintf("%-8s= %20s", key, value)
	if comment != "" {
		s += " / " + comment
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return fmt.Sprintf("%-80s", s)
}

func fitsString(s string) string {
	// FITS strings must be ASCII. Limit escaped content, keeping a closing quote.
	var out strings.Builder
	for _, r := range s {
		piece := string(r)
		if r < 32 || r > 126 {
			piece = "?"
		}
		if r == '\'' {
			piece = "''"
		}
		if out.Len()+len(piece) > 66 {
			break
		}
		out.WriteString(piece)
	}
	return "'" + out.String() + "'"
}
