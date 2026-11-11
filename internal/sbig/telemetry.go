package sbig

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Monitor observes the one serialized native caller without issuing driver calls.
// It can be shared with a watchdog before Open, including DLL initialization.
type Monitor struct{ active atomic.Pointer[busyCall] }
type busyCall struct {
	name  string
	start time.Time
}

func (m *Monitor) begin(name string) {
	if m != nil {
		m.active.Store(&busyCall{name: name, start: time.Now()})
	}
}
func (m *Monitor) end() {
	if m != nil {
		m.active.Store(nil)
	}
}

func (m *Monitor) Busy() (name string, started time.Time, busy bool) {
	if m != nil {
		if c := m.active.Load(); c != nil {
			return c.name, c.start, true
		}
	}
	return "", time.Time{}, false
}

// PhaseObservation is a sampled status, not an exact hardware transition time.
// Info retains every driver bit; Phase describes its imaging-CCD low two bits.
type PhaseObservation struct {
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	Status         uint16  `json:"status_raw"`
	Info           uint16  `json:"info_raw"`
	Phase          string  `json:"imaging_phase"`
}

type CaptureTelemetry struct {
	LineMilliseconds    []float64          `json:"line_milliseconds,omitempty"`
	CompletedRows       int                `json:"completed_rows"`
	ExpectedRows        int                `json:"expected_rows"`
	ExposureWaitSeconds float64            `json:"exposure_wait_seconds"`
	ReadoutSeconds      float64            `json:"readout_seconds"`
	Phases              []PhaseObservation `json:"phase_observations,omitempty"`
	Status2Available    bool               `json:"extended_status_available"`
	Status2Error        string             `json:"extended_status_error,omitempty"`
	PhaseNote           string             `json:"phase_note"`
}

// LastCapture must be called on the serialized camera thread. Returned slices
// are copies so saving reports cannot mutate the next capture's telemetry.
func (c *Camera) LastCapture() CaptureTelemetry {
	t := c.lastCapture
	t.LineMilliseconds = append([]float64(nil), t.LineMilliseconds...)
	t.Phases = append([]PhaseObservation(nil), t.Phases...)
	return t
}

type DriverError struct {
	Command uint16
	Code    int16
	Message string
}

func (e *DriverError) Error() string {
	return fmt.Sprintf("%s (%d): SBIG error %d: %s", commandNames[e.Command], e.Command, e.Code, e.Message)
}
