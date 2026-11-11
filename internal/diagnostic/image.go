package diagnostic

import (
	"encoding/csv"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Frame struct {
	Stem         string         `json:"stem"`
	Width        int            `json:"width"`
	Height       int            `json:"height"`
	Metadata     map[string]any `json:"metadata"`
	Stats        Stats          `json:"stats"`
	FITS         string         `json:"fits"`
	PNG          string         `json:"png_fixed"`
	StretchedPNG string         `json:"png_stretched"`
	RowsCSV      string         `json:"rows_csv"`
	ColumnsCSV   string         `json:"columns_csv"`
	HistogramCSV string         `json:"histogram_csv"`
	StretchLow   float64        `json:"stretch_low_adu"`
	StretchHigh  float64        `json:"stretch_high_adu"`
}

// SaveFrame produces original FITS pixels, a fixed 16-bit grayscale PNG, a
// separately labelled display-stretched 8-bit PNG, and complete CSV profiles.
// Its filenames are basenames relative to dir, suitable for a local HTML report.
func SaveFrame(dir, stem string, pixels []uint16, width, height int, metadata map[string]any) (Frame, error) {
	f := Frame{Stem: stem, Width: width, Height: height, Metadata: metadata}
	if stem == "" || stem == "." || stem == ".." || strings.ContainsAny(stem, "/\\:\x00") {
		return f, fmt.Errorf("invalid frame filename stem")
	}
	f.Stats = Analyze(pixels, width, height)
	if !f.Stats.Valid {
		return f, fmt.Errorf("%s", f.Stats.Error)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return f, err
	}
	f.FITS = stem + ".fits"
	f.PNG = stem + "_fixed.png"
	f.StretchedPNG = stem + "_stretch.png"
	f.RowsCSV = stem + "_rows.csv"
	f.ColumnsCSV = stem + "_columns.csv"
	f.HistogramCSV = stem + "_histogram.csv"
	f.StretchLow, f.StretchHigh = f.Stats.P01, f.Stats.P995
	if f.StretchHigh <= f.StretchLow {
		f.StretchLow, f.StretchHigh = float64(f.Stats.Min), float64(f.Stats.Max)
	}
	if f.StretchHigh <= f.StretchLow {
		f.StretchLow, f.StretchHigh = 0, 65535
	}
	gray := image.NewGray16(image.Rect(0, 0, width, height))
	stretched := image.NewGray(image.Rect(0, 0, width, height))
	for i, v := range pixels {
		gray.Pix[2*i], gray.Pix[2*i+1] = byte(v>>8), byte(v)
		scaled := math.Round((float64(v) - f.StretchLow) * 255 / (f.StretchHigh - f.StretchLow))
		stretched.Pix[i] = byte(math.Min(255, math.Max(0, scaled)))
	}
	operations := []struct {
		name  string
		write func(io.Writer) error
	}{
		{f.FITS, func(w io.Writer) error { return writeFITS(w, pixels, width, height, metadata) }},
		{f.PNG, func(w io.Writer) error { return png.Encode(w, gray) }},
		{f.StretchedPNG, func(w io.Writer) error { return png.Encode(w, stretched) }},
		{f.RowsCSV, func(w io.Writer) error { return writeProfiles(w, f.Stats.Rows) }},
		{f.ColumnsCSV, func(w io.Writer) error { return writeProfiles(w, f.Stats.Columns) }},
		{f.HistogramCSV, func(w io.Writer) error { return writeHistogram(w, &f.Stats.Histogram) }},
	}
	for _, op := range operations {
		path := filepath.Join(dir, op.name)
		// Exclusive creation avoids destroying an earlier diagnostic frame.
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return f, fmt.Errorf("create %s: %w", op.name, err)
		}
		writeErr := op.write(file)
		closeErr := file.Close()
		if writeErr != nil {
			return f, fmt.Errorf("write %s: %w", op.name, writeErr)
		}
		if closeErr != nil {
			return f, closeErr
		}
	}
	return f, nil
}

func writeProfiles(dst io.Writer, profiles []Profile) error {
	w := csv.NewWriter(dst)
	if err := w.Write([]string{"index_zero_based", "min_adu", "max_adu", "mean_adu", "median_adu", "stddev_adu", "zero_fraction", "saturated_65535_fraction", "near_saturated_ge65000_fraction"}); err != nil {
		return err
	}
	for _, p := range profiles {
		if err := w.Write([]string{strconv.Itoa(p.Index), strconv.Itoa(int(p.Min)), strconv.Itoa(int(p.Max)), f64(p.Mean), f64(p.Median), f64(p.StdDev), f64(p.ZeroFraction), f64(p.SaturatedFraction), f64(p.NearSaturatedFraction)}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func writeHistogram(dst io.Writer, hist *[65536]uint64) error {
	w := csv.NewWriter(dst)
	if err := w.Write([]string{"adu", "count"}); err != nil {
		return err
	}
	for v, n := range hist {
		if err := w.Write([]string{strconv.Itoa(v), strconv.FormatUint(n, 10)}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func f64(v float64) string { return strconv.FormatFloat(v, 'g', 12, 64) }
