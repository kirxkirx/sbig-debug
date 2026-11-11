package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kirxkirx/sbig-debug/internal/diagnostic"
)

// Retain the exact input file before parsing so reports remain self-contained and
// analysis cannot inadvertently refer to a different version of a changing file.
const maxSourceFileBytes = 512 << 20

func copyInput(source, destination string) (string, error) {
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > maxSourceFileBytes {
		return "", errors.New("input must be a regular file no larger than 512 MiB")
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(in, maxSourceFileBytes+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		return "", errors.Join(copyErr, closeErr)
	}
	if n > maxSourceFileBytes || n != st.Size() {
		return "", errors.New("input changed size while being copied or exceeds 512 MiB; retry with a completed image")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func analyzeFiles(ctx context.Context, c config, r *report, log io.Writer) error {
	r.Notes = append(r.Notes, "Offline analysis: no camera or driver was opened. Input files are retained byte for byte; the pixel FITS contains the selected image plane and a subset of its metadata. ADU values are not stretched or clamped for measurement.")
	failed := 0
	for i, source := range c.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		stem := fmt.Sprintf("%03d_import", i+1)
		err := analyzeFile(c.out, stem, source, r)
		if err != nil {
			failed++
			msg := fmt.Sprintf("%s: %v", source, err)
			r.Errors = append(r.Errors, msg)
			fmt.Fprintln(log, "ERROR:", msg)
		} else {
			frame := r.Frames[len(r.Frames)-1]
			fmt.Fprintf(log, "%s: %dx%d, median %.2f ADU, exact saturation %.3f%%, near saturation %.3f%%\n", source, frame.Width, frame.Height, frame.Stats.Median, frame.Stats.SaturatedFraction*100, frame.Stats.NearSaturatedFraction*100)
		}
		if err := saveReport(c.out, *r); err != nil {
			return err
		}
	}
	if failed != 0 {
		return fmt.Errorf("%d of %d input files could not be analyzed", failed, len(c.files))
	}
	return nil
}

func analyzeFile(dir, stem, source string, r *report) error {
	absolute, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	name := stem + "_source.fits"
	copyPath := filepath.Join(dir, name)
	hash, err := copyInput(absolute, copyPath)
	if err != nil {
		return err
	}
	img, err := diagnostic.ReadFITS(copyPath)
	if err != nil {
		return err
	}
	img.Metadata["source_path"] = absolute
	img.Metadata["source_file"] = name
	img.Metadata["source_file_sha256"] = hash
	img.Metadata["analysis_only"] = true
	img.Metadata["software"] = "SBIG Debug " + version
	frame, err := diagnostic.SaveFrame(dir, stem, img.Pixels, img.Width, img.Height, img.Metadata)
	if err != nil {
		return err
	}
	r.Frames = append(r.Frames, frame)
	return nil
}
