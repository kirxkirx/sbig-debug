package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kirxkirx/sbig-debug/internal/diagnostic"
	"github.com/kirxkirx/sbig-debug/internal/sbig"
)

var version = "dev"

type config struct {
	out, dll, device, ip, action, test, exposures, modes string
	count                                                int
	timeout                                              time.Duration
	demo, noPause, showVersion                           bool
}

type report struct {
	Version   string             `json:"version"`
	Started   string             `json:"started_utc"`
	Finished  string             `json:"finished_utc"`
	OS        string             `json:"os"`
	Arch      string             `json:"arch"`
	Demo      bool               `json:"demo"`
	Action    string             `json:"action"`
	Settings  configView         `json:"settings"`
	Snapshots []snapshot         `json:"snapshots"`
	Frames    []diagnostic.Frame `json:"frames"`
	Notes     []string           `json:"notes"`
	Errors    []string           `json:"errors"`
}
type configView struct {
	Device, IP, DLL, Test, Exposures, Modes string
	Count                                   int
}
type snapshot struct {
	Label  string         `json:"label"`
	Time   string         `json:"utc"`
	Values map[string]any `json:"values"`
}

func main() {
	runtime.LockOSThread() // SBIG driver handles and all native calls stay on one OS thread.
	defer runtime.UnlockOSThread()
	os.Exit(entry())
}

func entry() int {
	setupConsole()
	var c config
	fs := flag.NewFlagSet("sbig-debug", flag.ContinueOnError)
	fs.StringVar(&c.out, "out", "", "new output directory (default: ~/SBIG-Diagnostics/run-TIMESTAMP)")
	fs.StringVar(&c.dll, "dll", "", "explicit full path to matching-bitness SBIGUDrv.dll")
	fs.StringVar(&c.device, "device", "usb", "usb auto-detect, usb1..usb24, or ethernet")
	fs.StringVar(&c.ip, "ip", "", "camera IPv4 address for --device ethernet")
	fs.StringVar(&c.action, "action", "info", "info, baseline, or compare")
	fs.StringVar(&c.test, "test", "vdd", "compare: vdd, throughput, conservative, or all")
	fs.StringVar(&c.exposures, "exposures", "0.01,5", "comma-separated dark exposure times in seconds, centisecond resolution")
	fs.StringVar(&c.modes, "modes", "0", "comma-separated SDK readout mode IDs (0=1x1, 1=2x2, 2=3x3 when supported)")
	fs.IntVar(&c.count, "count", 3, "consecutive frames per exposure and readout mode (1..100)")
	fs.DurationVar(&c.timeout, "timeout", 120*time.Second, "extra time allowed beyond exposure; cannot interrupt a hung native DLL call")
	fs.BoolVar(&c.demo, "demo", false, "generate synthetic diagnostic examples without loading a driver")
	fs.BoolVar(&c.noPause, "no-pause", false, "disable interactive menu and exit pause")
	fs.BoolVar(&c.showVersion, "version", false, "print version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Unexpected positional arguments:", fs.Args())
		return 2
	}
	if c.showVersion {
		fmt.Printf("SBIG Debug %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return 0
	}
	interactive := len(os.Args) == 1 && terminalInput()
	if interactive {
		fmt.Println("SBIG STXL diagnostic utility", version)
		fmt.Println("Close NINA, CCDOps, MaxIm and other camera programs before connecting.")
		fmt.Println("1  Collect camera/driver information only (default)")
		fmt.Println("2  Repeated dark frames at current settings (0.01 and 5 s)")
		fmt.Println("3  Compare Vdd optimization OFF, with baseline before and after")
		fmt.Println("4  Compare high throughput OFF, with baseline before and after")
		fmt.Println("5  Compare both settings OFF, with baseline before and after")
		fmt.Println("6  Repeated dark frames at 1x1, 2x2 and 3x3 (supported modes)")
		fmt.Println("7  Demo report, no camera")
		fmt.Print("Choose [1]: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		switch strings.TrimSpace(line) {
		case "", "1":
		case "2":
			c.action = "baseline"
		case "3":
			c.action = "compare"
		case "4":
			c.action = "compare"
			c.test = "throughput"
		case "5":
			c.action = "compare"
			c.test = "conservative"
		case "6":
			c.action = "baseline"
			c.modes = "0,1,2"
		case "7":
			c.demo = true
		default:
			fmt.Println("Unknown choice")
			return 2
		}
	}
	code := run(c)
	if interactive && !c.noPause {
		fmt.Print("Press Enter to close. ")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
	return code
}

func terminalInput() bool {
	s, e := os.Stdin.Stat()
	return e == nil && s.Mode()&os.ModeCharDevice != 0
}

func validate(c config) ([]float64, []uint16, error) {
	if c.action != "info" && c.action != "baseline" && c.action != "compare" {
		return nil, nil, fmt.Errorf("invalid --action %q", c.action)
	}
	if c.test != "vdd" && c.test != "throughput" && c.test != "conservative" && c.test != "all" {
		return nil, nil, fmt.Errorf("invalid --test %q", c.test)
	}
	if c.count < 1 || c.count > 100 {
		return nil, nil, fmt.Errorf("--count must be 1..100")
	}
	if c.timeout < time.Second || c.timeout > time.Hour {
		return nil, nil, fmt.Errorf("--timeout must be 1s..1h")
	}
	var exposures []float64
	for _, s := range strings.Split(c.exposures, ",") {
		f, e := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0.01 || f > 3600 || math.Abs(f*100-math.Round(f*100)) > 1e-7 {
			return nil, nil, fmt.Errorf("exposures must be 0.01..3600 seconds in 0.01 second steps: %q", s)
		}
		exposures = append(exposures, f)
	}
	var modes []uint16
	for _, s := range strings.Split(c.modes, ",") {
		n, e := strconv.ParseUint(strings.TrimSpace(s), 10, 16)
		if e != nil {
			return nil, nil, fmt.Errorf("invalid readout mode ID %q", s)
		}
		modes = append(modes, uint16(n))
	}
	if len(exposures) > 20 || len(modes) > 20 {
		return nil, nil, fmt.Errorf("limit exposure and mode lists to 20 entries each")
	}
	return exposures, modes, nil
}

func run(c config) int {
	exposures, modeIDs, err := validate(c)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if c.out == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			home = "."
		}
		c.out = filepath.Join(home, "SBIG-Diagnostics", "run-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	}
	c.out, err = filepath.Abs(c.out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// Never overwrite an earlier diagnostic run.
	if entries, e := os.ReadDir(c.out); e == nil && len(entries) > 0 {
		fmt.Fprintln(os.Stderr, "Output directory is not empty; choose a new --out directory:", c.out)
		return 2
	}
	if err = os.MkdirAll(c.out, 0755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	logFile, err := os.Create(filepath.Join(c.out, "console.log"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer logFile.Close()
	log := io.MultiWriter(os.Stdout, logFile)
	trace, err := os.Create(filepath.Join(c.out, "driver-calls.jsonl"))
	if err != nil {
		fmt.Fprintln(log, err)
		return 1
	}
	defer trace.Close()
	r := report{Version: version, Started: time.Now().UTC().Format(time.RFC3339Nano), OS: runtime.GOOS, Arch: runtime.GOARCH, Demo: c.demo, Action: c.action, Settings: configView{Device: c.device, IP: c.ip, DLL: c.dll, Test: c.test, Exposures: c.exposures, Modes: c.modes, Count: c.count}}
	fmt.Fprintf(log, "SBIG Debug %s | %s/%s\nOutput: %s\n", version, runtime.GOOS, runtime.GOARCH, c.out)
	fmt.Fprintln(log, "Reports remain on this computer. No automatic upload. No firmware or driver installation.")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if c.demo {
		err = demo(c.out, &r, log)
	} else {
		err = hardware(ctx, c, exposures, modeIDs, &r, log, trace)
	}
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		fmt.Fprintln(log, "ERROR:", err)
	}
	r.Finished = time.Now().UTC().Format(time.RFC3339Nano)
	if e := saveReport(c.out, r); e != nil {
		fmt.Fprintln(log, "Cannot write report:", e)
		return 1
	}
	fmt.Fprintln(log, "Report:", filepath.Join(c.out, "report.html"))
	fmt.Fprintln(log, "Keep the entire output folder: raw FITS, previews, CSV profiles, report.json, and driver trace.")
	if err != nil {
		return 1
	}
	return 0
}

type camera interface {
	Close() error
	Snapshot() map[string]any
	Modes() ([]sbig.Mode, error)
	GetControl(uint16) (int32, error)
	SetControl(uint16, int32) error
	Capture(context.Context, sbig.Mode, float64) ([]uint16, error)
}

func hardware(ctx context.Context, c config, exposures []float64, ids []uint16, r *report, log, trace io.Writer) (err error) {
	fmt.Fprintln(log, "Close other camera clients. Cooler settings are left as found; all acquisitions use the closed shutter.")
	cam, err := sbig.Open(sbig.Options{DLL: c.dll, Device: c.device, IP: c.ip, Trace: trace})
	if err != nil {
		return err
	}
	defer func() {
		if e := cam.Close(); e != nil {
			err = errors.Join(err, fmt.Errorf("camera cleanup: %w", e))
		}
	}()
	return session(ctx, cam, c, exposures, ids, r, log)
}

type profile struct {
	name    string
	changes []controlChange
}
type controlChange struct {
	id    uint16
	value int32
}

func profiles(c config) []profile {
	p := []profile{{name: "baseline-before"}}
	if c.action != "compare" {
		p[0].name = "baseline"
		return p
	}
	if c.test == "vdd" || c.test == "all" {
		p = append(p, profile{"vdd-off", []controlChange{{8, 0}}})
	}
	if c.test == "throughput" || c.test == "all" {
		p = append(p, profile{"throughput-off", []controlChange{{7, 0}}})
	}
	if c.test == "conservative" || c.test == "all" {
		p = append(p, profile{"vdd-and-throughput-off", []controlChange{{8, 0}, {7, 0}}})
	}
	return append(p, profile{name: "baseline-after"})
}

func addSnapshot(cam camera, r *report, label string, log io.Writer) {
	s := snapshot{Label: label, Time: time.Now().UTC().Format(time.RFC3339Nano), Values: cam.Snapshot()}
	r.Snapshots = append(r.Snapshots, s)
	b, e := json.MarshalIndent(s, "", "  ")
	if e == nil {
		fmt.Fprintln(log, string(b))
	}
}

func session(ctx context.Context, cam camera, c config, exposures []float64, ids []uint16, r *report, log io.Writer) (err error) {
	addSnapshot(cam, r, "initial", log)
	if c.action == "info" {
		return nil
	}
	// This diagnostic must not present synthetic or TDI pixels as camera images.
	if v, e := cam.GetControl(6); e != nil {
		return fmt.Errorf("cannot verify pixel download is enabled: %w", e)
	} else if v == 0 {
		return fmt.Errorf("DCP_USB_PIXEL_DL_ENABLE is OFF; refusing image acquisition")
	}
	if v, e := cam.GetControl(11); e == nil && v != 0 {
		return fmt.Errorf("DCP_TDI_MODE_ENABLE is ON; normal frame acquisition is unsafe in this mode")
	}
	available, err := cam.Modes()
	if err != nil {
		return err
	}
	var selected []sbig.Mode
	for _, id := range ids {
		found := false
		for _, m := range available {
			if m.ID == id {
				selected = append(selected, m)
				found = true
				break
			}
		}
		if !found {
			note := fmt.Sprintf("Readout mode %d is not advertised; skipped.", id)
			r.Notes = append(r.Notes, note)
			fmt.Fprintln(log, note)
		}
	}
	if len(selected) == 0 {
		return errors.New("none of the requested readout modes are available")
	}
	plan := profiles(c)
	var bytes int64
	for _, m := range selected {
		bytes += int64(m.Width) * int64(m.Height) * 2 * int64(c.count*len(exposures)*len(plan))
	}
	fmt.Fprintf(log, "Planned: %d frames, approximately %.0f MiB raw pixels plus previews. Ctrl+C requests cleanup.\n", len(selected)*len(exposures)*c.count*len(plan), float64(bytes)/(1024*1024))
	seq := 0
	for _, p := range plan {
		if err = ctx.Err(); err != nil {
			return err
		}
		fmt.Fprintln(log, "Profile:", p.name)
		if err = runProfile(ctx, cam, c, p, selected, exposures, r, log, &seq); err != nil {
			return err
		}
	}
	addSnapshot(cam, r, "final-restored", log)
	return nil
}

func runProfile(ctx context.Context, cam camera, c config, p profile, modes []sbig.Mode, exposures []float64, r *report, log io.Writer, seq *int) (err error) {
	var restore []controlChange
	defer func() {
		for i := len(restore) - 1; i >= 0; i-- {
			q := restore[i]
			if e := cam.SetControl(q.id, q.value); e != nil {
				err = errors.Join(err, fmt.Errorf("RESTORE FAILED control %d to %d: %w", q.id, q.value, e))
				continue
			}
			v, e := cam.GetControl(q.id)
			if e != nil || v != q.value {
				err = errors.Join(err, fmt.Errorf("restore verification failed control %d: got %d (%v), expected %d", q.id, v, e, q.value))
			}
		}
	}()
	for _, q := range p.changes {
		old, e := cam.GetControl(q.id)
		if e != nil {
			return fmt.Errorf("cannot safely test control %d without its original value: %w", q.id, e)
		}
		if old != 0 && old != 1 {
			return fmt.Errorf("control %d returned unexpected boolean value %d; refusing a change that cannot be restored safely", q.id, old)
		}
		if old == q.value {
			note := fmt.Sprintf("%s: control %d is already %d; this block does not change it.", p.name, q.id, old)
			r.Notes = append(r.Notes, note)
			fmt.Fprintln(log, note)
			continue
		}
		restore = append(restore, controlChange{q.id, old}) // Also restore if a failed write partly applied.
		if e = cam.SetControl(q.id, q.value); e != nil {
			return fmt.Errorf("set control %d: %w", q.id, e)
		}
		got, e := cam.GetControl(q.id)
		if e != nil || got != q.value {
			return fmt.Errorf("control %d verification: got %d (%v), expected %d", q.id, got, e, q.value)
		}
	}
	addSnapshot(cam, r, p.name, log)
	for _, m := range modes {
		for _, secs := range exposures {
			for n := 0; n < c.count; n++ {
				if err = ctx.Err(); err != nil {
					return err
				}
				*seq++
				stem := fmt.Sprintf("%03d_%s_mode%d_%.2fs_%02d", *seq, p.name, m.ID, secs, n+1)
				fmt.Fprintf(log, "Acquiring %s (%dx%d), shutter closed...\n", stem, m.Width, m.Height)
				before := cam.Snapshot()
				started := time.Now()
				frameCtx, cancel := context.WithTimeout(ctx, time.Duration(secs*float64(time.Second))+c.timeout)
				pixels, e := cam.Capture(frameCtx, m, secs)
				cancel()
				captureElapsed := time.Since(started).Seconds()
				after := cam.Snapshot()
				if e != nil {
					r.Snapshots = append(r.Snapshots, snapshot{Label: stem + "-failed", Time: time.Now().UTC().Format(time.RFC3339Nano), Values: after})
					return fmt.Errorf("%s: %w", stem, e)
				}
				meta := map[string]any{"profile": p.name, "mode": m.ID, "exposure_seconds": secs, "EXPTIME": secs, "IMAGETYP": "Dark Frame", "DATE-OBS": started.UTC().Format(time.RFC3339Nano), "capture_elapsed_seconds": captureElapsed, "before": before, "after": after, "version": version, "software": "SBIG Debug " + version}
				if m.ID <= 2 {
					meta["binning"] = int(m.ID) + 1
				}
				if info, ok := before["imaging_ccd"].(map[string]any); ok {
					meta["camera_name"] = info["name"]
				}
				if temp, ok := before["temperature"].(map[string]any); ok {
					if value, exists := temp["imaging_ccd_c"]; exists {
						meta["ccd_temperature_c"] = value
					}
				}
				frame, e := diagnostic.SaveFrame(c.out, stem, pixels, int(m.Width), int(m.Height), meta)
				if e != nil {
					return fmt.Errorf("saving %s: %w", stem, e)
				}
				r.Frames = append(r.Frames, frame)
				b, _ := json.Marshal(frame.Stats)
				fmt.Fprintln(log, string(b))
				// Save after every frame so a later acquisition failure leaves usable evidence.
				if e = saveReport(c.out, *r); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

func demo(out string, r *report, log io.Writer) error {
	r.Notes = append(r.Notes, "SIMULATED DATA ONLY: this run did not load SBIGUDrv or connect to a camera.")
	r.Snapshots = append(r.Snapshots, snapshot{Label: "simulation", Time: time.Now().UTC().Format(time.RFC3339Nano), Values: map[string]any{"camera": "synthetic STXL-like corruption sequence", "actual_hardware_test": false}})
	const w, h = 320, 200
	for i, name := range []string{"synthetic-normal", "synthetic-bottom-stripe", "synthetic-saturated"} {
		pixels := make([]uint16, w*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				v := 1000 + (x*31+y*17)%101
				if i == 1 && y >= 140 {
					v = 62000 + (x*3+y)%101
				}
				if i == 2 {
					v = 65535
				}
				pixels[y*w+x] = uint16(v)
			}
		}
		f, e := diagnostic.SaveFrame(out, name, pixels, w, h, map[string]any{"simulated": true, "profile": name, "EXPTIME": 0.01, "exposure_seconds": 0.01, "IMAGETYP": "SIMULATED"})
		if e != nil {
			return e
		}
		r.Frames = append(r.Frames, f)
		fmt.Fprintln(log, "Saved", name)
	}
	return nil
}
