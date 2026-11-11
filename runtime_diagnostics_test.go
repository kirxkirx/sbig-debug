package main

import (
	"strings"
	"testing"
	"time"
)

type stalledMonitor struct{ start time.Time }

func (m stalledMonitor) Busy() (string, time.Time, bool) { return "TEST_NATIVE_CALL", m.start, true }

type notificationWriter struct{ messages chan string }

func (w notificationWriter) Write(b []byte) (int, error) {
	w.messages <- string(b)
	return len(b), nil
}

func TestWatchdogReportsStalledNativeCallAndStops(t *testing.T) {
	messages := make(chan string, 10)
	stop := watchNativeCalls(stalledMonitor{time.Now().Add(-time.Minute)}, notificationWriter{messages}, time.Millisecond, time.Second)
	select {
	case message := <-messages:
		if !strings.Contains(message, "TEST_NATIVE_CALL") || !strings.Contains(message, "cannot be safely interrupted") {
			t.Fatal(message)
		}
	case <-time.After(3 * time.Second):
		stop()
		t.Fatal("no watchdog warning")
	}
	stop()
	if len(messages) != 0 {
		t.Fatal("repeated warning for the same native call")
	}
}

func TestLinkCounterChangesRejectUnavailableAndReset(t *testing.T) {
	snapshot := func(total, failed uint32) map[string]any {
		return map[string]any{"link": map[string]any{"communications_total": total, "communications_failed": failed}}
	}
	got := linkCounterChange(snapshot(12, 1), snapshot(16, 3))
	if got["available"] != true || got["communications_total"] != uint32(4) || got["communications_failed"] != uint32(2) {
		t.Fatal(got)
	}
	for _, after := range []map[string]any{snapshot(2, 0), {"link": map[string]any{"available": false}}, nil} {
		if got := linkCounterChange(snapshot(12, 1), after); got["available"] != false {
			t.Fatalf("unknown counter became a measurement: %v", got)
		}
	}
}
