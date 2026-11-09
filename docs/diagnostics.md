# Interpreting SBIG STXL diagnostics

This utility is designed to collect evidence about successive frames becoming
corrupted or saturated. A returned image and a successful driver command do not
prove that its pixels are correct. Conversely, saturation and banding do not by
themselves distinguish electronics, firmware, driver state, or transfer errors.
The original reported failure occurred with both NINA and CCDOps and persisted
after a physical power cycle. Those applications share the SBIG Universal Driver;
changing application alone therefore does not isolate that driver.

## A useful remote comparison

Close NINA, CCDOps, and other camera clients before connecting. First record the
unmodified settings and repeated dark exposures through the same utility. Preserve
the cooler setpoint and regulation state; a temperature change would confound the
comparison. The utility's connection is a software initialization, not a power
cycle, and its initial driver settings are its own process state, not a snapshot
of NINA's previous driver process.

Use full-frame 1x1 data first: a small subframe could hide the horizontal boundary.
Repeated frames at 0.01 s and 5 s reproduce the original short and longer exposure
paths. These are requested exposure times; compare the camera's advertised minimum
and any driver rejection. A short dark exposure is not proof of a true zero-time
bias. If the camera supports 2x2 and 3x3, those modes provide a separate readout
comparison. Read out without dark subtraction, warm-pixel removal, or further
image processing. Check the reported repair settings before calling any data raw.

Compare one driver setting at a time, with a baseline, changed-setting sequence,
and restored-baseline sequence. Read back each successful change and record the
actual value. A successful API readback means the driver accepted the setting;
it does not prove the camera hardware implements it or that it changed an analog
voltage. If a setting cannot be read, do not guess its initial value for restoring
it. Capture in the same process that sets the parameter. Unsupported diagnostic
queries are useful report entries and are not proof of camera damage.

Do not infer recovery from a single improved frame. The reported progression from
the first frame into persistent saturation makes repeatability and sequence order
especially relevant. Reconnection or a setting transition can itself affect state.

## Documented driver parameters

The manufacturer's [SBIG Universal Driver application note, version 4.75 Build 9](https://supernovae.in2p3.fr/~llg/DICE/StarDICE/sbig/LinuxDevKit/doc/SBIGUDrv.pdf)
is an older primary document preserved by a research institution. Sections 3.5.13
and 3.5.14 describe the controls. The running DLL remains authoritative about
which queries it accepts; camera-specific effects may differ.

- `DCP_VDD_OPTIMIZED`: the documented default is true. The optimization skips
  the Vdd lowering/raising cycle for exposures shorter than 3 seconds. False
  disables that short-exposure optimization; it does **not** mean Vdd is never
  lowered. The manual explicitly describes different short/long bias structures.
  It offers a useful comparison, not a claimed repair for a particular camera.
- `DCP_HIGH_THROUGHPUT`: the documented default is false. True trades image
  quality for speed and can leave the shutter motor energized. Report it and,
  when testing, compare against false; enabling it is not a corrective default.
- `DCP_AUTO_AD_GAIN`: report its state. Disabling it permits manual A/D settings;
  it is not a generic gain repair. `USB AD Control` is documented for OEM use.
- `DCP_NO_HCLKS_FOR_INTEGRATION` and `DCP_VERT_FLUSH_CONTROL_ENABLE` affect
  clocking and are documented for SBIG testing. Read their values without
  experimenting with them as part of the ordinary suite.
- `DCP_CALL_JOURNAL_ENABLE`: the driver journal can introduce readout artifacts.
  Application-side timing/error logs avoid enabling that diagnostic mode.
- Repair controls can alter pixels. Their Windows settings may persist in the
  Registry, so changing them is not equivalent to a process-only experiment.

The SDK also defines a read-only form of `Customer Options` for STX/STXL/STT
families, including overscan, window heater, preflash, and Vdd-normally-off flags.
Reporting these is useful; do not conflate the camera's Vdd option with the DLL's
short-exposure optimization. `Read Offset 2`, when supported, provides offset and
readout-register RMS noise rather than a calibrated power-supply measurement.

## What the output can show

The FITS image preserves the delivered unsigned pixels using `BITPIX=16`,
`BSCALE=1`, and `BZERO=32768`. Data are big endian and header/data blocks are
padded to 2880 bytes. No optical orientation is assumed: row zero is the first
row delivered by the driver. FITS viewers may display the vertical axis differently
from the PNG preview.

The fixed PNG maps the full 0–65535 digital range. The separately labelled
stretched PNG maps the 1st to 99.5th percentiles into 8-bit display values and
clips outside that interval. If that interval is empty, it uses the minimum and
maximum, falling back to the full range for a constant image. Use the FITS and
numeric statistics to decide whether data are saturated, not the stretched PNG.

Statistics use every pixel. Standard deviations are population standard deviations;
quantiles interpolate between adjacent ranks. Exact saturation is 65535 ADU;
the near-saturation fraction uses a stated threshold of 65000 ADU. Zero pixels,
histogram populations, unique values, strongest adjacent row/column mean steps,
and exactly repeated adjacent rows help characterize structured corruption.
Neither repeated rows nor a discontinuity proves a transfer failure. All-saturated
rows are necessarily identical. The strongest boundary is a descriptive maximum,
not a statistical hardware-failure detector. CSV indices are zero based; a boundary
row denotes the row immediately after the largest jump.

The pixel SHA-256 is computed from consecutive unsigned 16-bit values in big-endian
order, independent of FITS headers. Identical hashes across frames flag identical
delivered arrays; investigate all-zero and all-saturated cases before assuming a
stale buffer. Output files are created exclusively to avoid overwriting earlier
evidence. A failed save can leave partial files, which must not be interpreted as
a successful complete frame.

## Limits of power and Windows diagnostics

The [STXL operating manual](https://diffractionlimited.com/wp-content/uploads/2016/12/stxl_manual.pdf)
describes input-voltage indicator LEDs and a 10.5–14 V input range. It does not
establish a software-readable analog rail-voltage meter. The older Universal
Driver's Bit I/O low-voltage warning is specifically documented for ST-L cameras;
a raw bit result on another model must not be labelled a measured STXL voltage
or used to certify a good supply. No ordinary driver query establishes actual
CCD substrate voltage or clock waveforms. Those physical quantities remain
unmeasured by this tool.

Diffraction Limited's [July 9, 2026 Windows 11 notice](https://forum.diffractionlimited.com/threads/sbig-universal-driver-disabled-under-windows-11-by-microsoft.11152/)
identifies USBIO driver blocking affecting STXL-11002 and other legacy cameras.
This is relevant to DLL/device/link failures and Device Manager Code 52. It is
not evidence that Windows signing policy caused a fully downloaded saturated
frame. Record the operating-system build, DLL path and version, API version, and
actual return codes before drawing that conclusion. This utility does not change
Windows security policy, firmware, drivers, or camera nonvolatile settings.

The manufacturer's [MaxIm DL driver guide](https://cdn.diffractionlimited.com/help/maximdl/CCD_Cameras_and_Autoguiders.htm)
confirms STXL uses the second-generation SBIG Universal Driver. DL Imaging is a
different camera family interface and is not a replacement STXL acquisition path.
An explicit alternate DLL path tests another installed compatible DLL without
replacing the system DLL. It must match the executable's architecture and still
depends on the installed lower-level camera driver.
