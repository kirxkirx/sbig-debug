package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kirxkirx/sbig-debug/internal/sbig"
)

type fakeCamera struct {
	controls   map[uint16]int32
	writes     []controlChange
	captures   []map[uint16]int32
	captureErr error
	readErr    bool
	setErr     bool
	restoreErr bool
}

func (f *fakeCamera) Close() error             { return nil }
func (f *fakeCamera) Snapshot() map[string]any { return map[string]any{"simulation": true} }
func (f *fakeCamera) Modes() ([]sbig.Mode, error) {
	return []sbig.Mode{{ID: 0, Width: 2, Height: 2, Name: "1x1"}}, nil
}
func (f *fakeCamera) GetControl(id uint16) (int32, error) {
	if f.readErr {
		return 0, errors.New("query failed")
	}
	return f.controls[id], nil
}
func (f *fakeCamera) SetControl(id uint16, v int32) error {
	f.writes = append(f.writes, controlChange{id, v})
	if f.restoreErr && v == 1 {
		return errors.New("restore failed")
	}
	f.controls[id] = v
	if f.setErr && v == 0 {
		return errors.New("partial write failed")
	}
	return nil
}
func (f *fakeCamera) Capture(ctx context.Context, _ sbig.Mode, _ float64) ([]uint16, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	m := map[uint16]int32{}
	for k, v := range f.controls {
		m[k] = v
	}
	f.captures = append(f.captures, m)
	return []uint16{1, 2, 3, 4}, f.captureErr
}
func testConfig(t *testing.T) config {
	return config{out: t.TempDir(), action: "compare", test: "vdd", count: 1, timeout: time.Second, exposures: "0.01", modes: "0"}
}
func TestComparisonRestoresAndBracketsBaseline(t *testing.T) {
	c := testConfig(t)
	f := &fakeCamera{controls: map[uint16]int32{6: 1, 7: 1, 8: 1}}
	var r report
	if e := session(context.Background(), f, c, []float64{0.01}, []uint16{0}, &r, io.Discard); e != nil {
		t.Fatal(e)
	}
	if len(f.captures) != 3 || f.captures[0][8] != 1 || f.captures[1][8] != 0 || f.captures[2][8] != 1 || f.controls[8] != 1 {
		t.Fatalf("unbracketed or unrestored comparison: %+v", f.captures)
	}
	for _, m := range f.captures {
		if m[7] != 1 {
			t.Fatal("one-variable experiment changed unrelated throughput")
		}
	}
	if len(r.Frames) != 3 {
		t.Fatal("missing frames")
	}
}
func TestRestoreOnAcquisitionAndPartialWriteErrors(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "acquisition", true: "write"}[partial], func(t *testing.T) {
			c := testConfig(t)
			f := &fakeCamera{controls: map[uint16]int32{7: 1, 8: 1}, captureErr: errors.New("readout failure"), setErr: partial}
			p := profile{"experiment", []controlChange{{8, 0}, {7, 0}}}
			var r report
			seq := 0
			e := runProfile(context.Background(), f, c, p, []sbig.Mode{{ID: 0, Width: 2, Height: 2}}, []float64{0.01}, &r, io.Discard, &seq)
			if e == nil || f.controls[8] != 1 || f.controls[7] != 1 {
				t.Fatalf("controls not restored after failure: %v %+v", e, f.controls)
			}
		})
	}
}
func TestRestoreFailureIsVisible(t *testing.T) {
	c := testConfig(t)
	f := &fakeCamera{controls: map[uint16]int32{8: 1}, restoreErr: true, captureErr: errors.New("readout failure")}
	seq := 0
	e := runProfile(context.Background(), f, c, profile{"vdd-off", []controlChange{{8, 0}}}, []sbig.Mode{{ID: 0, Width: 2, Height: 2}}, []float64{0.01}, &report{}, io.Discard, &seq)
	if e == nil || !strings.Contains(e.Error(), "RESTORE FAILED") || !strings.Contains(e.Error(), "readout failure") {
		t.Fatalf("lost failure: %v", e)
	}
}
func TestUnreadableControlNeverWritten(t *testing.T) {
	c := testConfig(t)
	f := &fakeCamera{readErr: true}
	seq := 0
	e := runProfile(context.Background(), f, c, profile{"vdd-off", []controlChange{{8, 0}}}, nil, nil, &report{}, io.Discard, &seq)
	if e == nil || len(f.writes) > 0 {
		t.Fatal("guessed original control")
	}
}

func TestUnexpectedBooleanControlNeverWritten(t *testing.T) {
	c := testConfig(t)
	f := &fakeCamera{controls: map[uint16]int32{8: 2}}
	seq := 0
	e := runProfile(context.Background(), f, c, profile{"vdd-off", []controlChange{{8, 0}}}, nil, nil, &report{}, io.Discard, &seq)
	if e == nil || len(f.writes) > 0 {
		t.Fatal("modified a control with an unrestorable original value")
	}
}
func TestInfoDoesNotAcquireOrChangeSettings(t *testing.T) {
	c := testConfig(t)
	c.action = "info"
	f := &fakeCamera{readErr: true}
	if e := session(context.Background(), f, c, nil, nil, &report{}, io.Discard); e != nil {
		t.Fatal(e)
	}
	if len(f.writes) > 0 || len(f.captures) > 0 {
		t.Fatal("info mode changed/acquired")
	}
}
func TestDisabledPixelDownloadRefusesAcquisition(t *testing.T) {
	c := testConfig(t)
	f := &fakeCamera{controls: map[uint16]int32{6: 0}}
	if e := session(context.Background(), f, c, []float64{0.01}, []uint16{0}, &report{}, io.Discard); e == nil {
		t.Fatal("accepted disabled pixels")
	}
	if len(f.captures) > 0 || len(f.writes) > 0 {
		t.Fatal("changed or acquired")
	}
}
func TestValidateExposureAndCounts(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "0", "-1", "0.001", "3601", "1.015", ""} {
		c := testConfig(t)
		c.exposures = value
		if _, _, e := validate(c); e == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	c := testConfig(t)
	c.exposures = "0.01,2.99,3,3.01,30"
	if _, _, e := validate(c); e != nil {
		t.Fatal(e)
	}
}
func TestDemoReportAndHTMLEscaping(t *testing.T) {
	dir := t.TempDir()
	r := report{Demo: true, Notes: []string{"<script>alert(1)</script>"}}
	if e := demo(dir, &r, io.Discard); e != nil {
		t.Fatal(e)
	}
	if e := saveReport(dir, r); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "report.html"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "<script>alert") || !strings.Contains(string(b), "SIMULATED DATA ONLY") {
		t.Fatal("unsafe or unlabeled report")
	}
	if len(r.Frames) != 3 || r.Frames[2].Stats.SaturatedFraction != 1 || r.Frames[1].Stats.BoundaryRow != 140 {
		t.Fatal("demo lost diagnostic patterns")
	}
	// Repeated saves must replace reports on Windows too.
	if e = saveReport(dir, r); e != nil {
		t.Fatal(e)
	}
}
