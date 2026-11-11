package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/kirxkirx/sbig-debug/internal/diagnostic"
)

func TestOfflinePreservesOriginalAndPhysicalPixels(t *testing.T) {
	sourceDir := t.TempDir()
	pixels := []uint16{0, 1000, 62000, 65535}
	source, err := diagnostic.SaveFrame(sourceDir, "original", pixels, 2, 2, map[string]any{"EXPTIME": 5, "IMAGETYP": "Dark Frame"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sourceDir, source.FITS)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c := config{action: "analyze", out: t.TempDir(), files: []string{path}}
	var r report
	if err = analyzeFiles(context.Background(), c, &r, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(r.Frames) != 1 || r.Frames[0].Stats.SHA256 != source.Stats.SHA256 {
		t.Fatal("offline analysis changed physical pixels")
	}
	frame := r.Frames[0]
	copy, err := os.ReadFile(filepath.Join(c.out, frame.Metadata["source_file"].(string)))
	if err != nil || !bytes.Equal(copy, original) {
		t.Fatalf("original file was not preserved: %v", err)
	}
	if got := frame.Metadata["source_file_sha256"]; got != fmt.Sprintf("%x", sha256.Sum256(original)) {
		t.Fatalf("source hash=%v", got)
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(original, unchanged) {
		t.Fatal("modified source")
	}
	image, err := diagnostic.ReadFITS(filepath.Join(c.out, frame.FITS))
	if err != nil || !bytes.Equal(uint16Bytes(image.Pixels), uint16Bytes(pixels)) {
		t.Fatalf("saved pixel FITS differs: %v", err)
	}
}

func uint16Bytes(p []uint16) []byte {
	b := make([]byte, len(p)*2)
	for i, v := range p {
		b[2*i], b[2*i+1] = byte(v>>8), byte(v)
	}
	return b
}

func TestOfflineBatchRetainsGoodFrameAfterBadFile(t *testing.T) {
	dir := t.TempDir()
	source, err := diagnostic.SaveFrame(dir, "valid", []uint16{1000}, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.fits")
	if err = os.WriteFile(bad, []byte("not a FITS file"), 0600); err != nil {
		t.Fatal(err)
	}
	c := config{action: "analyze", out: t.TempDir(), files: []string{bad, filepath.Join(dir, source.FITS)}}
	var r report
	if err = analyzeFiles(context.Background(), c, &r, io.Discard); err == nil {
		t.Fatal("batch silently accepted bad input")
	}
	if len(r.Errors) != 1 || len(r.Frames) != 1 || r.Frames[0].Stem != "002_import" {
		t.Fatal("batch lost successful evidence or error")
	}
	if _, err = os.Stat(filepath.Join(c.out, "report.json")); err != nil {
		t.Fatal(err)
	}
}

func TestOfflineInputValidationAndExclusiveCopy(t *testing.T) {
	for _, c := range []config{{action: "analyze"}, {action: "analyze", files: []string{""}}, {action: "analyze", files: []string{"image.fits"}, demo: true}, {action: "baseline", files: []string{"image.fits"}}} {
		if _, _, err := validate(c); err == nil {
			t.Fatalf("accepted invalid input: %+v", c)
		}
	}
	if _, _, err := validate(config{action: "analyze", files: []string{"image.fits"}}); err != nil {
		t.Fatal("offline action unnecessarily requires acquisition parameters:", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "source.fits")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := copyInput(path, path); err == nil {
		t.Fatal("overwrote source")
	}
	if _, err := copyInput(dir, filepath.Join(dir, "copy.fits")); err == nil {
		t.Fatal("accepted a directory")
	}
}
