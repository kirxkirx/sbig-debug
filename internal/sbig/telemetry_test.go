package sbig

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func telemetryTransport(status2Code int16) *mockTransport {
	return &mockTransport{fn: func(id uint16, p, r []byte) int16 {
		switch id {
		case 11:
			fillCCDInfo(r)
		case 12:
			r[0] = 3
		case 61:
			if status2Code != 0 {
				return status2Code
			}
			put16(r, 0, 3)
			put16(r, 2, 3)
		case 36:
			copy(r, "injected status error")
		}
		return 0
	}}
}

func TestCaptureTelemetryAndUnsupportedStatusFallback(t *testing.T) {
	for _, code := range []int16{0, 4, 5, 8} {
		m := telemetryTransport(code)
		c := &Camera{t: m}
		pixels, err := c.Capture(context.Background(), Mode{0, 4, 3, ""}, 0.01)
		timing := c.LastCapture()
		if code == 8 {
			var de *DriverError
			if !errors.As(err, &de) || de.Code != 8 || pixels != nil || timing.CompletedRows != 0 {
				t.Fatalf("transport failure was swallowed: %v %+v", err, timing)
			}
			if m.calls[len(m.calls)-2] != 2 || m.calls[len(m.calls)-1] != 25 {
				t.Fatal("failed status query did not clean up", m.calls)
			}
			if timing.ExposureWaitSeconds <= 0 {
				t.Fatal("failed exposure wait not recorded")
			}
			continue
		}
		if err != nil || len(pixels) != 12 || timing.CompletedRows != 3 || len(timing.LineMilliseconds) != 3 {
			t.Fatalf("capture telemetry: %v %+v", err, timing)
		}
		if code == 0 {
			if !timing.Status2Available || len(timing.Phases) != 1 || timing.Phases[0].Phase != "post-exposure" {
				t.Fatal(timing)
			}
			timing.Phases[0].Phase = "mutated"
			if c.LastCapture().Phases[0].Phase == "mutated" {
				t.Fatal("phase copy aliases camera state")
			}
		} else if timing.Status2Available || timing.Status2Error == "" {
			t.Fatal("unsupported extended status was presented as available", timing)
		}
		timing.LineMilliseconds[0] = -1
		if c.LastCapture().LineMilliseconds[0] < 0 {
			t.Fatal("line timing copy aliases camera state")
		}
		m.calls = nil
		if _, err = c.Capture(context.Background(), Mode{0, 4, 3, ""}, 0.01); err != nil {
			t.Fatal(err)
		}
		if code != 0 {
			for _, id := range m.calls {
				if id == 61 {
					t.Fatal("unsupported command unnecessarily retried on later capture")
				}
			}
		}
	}
}

func TestMonitorObservesCallWithoutCallingDriver(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	m := &mockTransport{fn: func(id uint16, p, r []byte) int16 {
		close(entered)
		<-release
		return 0
	}}
	monitor := &Monitor{}
	c := &Camera{t: m, monitor: monitor}
	done := make(chan error, 1)
	go func() { done <- c.command(12, word(50), make([]byte, 2)) }()
	<-entered
	name, started, busy := monitor.Busy()
	if !busy || name != "CC_QUERY_COMMAND_STATUS" || started.IsZero() {
		t.Errorf("missing active call: %s %v %v", name, started, busy)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, _, busy = monitor.Busy(); busy || len(m.calls) != 1 {
		t.Fatal("monitor changed driver traffic or retained completed call")
	}
}

type slowLineTrace struct{}

func (slowLineTrace) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "begin CC_READOUT_LINE") {
		time.Sleep(100 * time.Millisecond)
	}
	return len(p), nil
}

func TestLineTimingsExcludeSlowTraceWrites(t *testing.T) {
	c := &Camera{t: telemetryTransport(0), trace: slowLineTrace{}}
	if _, err := c.Capture(context.Background(), Mode{0, 4, 3, ""}, 0.01); err != nil {
		t.Fatal(err)
	}
	for _, ms := range c.LastCapture().LineMilliseconds {
		if ms >= 100 {
			t.Fatalf("trace delay contaminated native line timing: %.1f ms", ms)
		}
	}
}
