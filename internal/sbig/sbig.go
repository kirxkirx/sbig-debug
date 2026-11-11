// Package sbig implements the documented Windows SBIG Universal Driver ABI.
// All calls for a camera must be serialized on one locked OS thread. No driver
// binaries are bundled. ABI source: vendor WinDevKit.zip, header dated 2019-04-17.
package sbig

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	DLL     string
	Device  string
	IP      string
	Trace   io.Writer
	Monitor *Monitor
}

type Mode struct {
	ID     uint16 `json:"id"`
	Width  uint16 `json:"width"`
	Height uint16 `json:"height"`
	Name   string `json:"name"`
}

// ControlNames follows the vendor's DRIVER_CONTROL_PARAM enumeration. A
// successful read only means the DLL recognizes it, not that this camera uses it.
var ControlNames = []string{
	"DCP_USB_FIFO_ENABLE", "DCP_CALL_JOURNAL_ENABLE", "DCP_IVTOH_RATIO",
	"DCP_USB_FIFO_SIZE", "DCP_USB_DRIVER", "DCP_KAI_RELGAIN", "DCP_USB_PIXEL_DL_ENABLE",
	"DCP_HIGH_THROUGHPUT", "DCP_VDD_OPTIMIZED", "DCP_AUTO_AD_GAIN",
	"DCP_NO_HCLKS_FOR_INTEGRATION", "DCP_TDI_MODE_ENABLE", "DCP_VERT_FLUSH_CONTROL_ENABLE",
	"DCP_ETHERNET_PIPELINE_ENABLE", "DCP_FAST_LINK", "DCP_OVERSCAN_ROWSCOLS",
	"DCP_PIXEL_PIPELINE_ENABLE", "DCP_COLUMN_REPAIR_ENABLE", "DCP_WARM_PIXEL_REPAIR_ENABLE",
	"DCP_WARM_PIXEL_REPAIR_COUNT", "DCP_TDI_MODE_DRIFT_RATE", "DCP_OVERRIDE_AD_GAIN", "DCP_ENABLE_AUTO_OFFSET",
}

type transport interface {
	call(uint16, []byte, []byte) int16
	close() error
	metadata() map[string]any
}

type Camera struct {
	t                              transport
	trace                          io.Writer
	driverOpen, deviceOpen, closed bool
	device                         string
	ip                             string
	monitor                        *Monitor
	lastCapture                    CaptureTelemetry
	lastNativeDuration             time.Duration
	status2Unsupported             string
}

var commandNames = map[uint16]string{
	2: "CC_END_EXPOSURE", 3: "CC_READOUT_LINE", 6: "CC_QUERY_TEMPERATURE_STATUS",
	9: "CC_ESTABLISH_LINK", 10: "CC_GET_DRIVER_INFO", 11: "CC_GET_CCD_INFO",
	12: "CC_QUERY_COMMAND_STATUS", 17: "CC_OPEN_DRIVER", 18: "CC_CLOSE_DRIVER",
	25: "CC_END_READOUT", 27: "CC_OPEN_DEVICE", 28: "CC_CLOSE_DEVICE",
	32: "CC_GET_LINK_STATUS", 35: "CC_START_READOUT", 36: "CC_GET_ERROR_STRING",
	37: "CC_SET_DRIVER_CONTROL", 38: "CC_GET_DRIVER_CONTROL", 44: "CC_BIT_IO",
	50: "CC_START_EXPOSURE2", 55: "CC_CUSTOMER_OPTIONS", 61: "CC_QUERY_COMMAND_STATUS2",
}

var le = binary.LittleEndian

func u16(b []byte, off int) uint16      { return le.Uint16(b[off:]) }
func u32(b []byte, off int) uint32      { return le.Uint32(b[off:]) }
func put16(b []byte, off int, n uint16) { le.PutUint16(b[off:], n) }
func put32(b []byte, off int, n uint32) { le.PutUint32(b[off:], n) }
func word(n uint16) []byte              { b := make([]byte, abiWordSize); put16(b, 0, n); return b }
func cstring(b []byte) string {
	if n := bytes.IndexByte(b, 0); n >= 0 {
		b = b[:n]
	}
	return string(b)
}
func bcd(n uint32) float64 {
	var v, scale uint32 = 0, 1
	for n > 0 {
		if n&15 > 9 {
			return -1
		}
		v += (n & 15) * scale
		scale *= 10
		n >>= 4
	}
	return float64(v) / 100
}

// Open loads the vendor DLL, opens the selected USB/Ethernet device and links it.
// Device accepts usb, usb1 through usb24, or ethernet (requires numeric IPv4).
func Open(opt Options) (*Camera, error) {
	p, err := deviceParams(opt.Device, opt.IP)
	if err != nil {
		return nil, err
	}
	opt.Monitor.begin("Load SBIG driver DLL")
	t, err := loadTransport(opt.DLL)
	opt.Monitor.end()
	if err != nil {
		return nil, err
	}
	c := &Camera{t: t, trace: opt.Trace, device: opt.Device, ip: opt.IP, monitor: opt.Monitor}
	if err = c.command(17, nil, nil); err != nil {
		_ = t.close()
		return nil, err
	}
	c.driverOpen = true
	if err = c.command(27, p, nil); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	c.deviceOpen = true
	if err = c.command(9, word(0), make([]byte, abiWordSize)); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	return c, nil
}

func deviceParams(device, ipText string) ([]byte, error) {
	d := strings.ToLower(device)
	p := make([]byte, abiOpenDevice.size)
	switch {
	case d == "" || d == "usb":
		put16(p, abiOpenDevice.off("deviceType"), 0x7f00)
	case d == "ethernet":
		ip := net.ParseIP(ipText).To4()
		if ip == nil {
			return nil, errors.New("ethernet requires a numeric IPv4 address")
		}
		put16(p, abiOpenDevice.off("deviceType"), 0x7f01)
		put32(p, abiOpenDevice.off("ipAddress"), binary.BigEndian.Uint32(ip))
	case strings.HasPrefix(d, "usb"):
		n, err := strconv.Atoi(strings.TrimPrefix(d, "usb"))
		if err != nil || n < 1 || n > 24 {
			return nil, errors.New("device must be usb, usb1..usb24, or ethernet")
		}
		put16(p, abiOpenDevice.off("deviceType"), uint16(0x7f01+n))
	default:
		return nil, errors.New("device must be usb, usb1..usb24, or ethernet")
	}
	if ipText != "" && d != "ethernet" {
		return nil, errors.New("IP address is only applicable to ethernet")
	}
	return p, nil
}

func (c *Camera) log(format string, args ...any) {
	if c.trace != nil {
		_ = json.NewEncoder(c.trace).Encode(map[string]any{"utc": time.Now().UTC().Format(time.RFC3339Nano), "event": fmt.Sprintf(format, args...)})
		if flush, ok := c.trace.(interface{ Flush() error }); ok {
			_ = flush.Flush()
		}
	}
}
func (c *Camera) command(id uint16, p, r []byte) error { return c.call(id, p, r, true) }
func (c *Camera) call(id uint16, p, r []byte, verbose bool) error {
	if c.closed {
		return errors.New("camera is closed")
	}
	start := time.Now()
	if verbose {
		c.log("begin %s (%d) params=%x", commandNames[id], id, p)
	}
	c.monitor.begin(commandNames[id])
	nativeStart := time.Now()
	code := c.t.call(id, p, r)
	c.lastNativeDuration = time.Since(nativeStart)
	c.monitor.end()
	if verbose || code != 0 {
		c.log("%s (%d) rc=%d elapsed_ms=%.3f params=%x", commandNames[id], id, code, float64(time.Since(start).Microseconds())/1000, p)
	}
	if code == 0 {
		return nil
	}
	b := make([]byte, abiError.size)
	msg := "driver error"
	if id != 36 {
		c.log("begin %s (%d) params=%x", commandNames[36], 36, word(uint16(code)))
		c.monitor.begin(commandNames[36])
		errCode := c.t.call(36, word(uint16(code)), b)
		c.monitor.end()
		if errCode == 0 {
			msg = cstring(b)
		}
	}
	return &DriverError{Command: id, Code: code, Message: msg}
}

func (c *Camera) Close() error {
	if c == nil || c.closed {
		return nil
	}
	var errs []error
	if c.deviceOpen {
		errs = append(errs, c.command(28, nil, nil))
		c.deviceOpen = false
	}
	if c.driverOpen {
		errs = append(errs, c.command(18, nil, nil))
		c.driverOpen = false
	}
	c.closed = true
	errs = append(errs, c.t.close())
	return errors.Join(errs...)
}

func (c *Camera) GetControl(id uint16) (int32, error) {
	r := make([]byte, abiGetControl.size)
	err := c.command(38, word(id), r)
	return int32(u32(r, abiGetControl.off("controlValue"))), err
}

func (c *Camera) SetControl(id uint16, value int32) error {
	// Restrict the write surface to the two documented experiments. Clocking,
	// gain, EEPROM, firmware, cooler and persistent repair settings are untouched.
	if id != 7 && id != 8 {
		return fmt.Errorf("control %d is read-only in this utility", id)
	}
	if value != 0 && value != 1 {
		return errors.New("control value must be 0 or 1")
	}
	p := make([]byte, abiSetControl.size)
	put16(p, abiSetControl.off("controlParameter"), id)
	put32(p, abiSetControl.off("controlValue"), uint32(value))
	return c.command(37, p, nil)
}

func (c *Camera) ccdInfo(request uint16) (map[string]any, []Mode, error) {
	r := make([]byte, abiCCDInfo.size) // Windows pack(8): readoutInfo starts at 72, 20 x 16.
	if err := c.command(11, word(request), r); err != nil {
		return nil, nil, err
	}
	n := int(u16(r, abiCCDInfo.off("readoutModes")))
	if n > 20 {
		return nil, nil, fmt.Errorf("driver returned invalid readout-mode count %d", n)
	}
	modes := make([]Mode, 0, n)
	detail := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		o := abiCCDInfo.off("readoutInfo") + abiReadoutInfo.size*i
		id := u16(r, o+abiReadoutInfo.off("mode"))
		name := fmt.Sprintf("mode %d", id)
		if id < 3 {
			name = fmt.Sprintf("%dx%d", id+1, id+1)
		}
		m := Mode{id, u16(r, o+abiReadoutInfo.off("width")), u16(r, o+abiReadoutInfo.off("height")), name}
		modes = append(modes, m)
		detail = append(detail, map[string]any{"id": id, "name": name, "width": m.Width, "height": m.Height, "gain_e_per_adu": bcd(uint32(u16(r, o+abiReadoutInfo.off("gain")))), "pixel_width_um": bcd(u32(r, o+abiReadoutInfo.off("pixel_width"))), "pixel_height_um": bcd(u32(r, o+abiReadoutInfo.off("pixel_height")))})
	}
	return map[string]any{"name": cstring(r[abiCCDInfo.off("name"):abiCCDInfo.off("readoutModes")]), "camera_type": u16(r, abiCCDInfo.off("cameraType")), "firmware_bcd": fmt.Sprintf("0x%04x", u16(r, abiCCDInfo.off("firmwareVersion"))), "firmware_version": fmt.Sprintf("%.2f", bcd(uint32(u16(r, abiCCDInfo.off("firmwareVersion"))))), "readout_modes": detail}, modes, nil
}

func (c *Camera) Modes() ([]Mode, error) { _, m, e := c.ccdInfo(0); return m, e }

// Snapshot performs only query commands. Unsupported responses remain explicit
// errors instead of plausible-looking zero measurements.
func (c *Camera) Snapshot() map[string]any {
	out := map[string]any{"utc": time.Now().UTC().Format(time.RFC3339Nano), "dll": c.t.metadata(), "device": c.device, "ip": c.ip}
	optional := func(key string, cmd uint16, p []byte, n int, decode func([]byte) any) {
		r := make([]byte, n)
		if e := c.command(cmd, p, r); e != nil {
			out[key] = map[string]any{"available": false, "error": e.Error()}
			return
		}
		out[key] = decode(r)
	}
	for req, name := range []string{"driver_standard", "driver_extended", "driver_usb_loader"} {
		optional(name, 10, word(uint16(req)), abiDriverInfo.size, func(r []byte) any {
			return map[string]any{"name": cstring(r[abiDriverInfo.off("name"):abiDriverInfo.off("maxRequest")]), "version_bcd": fmt.Sprintf("0x%04x", u16(r, abiDriverInfo.off("version"))), "version": fmt.Sprintf("%.2f", bcd(uint32(u16(r, abiDriverInfo.off("version"))))), "max_request": u16(r, abiDriverInfo.off("maxRequest"))}
		})
	}
	for req, name := range []string{"imaging_ccd", "tracking_ccd"} {
		info, _, e := c.ccdInfo(uint16(req))
		if e != nil {
			out[name] = map[string]any{"available": false, "error": e.Error()}
		} else {
			out[name] = info
		}
	}
	optional("camera_extended", 11, word(2), abiExtended.size, func(r []byte) any {
		cols := []uint16{}
		for i := 0; i < int(u16(r, abiExtended.off("badColumns"))) && i < 4; i++ {
			cols = append(cols, u16(r, abiExtended.off("columns")+abiWordSize*i))
		}
		return map[string]any{"serial_number": cstring(r[abiExtended.off("serialNumber"):abiExtended.size]), "imaging_abg": u16(r, abiExtended.off("imagingABG")), "bad_columns_count": u16(r, abiExtended.off("badColumns")), "bad_columns_first_four": cols}
	})
	optional("imaging_capabilities", 11, word(4), abiCapabilities.size, func(r []byte) any {
		b := u16(r, abiCapabilities.off("capabilitiesBits"))
		return map[string]any{"bits": fmt.Sprintf("0x%04x", b), "frame_transfer": b&1 != 0, "electronic_shutter_ms": b&2 != 0, "remote_guide_head": b&4 != 0, "internal_frame_buffer": b&32 != 0, "requires_start_exposure2": b&64 != 0, "dump_extra": u16(r, abiCapabilities.off("dumpExtra"))}
	})
	optional("camera_extended3", 11, word(6), abiExtended3.size, func(r []byte) any {
		b := u32(r, abiExtended3.off("cameraBits"))
		return map[string]any{"camera_bits": fmt.Sprintf("0x%08x", b), "ccd_bits": fmt.Sprintf("0x%08x", u32(r, abiExtended3.off("ccdBits"))), "extra_bits": fmt.Sprintf("0x%08x", u32(r, abiExtended3.off("extraBits"))), "stxl_flag": b&1 != 0, "electronic_shutter_only": b&2 != 0, "color_sensor": u32(r, abiExtended3.off("ccdBits"))&1 != 0}
	})
	optional("link", 32, nil, abiLink.size, func(r []byte) any {
		return map[string]any{"established": u16(r, abiLink.off("linkEstablished")) != 0, "camera_type": u16(r, abiLink.off("cameraType")), "communications_total": u32(r, abiLink.off("comTotal")), "communications_failed": u32(r, abiLink.off("comFailed"))}
	})
	optional("customer_options_read_only", 55, make([]byte, abiCustomer.size), abiCustomer.size, func(r []byte) any {
		return map[string]any{"overscan_regions": u16(r, abiCustomer.off("bOverscanRegions")) != 0, "window_heater": u16(r, abiCustomer.off("bWindowHeater")) != 0, "preflash_ccd": u16(r, abiCustomer.off("bPreflashCcd")) != 0, "vdd_normally_off": u16(r, abiCustomer.off("bVddNormallyOff")) != 0}
	})
	p := make([]byte, abiBit.size)
	put16(p, abiBit.off("bitOperation"), 1)
	optional("legacy_low_voltage_flag", 44, p, 2, func(r []byte) any {
		return map[string]any{"raw_bit": u16(r, 0), "applicability": "The SDK documents this bit for STL only. STXL validity and threshold are unverified; this is not a voltage measurement and cannot rule out a supply fault."}
	})
	out["temperature"] = c.temperature()
	controls := make([]map[string]any, 0, len(ControlNames))
	for id, name := range ControlNames {
		item := map[string]any{"id": id, "name": name}
		v, e := c.GetControl(uint16(id))
		if e != nil {
			item["available"] = false
			item["error"] = e.Error()
		} else {
			item["available"] = true
			item["value"] = v
		}
		controls = append(controls, item)
	}
	out["driver_controls"] = controls
	out["driver_controls_note"] = "Values are DLL runtime controls; a readable value does not establish that a particular camera implements its effect. No values were changed by Snapshot."
	return out
}

var temperatureFields = []string{"ccdSetpoint", "imagingCCDTemperature", "trackingCCDTemperature", "externalTrackingCCDTemperature", "ambientTemperature", "imagingCCDPower", "trackingCCDPower", "externalTrackingCCDPower", "heatsinkTemperature", "fanPower", "fanSpeed", "trackingCCDSetpoint"}

func (c *Camera) temperature() map[string]any {
	errs := []string{}
	for _, req := range []uint16{2, 1} {
		r := make([]byte, abiTemperature.size)
		if e := c.command(6, word(req), r); e != nil {
			errs = append(errs, e.Error())
			continue
		}
		m := map[string]any{"request": req, "cooling_enabled_raw": u16(r, abiTemperature.off("coolingEnabled")), "cooling_enabled": u16(r, abiTemperature.off("coolingEnabled"))&0x7fff != 0, "cooling_frozen": u16(r, abiTemperature.off("coolingEnabled"))&0x8000 != 0, "fan_state": u16(r, abiTemperature.off("fanEnabled"))}
		names := []string{"ccd_setpoint_c", "imaging_ccd_c", "tracking_ccd_c", "external_tracking_ccd_c", "ambient_c", "imaging_cooler_percent", "tracking_cooler_percent", "external_tracking_cooler_percent", "heatsink_c", "fan_percent", "fan_rpm", "tracking_setpoint_c"}
		if req == 1 {
			names = names[:10]
		}
		for i, name := range names {
			v := math.Float64frombits(le.Uint64(r[abiTemperature.off(temperatureFields[i]):]))
			if math.IsNaN(v) || math.IsInf(v, 0) {
				m[name] = nil
			} else {
				m[name] = v
			}
		}
		m["note"] = "Fields for absent sensors or fans may be zero or model-specific; zeros are not proof of a physical sensor reading."
		if len(errs) > 0 {
			m["fallback_errors"] = errs
		}
		return m
	}
	r := make([]byte, abiTemperatureLegacy.size)
	if e := c.command(6, nil, r); e != nil {
		errs = append(errs, e.Error())
		return map[string]any{"available": false, "errors": errs}
	}
	return map[string]any{"request": 0, "enabled_raw": u16(r, abiTemperatureLegacy.off("enabled")), "setpoint_ad": u16(r, abiTemperatureLegacy.off("ccdSetpoint")), "cooler_power_0_255": u16(r, abiTemperatureLegacy.off("power")), "ccd_thermistor_ad": u16(r, abiTemperatureLegacy.off("ccdThermistor")), "ambient_thermistor_ad": u16(r, abiTemperatureLegacy.off("ambientThermistor")), "fallback_errors": errs}
}

// Capture takes a shutter-closed full frame with StartExposure2 and its matching
// StartReadout geometry. A context bounds polling; a stuck native DLL call cannot
// be interrupted safely by Go, so hardware transport timeouts remain driver-owned.
func (c *Camera) Capture(ctx context.Context, mode Mode, seconds float64) (pixels []uint16, err error) {
	c.lastCapture = CaptureTelemetry{ExpectedRows: int(mode.Height), PhaseNote: "Phase observations are sampled about every 100 ms; short phases can be missed. Pre-exposure can include shutter movement or preflash, and does not prove either occurred."}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0.01 || seconds > 3600 {
		return nil, errors.New("exposure must be 0.01..3600 seconds")
	}
	if mode.Width == 0 || mode.Height == 0 || uint64(mode.Width)*uint64(mode.Height) > 100_000_000 {
		return nil, errors.New("invalid or excessive image dimensions")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// Only use modes and geometry actually reported by the connected camera.
	modes, e := c.Modes()
	if e != nil {
		return nil, e
	}
	valid := false
	for _, m := range modes {
		if m.ID == mode.ID && m.Width == mode.Width && m.Height == mode.Height {
			valid = true
			break
		}
	}
	if !valid {
		return nil, errors.New("requested readout mode or dimensions are not reported by this camera")
	}
	statusBefore := make([]byte, abiWordSize)
	if err = c.command(12, word(50), statusBefore); err != nil {
		return nil, err
	}
	// A completed status can remain latched after a previous readout. Only the
	// two in-progress states establish that another integration is still active.
	if state := u16(statusBefore, 0) & 3; state == 1 || state == 2 {
		return nil, fmt.Errorf("imaging CCD exposure is still in progress (status %d); refusing to start another exposure", state)
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(seconds*float64(time.Second))+180*time.Second)
		defer cancel()
	}
	ticks := uint32(math.Round(seconds * 100))
	p := make([]byte, abiExposure.size)
	put32(p, abiExposure.off("exposureTime"), ticks)
	put16(p, abiExposure.off("openShutter"), 2)
	put16(p, abiExposure.off("readoutMode"), mode.ID)
	put16(p, abiExposure.off("height"), mode.Height)
	put16(p, abiExposure.off("width"), mode.Width)
	if err = c.command(50, p, nil); err != nil {
		return nil, err
	}
	exposureStart := time.Now()
	exposureActive := true
	defer func() {
		var cleanup []error
		if exposureActive {
			cleanup = append(cleanup, c.command(2, word(0), nil))
		}
		// On STX/STXL, EndExposure alone can initiate frame-buffer readout;
		// EndReadout is necessary even when cancelling before StartReadout.
		cleanup = append(cleanup, c.command(25, word(0), nil))
		err = errors.Join(err, errors.Join(cleanup...))
		if err != nil {
			pixels = nil
		}
	}()
	waiting := true
	defer func() {
		if waiting {
			c.lastCapture.ExposureWaitSeconds = time.Since(exposureStart).Seconds()
		}
	}()
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		status, e := c.exposureStatus(exposureStart)
		if e != nil {
			return nil, e
		}
		c.log("exposure_status=0x%04x", status)
		if status&3 == 3 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	c.lastCapture.ExposureWaitSeconds = time.Since(exposureStart).Seconds()
	waiting = false
	if err = c.command(2, word(0), nil); err != nil {
		return nil, err
	}
	exposureActive = false
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	rp := make([]byte, abiReadout.size)
	put16(rp, abiReadout.off("readoutMode"), mode.ID)
	put16(rp, abiReadout.off("height"), mode.Height)
	put16(rp, abiReadout.off("width"), mode.Width)
	if err = c.command(35, rp, nil); err != nil {
		return nil, err
	}
	pixels = make([]uint16, int(mode.Width)*int(mode.Height))
	line := make([]byte, int(mode.Width)*2)
	lp := make([]byte, abiLine.size)
	put16(lp, abiLine.off("readoutMode"), mode.ID)
	put16(lp, abiLine.off("pixelLength"), mode.Width)
	c.lastCapture.LineMilliseconds = make([]float64, 0, int(mode.Height))
	readStart := time.Now()
	var slowest time.Duration
	rows := 0
	defer func() {
		c.lastCapture.ReadoutSeconds = time.Since(readStart).Seconds()
		c.log("readout_summary completed_rows=%d expected_rows=%d elapsed_ms=%.3f slowest_row_ms=%.3f", rows, mode.Height, float64(time.Since(readStart).Microseconds())/1000, float64(slowest.Microseconds())/1000)
	}()
	for y := 0; y < int(mode.Height); y++ {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if y%64 == 0 {
			c.log("readout_progress next_zero_based_row=%d", y)
		}
		if err = c.call(3, lp, line, y == 0 || y == int(mode.Height)-1); err != nil {
			return nil, fmt.Errorf("readout failed at zero-based row %d: %w", y, err)
		}
		d := c.lastNativeDuration
		c.lastCapture.LineMilliseconds = append(c.lastCapture.LineMilliseconds, float64(d)/float64(time.Millisecond))
		if d > slowest {
			slowest = d
		}
		rows++
		c.lastCapture.CompletedRows = rows
		for x := 0; x < int(mode.Width); x++ {
			pixels[y*int(mode.Width)+x] = u16(line, x*2)
		}
	}
	return pixels, nil
}

// Extended status is optional, but transport failures are never treated as an
// unsupported feature. A timed-out query aborts the acquisition and cleans up.
func (c *Camera) exposureStatus(start time.Time) (uint16, error) {
	if c.status2Unsupported == "" {
		r := make([]byte, abiStatus2.size)
		if err := c.command(61, word(50), r); err != nil {
			var de *DriverError
			if !errors.As(err, &de) || (de.Code != 4 && de.Code != 5) {
				return 0, err
			}
			c.status2Unsupported = err.Error()
		} else {
			status, info := u16(r, abiStatus2.off("status")), u16(r, abiStatus2.off("info"))
			c.lastCapture.Status2Available = true
			phases := c.lastCapture.Phases
			if len(phases) == 0 || phases[len(phases)-1].Info != info || phases[len(phases)-1].Status != status {
				// Bound storage even if a malfunctioning driver toggles status each poll.
				if len(phases) < 4096 {
					phase := []string{"idle", "pre-exposure", "integrating", "post-exposure"}[info&3]
					c.lastCapture.Phases = append(phases, PhaseObservation{time.Since(start).Seconds(), status, info, phase})
				}
			}
			return status, nil
		}
	}
	c.lastCapture.Status2Error = c.status2Unsupported
	r := make([]byte, abiWordSize)
	err := c.command(12, word(50), r)
	return u16(r, 0), err
}
