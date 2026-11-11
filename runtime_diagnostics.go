package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
	"time"
)

type lockedWriter struct {
	mu  sync.Mutex
	dst io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dst.Write(p)
}

func captureCrashes(dir string, log io.Writer) func() {
	f, err := os.OpenFile(filepath.Join(dir, "crash.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fmt.Fprintln(log, "Cannot create crash log:", err)
		return func() {}
	}
	err = debug.SetCrashOutput(f, debug.CrashOptions{})
	f.Close() // SetCrashOutput owns a duplicate descriptor.
	if err != nil {
		fmt.Fprintln(log, "Cannot enable Go fatal-error capture:", err)
		return func() {}
	}
	return func() { _ = debug.SetCrashOutput(nil, debug.CrashOptions{}) }
}

type busyMonitor interface {
	Busy() (string, time.Time, bool)
}

// The watchdog observes atomic state only. It never calls the driver or tries to
// interrupt native code. Joining on shutdown prevents writes after the log closes.
func watchNativeCalls(monitor busyMonitor, log io.Writer, interval, threshold time.Duration) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var warned time.Time
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				name, started, busy := monitor.Busy()
				if busy && now.Sub(started) >= threshold && !started.Equal(warned) {
					warned = started
					fmt.Fprintf(log, "%s WARNING: %s has not returned for %.0f seconds. The native call cannot be safely interrupted; the driver trace records the last call entered. Keep this output folder.\n", now.UTC().Format(time.RFC3339), name, now.Sub(started).Seconds())
				}
			}
		}
	}()
	return func() { close(stop); <-done }
}

func saveLineTimings(path string, milliseconds []float64) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	err = w.Write([]string{"row_zero_based", "driver_call_milliseconds"})
	for row, ms := range milliseconds {
		if err != nil {
			break
		}
		err = w.Write([]string{strconv.Itoa(row), strconv.FormatFloat(ms, 'f', 6, 64)})
	}
	w.Flush()
	if err == nil {
		err = w.Error()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func linkCounterChange(before, after map[string]any) map[string]any {
	b, bok := before["link"].(map[string]any)
	a, aok := after["link"].(map[string]any)
	out := map[string]any{"available": false}
	if !bok || !aok {
		return out
	}
	for _, key := range []string{"communications_total", "communications_failed"} {
		old, ok1 := b[key].(uint32)
		now, ok2 := a[key].(uint32)
		if !ok1 || !ok2 {
			return map[string]any{"available": false}
		}
		if now < old {
			return map[string]any{"available": false, "note": "Counter decreased; reset or wrap cannot be distinguished."}
		}
		out[key] = now - old
	}
	out["available"] = true
	out["note"] = "Snapshot-to-snapshot change, including telemetry queries; not exclusively image-transfer commands."
	return out
}
