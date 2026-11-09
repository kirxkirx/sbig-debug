package sbig

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

type mockTransport struct {
	fn    func(uint16, []byte, []byte) int16
	calls []uint16
}

func (m *mockTransport) call(id uint16, p, r []byte) int16 {
	m.calls = append(m.calls, id)
	return m.fn(id, p, r)
}
func (m *mockTransport) close() error             { return nil }
func (m *mockTransport) metadata() map[string]any { return map[string]any{"mock": true} }

// The fixed offsets below are the Windows SDK's pack(8) C ABI, including the
// otherwise easy-to-miss two-byte padding before GetCCDInfo.readoutInfo and the
// two-byte padding after StartExposure2.ccd. They are identical on x86 and x64.
func fillCCDInfo(r []byte) {
	binary.LittleEndian.PutUint16(r[0:], 0x0284)
	binary.LittleEndian.PutUint16(r[2:], 17)
	copy(r[4:68], "STXL-11002 mock")
	binary.LittleEndian.PutUint16(r[68:], 1)
	binary.LittleEndian.PutUint16(r[72:], 0)
	binary.LittleEndian.PutUint16(r[74:], 4)
	binary.LittleEndian.PutUint16(r[76:], 3)
	binary.LittleEndian.PutUint16(r[78:], 0x0081)
	binary.LittleEndian.PutUint32(r[80:], 0x00000900)
	binary.LittleEndian.PutUint32(r[84:], 0x00000900)
}

func TestWindowsCCDInfoPacking(t *testing.T) {
	m := &mockTransport{fn: func(id uint16, p, r []byte) int16 {
		if id != 11 || len(p) != 2 || len(r) != 392 {
			t.Fatalf("wrong request: id %d, params %d, results %d", id, len(p), len(r))
		}
		fillCCDInfo(r)
		return 0
	}}
	c := &Camera{t: m}
	info, modes, err := c.ccdInfo(0)
	if err != nil || len(modes) != 1 || modes[0] != (Mode{0, 4, 3, "1x1"}) {
		t.Fatalf("decode: %v %v", modes, err)
	}
	if info["firmware_version"] != "2.84" {
		t.Fatal(info)
	}
	d := info["readout_modes"].([]map[string]any)[0]
	if d["gain_e_per_adu"] != 0.81 || d["pixel_width_um"] != 9.0 {
		t.Fatal(d)
	}
}

func TestCaptureLifecycleAndGeometry(t *testing.T) {
	row := 0
	started := false
	m := &mockTransport{fn: func(id uint16, p, r []byte) int16 {
		switch id {
		case 11:
			fillCCDInfo(r)
		case 50:
			started = true
			want := []byte{0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 3, 0, 4, 0, 0, 0}
			if !reflect.DeepEqual(p, want) {
				t.Fatalf("StartExposure2 packed parameters %x != %x", p, want)
			}
		case 12:
			if !reflect.DeepEqual(p, []byte{50, 0}) {
				t.Fatalf("polling wrong command %x", p)
			}
			if started {
				r[0] = 3
			}
		case 2, 25:
			if !reflect.DeepEqual(p, []byte{0, 0}) {
				t.Fatal("CCD flags must be zero")
			}
		case 35:
			if !reflect.DeepEqual(p, []byte{0, 0, 0, 0, 0, 0, 0, 0, 3, 0, 4, 0}) {
				t.Fatalf("readout geometry %x", p)
			}
		case 3:
			if !reflect.DeepEqual(p, []byte{0, 0, 0, 0, 0, 0, 4, 0}) || len(r) != 8 {
				t.Fatal("readout-line geometry")
			}
			for x := 0; x < 4; x++ {
				binary.LittleEndian.PutUint16(r[x*2:], uint16(row*4+x))
			}
			row++
		default:
			t.Fatalf("unexpected command %d", id)
		}
		return 0
	}}
	c := &Camera{t: m}
	pixels, err := c.Capture(context.Background(), Mode{0, 4, 3, "1x1"}, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pixels, []uint16{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}) {
		t.Fatal(pixels)
	}
	if !reflect.DeepEqual(m.calls, []uint16{11, 12, 50, 12, 2, 35, 3, 3, 3, 25}) {
		t.Fatal(m.calls)
	}
}

func TestCaptureFailuresCleanUpAndDiscardIncompleteImage(t *testing.T) {
	for _, failed := range []uint16{12, 2, 35, 3, 25} {
		t.Run(commandNames[failed], func(t *testing.T) {
			started := false
			m := &mockTransport{fn: func(id uint16, p, r []byte) int16 {
				if id == failed && started {
					return 8
				}
				switch id {
				case 50:
					started = true
				case 11:
					fillCCDInfo(r)
				case 12:
					if started {
						r[0] = 3
					}
				case 36:
					copy(r, "synthetic failure")
				}
				return 0
			}}
			c := &Camera{t: m}
			pixels, err := c.Capture(context.Background(), Mode{0, 4, 3, ""}, 0.01)
			if err == nil || pixels != nil {
				t.Fatalf("incomplete capture published: %v %v", pixels, err)
			}
			if m.calls[len(m.calls)-1] != 25 && !(failed == 25 && m.calls[len(m.calls)-2] == 25) {
				t.Fatalf("missing EndReadout cleanup %v", m.calls)
			}
			if failed == 3 && !strings.Contains(err.Error(), "row 0") {
				t.Fatal(err)
			}
		})
	}
}

func TestCaptureCancellationEndsExposureAndReadout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := false
	m := &mockTransport{fn: func(id uint16, p, r []byte) int16 {
		switch id {
		case 50:
			started = true
		case 11:
			fillCCDInfo(r)
		case 12:
			if started {
				r[0] = 2
				cancel()
			}
		}
		return 0
	}}
	c := &Camera{t: m}
	pixels, err := c.Capture(ctx, Mode{0, 4, 3, ""}, 0.01)
	if !errors.Is(err, context.Canceled) || pixels != nil {
		t.Fatalf("%v %v", pixels, err)
	}
	if !reflect.DeepEqual(m.calls[len(m.calls)-2:], []uint16{2, 25}) {
		t.Fatal(m.calls)
	}
}

func TestCaptureRejectsInvalidInputsBeforeCameraMutation(t *testing.T) {
	m := &mockTransport{fn: func(id uint16, p, r []byte) int16 {
		if id == 11 {
			fillCCDInfo(r)
			return 0
		}
		t.Fatalf("unexpected camera mutation %d", id)
		return 0
	}}
	c := &Camera{t: m}
	for _, seconds := range []float64{0, math.NaN(), math.Inf(1), -1, 3601} {
		if _, e := c.Capture(context.Background(), Mode{0, 4, 3, ""}, seconds); e == nil {
			t.Fatalf("accepted %v", seconds)
		}
	}
	if _, e := c.Capture(context.Background(), Mode{0, 5, 3, ""}, 1); e == nil {
		t.Fatal("accepted unreported geometry")
	}
	if !reflect.DeepEqual(m.calls, []uint16{11}) {
		t.Fatal(m.calls)
	}
}

func TestControlPackingAndWriteRestrictions(t *testing.T) {
	m := &mockTransport{fn: func(id uint16, p, r []byte) int16 {
		if id == 37 && !reflect.DeepEqual(p, []byte{8, 0, 0, 0, 1, 0, 0, 0}) {
			t.Fatalf("SetDriverControl %x", p)
		}
		if id == 38 {
			if !reflect.DeepEqual(p, []byte{8, 0}) || len(r) != 4 {
				t.Fatal("GetDriverControl ABI")
			}
			r[0] = 1
		}
		return 0
	}}
	c := &Camera{t: m}
	if e := c.SetControl(8, 1); e != nil {
		t.Fatal(e)
	}
	if n, e := c.GetControl(8); e != nil || n != 1 {
		t.Fatalf("%d %v", n, e)
	}
	if e := c.SetControl(12, 0); e == nil {
		t.Fatal("allowed clocking modification")
	}
	if e := c.SetControl(8, 2); e == nil {
		t.Fatal("allowed nonboolean value")
	}
	if len(m.calls) != 2 {
		t.Fatal(m.calls)
	}
}

func TestTemperatureFallbackDoesNotInventLaterFields(t *testing.T) {
	m := &mockTransport{fn: func(id uint16, p, r []byte) int16 {
		if id == 36 {
			return 0
		}
		if id != 6 {
			t.Fatal(id)
		}
		if u16(p, 0) == 2 {
			return 8
		}
		if len(r) != 104 {
			t.Fatal("advanced temp response too small")
		}
		binary.LittleEndian.PutUint16(r[0:], 0x8001)
		binary.LittleEndian.PutUint64(r[8:], math.Float64bits(-20))
		binary.LittleEndian.PutUint64(r[16:], math.Float64bits(-19.5))
		return 0
	}}
	c := &Camera{t: m}
	got := c.temperature()
	if got["imaging_ccd_c"] != -19.5 || got["cooling_frozen"] != true {
		t.Fatal(got)
	}
	if _, ok := got["fan_rpm"]; ok {
		t.Fatal("invented fan speed on request 1")
	}
}

func TestDevicePacking(t *testing.T) {
	p, e := deviceParams("ethernet", "192.168.0.1")
	if e != nil || !reflect.DeepEqual(p, []byte{1, 127, 0, 0, 1, 0, 168, 192}) {
		t.Fatalf("%x %v", p, e)
	}
	if _, e = deviceParams("usb25", ""); e == nil {
		t.Fatal("invalid USB slot accepted")
	}
	if _, e = deviceParams("ethernet", "somehost"); e == nil {
		t.Fatal("hostname accepted without explicit IPv4")
	}
}

func TestRepeatedCapturesWithLatchedCompleteStatus(t *testing.T) {
	starts := 0
	m := &mockTransport{fn: func(id uint16, p, r []byte) int16 {
		switch id {
		case 11:
			fillCCDInfo(r)
		case 12:
			// Complete remains latched through EndExposure and EndReadout.
			r[0] = 3
		case 50:
			starts++
		}
		return 0
	}}
	c := &Camera{t: m}
	for i := 0; i < 2; i++ {
		if pixels, err := c.Capture(context.Background(), Mode{0, 4, 3, ""}, 0.01); err != nil || len(pixels) != 12 {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	if starts != 2 {
		t.Fatal("second exposure was not started")
	}
}

func TestActiveExposureIsNotInterrupted(t *testing.T) {
	for _, state := range []byte{1, 2} {
		m := &mockTransport{fn: func(id uint16, p, r []byte) int16 {
			switch id {
			case 11:
				fillCCDInfo(r)
			case 12:
				r[0] = state
			default:
				t.Fatalf("mutated active exposure: command %d", id)
			}
			return 0
		}}
		c := &Camera{t: m}
		if _, err := c.Capture(context.Background(), Mode{0, 4, 3, ""}, 0.01); err == nil {
			t.Fatal("accepted active exposure")
		}
	}
}

func TestTraceIsJSONAndWrittenBeforeNativeCall(t *testing.T) {
	var log bytes.Buffer
	m := &mockTransport{fn: func(id uint16, p, r []byte) int16 {
		if !strings.Contains(log.String(), "begin CC_GET_DRIVER_CONTROL") {
			t.Fatal("no trace before the potentially blocking call")
		}
		return 0
	}}
	c := &Camera{t: m, trace: &log}
	if _, err := c.GetControl(8); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(log.Bytes()), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatal("expected call start and result")
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil || record["utc"] == nil || record["event"] == nil {
			t.Fatalf("invalid trace line: %s (%v)", line, err)
		}
	}
}
