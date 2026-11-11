# SBIG Debug

A standalone Windows diagnostic utility for SBIG Universal Driver cameras, aimed
at an STXL-11002 that returns successive corrupted or saturated frames. It records
the installed driver and camera state, captures repeated dark frames, and compares
two documented driver settings **inside the same acquisition process**.

[Download Windows executables](https://github.com/kirxkirx/sbig-debug/releases/latest)
· [Builds](https://github.com/kirxkirx/sbig-debug/actions)
· [Diagnostic background and sources](docs/diagnostics.md)

No Python, Go, ASCOM, or .NET installation is needed to run the executable. The
manufacturer's **SBIG Universal Driver** and camera device driver must already be
installed. Proprietary drivers are not bundled. DL Imaging does not support STXL.
Automated tests simulate camera responses; this initial version has not been
validated on physical SBIG hardware.

## First run on Windows

1. Download and extract `sbig-debug-windows.zip` from Releases.
2. Close NINA, CCDOps, MaxIm DL, and any other program using the camera.
3. Double-click `sbig-debug-win32.exe` if using the usual 32-bit legacy SBIG DLL,
   or `sbig-debug-win64.exe` if the 64-bit SBIG DLL is installed. Both run on
   64-bit Windows; the **DLL architecture**, not just Windows, determines which
   one works. A wrong-architecture error explains how to switch.
4. Choose **1** to collect camera and driver information. The console stays open
   until you press Enter. The output directory is printed at the top and bottom.
5. Open `report.html` in that directory with a browser. No web server or network
   connection is needed. The default directory is
   `%USERPROFILE%\SBIG-Diagnostics\run-<UTC timestamp>`.

For an acquisition run, the mechanical shutter is requested **closed** for every
frame. Keep external illumination stable; an API shutter command cannot verify
the physical shutter position. Cooler regulation and setpoint remain as found.
There is no requested power cycle, firmware update, driver installation, or change
to Windows security settings.

If you do not have a camera connected, select **7** for a clearly labelled demo.
The demo makes three small synthetic images: normal, a bottom stripe, and fully
saturated. It never loads the driver DLL.

## Which experiment to run

The double-click menu offers:

| Choice | Action |
| --- | --- |
| 1 | Camera/driver information only; no image capture or parameter changes |
| 2 | Three consecutive 0.01 s darks, then three consecutive 5 s darks, at current driver settings |
| 3 | Baseline, `DCP_VDD_OPTIMIZED=0`, restored baseline |
| 4 | Baseline, `DCP_HIGH_THROUGHPUT=0`, restored baseline |
| 5 | Baseline, both controls set to zero, restored baseline |
| 6 | The repeated-dark sequence at advertised 1×1, 2×2, and 3×3 modes |
| 7 | Synthetic demo without a driver |
| 8 | Inventory installed DLLs without opening a camera |
| 9 | Analyze an existing FITS image without opening a camera |

Start with **2**, then **3**. A comparison runs six frames per block by default,
18 frames total. At 4008×2672, FITS alone needs about **367 MiB** for those 18
frames; PNGs and diagnostic tables require additional space. Expected frame count
and raw data volume are displayed before acquisition. A new report folder is used
for each run; existing nonempty directories are refused.

Setting Vdd optimization to zero disables the short-exposure optimization; it does
**not** disable Vdd lowering. It is a controlled experiment, not an established
repair. If high throughput is already zero, the program records that the test
made no change. A setting readback proves DLL acceptance, not an analog change in
the camera. Details and manufacturer sources are in
[diagnostics.md](docs/diagnostics.md).

Original control values are read before changing them, verified after writing,
and restored after each experimental block, including ordinary failures and
Ctrl+C. Restoration failures are reported and halt the sequence. Forced process
termination, power loss, or a hung driver can prevent cleanup. The utility never
enables high throughput unless restoring its original value. It does not change
gain, clocking, EEPROM, defect-repair settings, filter position, relays, or cooler
state. A reconnect initializes the driver; this does not reproduce NINA's previous
process state, and changes made here are not promised to carry over to NINA.

## Command-line examples

Open PowerShell in the extracted folder. Replace `win64` with `win32` as needed.

```powershell
# Read-only diagnostic queries
.\sbig-debug-win64.exe --action info

# Inspect driver copies without loading the camera DLL or connecting
.\sbig-debug-win64.exe --action drivers

# Analyze existing images; put flags before the file paths
.\sbig-debug-win64.exe --action analyze --no-pause 'C:\Images\bad1.fits' 'C:\Images\bad2.fits'

# Baseline: repeated full-frame darks
.\sbig-debug-win64.exe --action baseline --count 5 --exposures 0.01,5

# Vdd comparison in the same process, including restored baseline
.\sbig-debug-win64.exe --action compare --test vdd --count 3

# Explore the documented short/long transition
.\sbig-debug-win64.exe --action compare --test vdd --exposures 0.01,1,2.99,3.01,5 --count 3

# Separate high-throughput comparison
.\sbig-debug-win64.exe --action compare --test throughput

# Individual and combined changes, with original settings between experiments
.\sbig-debug-win64.exe --action compare --test all --count 3

# Full-frame advertised readout modes: 0=1x1, 1=2x2, 2=3x3
.\sbig-debug-win64.exe --action baseline --modes 0,1,2 --exposures 0.01,5

# Select a camera or Ethernet connection
.\sbig-debug-win64.exe --device usb1 --action info
.\sbig-debug-win64.exe --device ethernet --ip 192.168.1.100 --action info

# Test a specific compatible DLL without replacing the installed system DLL
.\sbig-debug-win64.exe --dll 'C:\SBIG-test\SBIGUDrv.dll' --action baseline

# Test reporting without hardware
.\sbig-debug-win64.exe --demo --out '.\demo-report' --no-pause
```

Exposure times must be 0.01–3600 seconds in 0.01-second increments. These are
requested dark exposures, not a guarantee of a zero-time bias or of the camera's
minimum timing. Mode dimensions come from the camera; unsupported modes are
explicitly skipped. Exposure and readout errors stop acquisition, preserve
earlier frames, and produce a partial/error report. The exit code is 0 on success,
1 for operation failure, and 2 for invalid arguments. Unusual pixel values are
observations, not an acquisition error.

`--timeout 120s` adds a deadline beyond the exposure duration. Cancellation and
deadlines are checked between driver calls; a blocking native DLL call cannot be
forcibly interrupted safely. The trace identifies the last call entered. Do not
run multiple copies against one camera. `--help` shows all options.

A watchdog prints the active native call after 60 seconds without a return. It
only observes call state; it does not issue driver commands. On Windows, acquisition
temporarily inhibits automatic idle sleep on the camera's locked OS thread and
restores the previous requirement on exit. Deliberate sleep, closing the laptop
lid, power loss, and forcibly terminating the program can still interrupt a run.

## Analyze existing images

Choose **9**, drop FITS files onto the executable, or pass them on the command line.
Positional file paths select offline analysis when `--action` is omitted. Put all
options before the paths. No camera connection or installed driver is needed.

Each original file is copied unchanged into the new output folder, with its file
SHA-256 and original path recorded. Statistics, pixel hashes, previews, and CSVs
use the same analysis as new captures. The separately generated pixel FITS keeps
the selected plane's physical values and selected metadata; the original input
copy retains the complete source file. A bad file is reported while later inputs
are still processed, and any failed input makes the exit status nonzero.

Supported inputs are uncompressed monochrome two-dimensional FITS images with
`BITPIX` 8, 16, or 32, `BSCALE=1`, integral `BZERO`, and physical integer values
within 0–65535. The usual unsigned `BITPIX=16, BZERO=32768` convention is supported.
Floating-point, scaled, undefined, color/cube, and out-of-range data are rejected
instead of being clipped or normalized. The reader can skip non-image HDUs to
find an image extension. Limits are 32 million pixels, 65535 pixels per axis,
512 MiB per input file, and 100 input files. XISF is not currently supported;
export an uncompressed integer FITS image preserving the original pixel values.

## Information collected

- Loaded DLL path, file version, SHA-256, process architecture, and Windows version.
- Inventory of default, legacy Windows System, and common application DLL copies,
  including PE architecture and whether it matches the process, even when connection fails.
- Driver/API versions, firmware, camera type/serial, reported CCD capabilities,
  readout modes, dimensions, pixel size and gain.
- Temperature/cooling/fan telemetry supported by the camera, and link counters.
- Read-only customer options and driver-control values, including Vdd, throughput,
  FIFO/pipeline, gain, vertical flush, and defect-repair controls.
- Driver call return codes and timing; exposure/readout cleanup status.
- Per-line native readout timing and sampled extended exposure phases when supported.
  Phase observations are approximate and do not establish whether preflash occurred.
- Per-frame snapshots, exact pixel hash, min/max/mean/median/standard deviation,
  zero and saturation fractions, and row/column discontinuity measurements.
- Row/column medians, the five largest row-median changes, bit occupancy, and
  left/right-half and even/odd-column summaries. These describe pixels, not a
  diagnosed failed component.

Unsupported queries appear as unavailable with the actual driver error; they are
not interpreted as zeros or camera failure. Some telemetry has model-dependent
meaning. In particular there is **no calibrated supply/substrate-voltage reading**
here. A legacy low-voltage bit is documented for STL, so its raw result must not
be used to certify an STXL supply. Readable driver controls need not affect every
camera model. Driver-side defect repair may already be active: the utility records
that state and preserves the pixels delivered by the driver without further repair.

## Output files

| File | Contents |
| --- | --- |
| `report.html` | Local report, comparison table, previews, row/column plots without grids, telemetry |
| `report.json` | Structured record including before/after snapshots for every completed frame |
| `console.log` | Human-readable run log |
| `driver-calls.jsonl` | Driver command trace |
| `crash.log` | Additional Go fatal-error output; normally empty |
| `*.fits` | Delivered unsigned 16-bit pixels, standard signed FITS storage with BZERO=32768 |
| `*_source.fits` | Offline input copied unchanged, including original headers/extensions |
| `*_fixed.png` | Full 0–65535 mapping, original 16-bit grayscale values |
| `*_stretch.png` | Labelled display-only contrast stretch |
| `*_rows.csv`, `*_columns.csv` | Statistics for every row/column, indexed from zero |
| `*_histogram.csv` | Counts for all 65536 possible ADU values |
| `*_line_timing.csv` | Native readout-call milliseconds for each completed row, excluding trace writes |

The program writes a report after every successfully saved frame and again at the
end. Row zero is the first row delivered by the driver; no optical orientation is
assumed. A FITS viewer can display the image vertically reversed relative to PNG.
The strongest boundary describes the largest adjacent mean jump; it does not by
itself identify defective electronics. Saturated rows can also be identical.

Keep or ZIP the entire output folder to share it. Nothing is uploaded automatically.
Paths, camera identifiers, and network settings are included in the reports, so
review the folder before posting it publicly.

## Driver loading and troubleshooting

The program loads an explicitly selected absolute DLL path, or checks the
executable's directory, the appropriate Windows system directory, and the legacy
`Windows\System` directory. It inventories common NINA, SBIG, Diffraction Limited,
and ASCOM installation folders, but application DLLs require explicit `--dll`
selection. Inventory paths are those visible to the process; a 32-bit process
may see redirected Windows system directories. It does not
search an arbitrary current directory or PATH. Select a trusted vendor DLL and
match the executable's architecture. A compatible user-mode DLL still needs the
installed low-level device driver. The vendor's
[legacy support page](https://diffractionlimited.com/legacy-product-support/)
provides Driver Checker and separate 64-bit DLL downloads.

If opening the driver/device fails, keep `report.html`, `console.log`, and the
driver trace. Check the reported DLL path and architecture, whether another camera
client is open, and the camera's entry in Device Manager. The documented Windows
11 USBIO blocking issue affects connection and driver loading; it does not establish
the cause of a completed saturated exposure. This utility does not bypass it.

## Building and testing

Building the executable uses Go 1.23 or newer with no third-party Go modules and no C compiler.
Windows calls use explicitly sized byte buffers verified against the manufacturer's
Windows SDK layouts (Windows `long` stays 32-bit on x64). The additional
[ABI verification script](tools/abicheck/README.md) uses Clang and a checksum-pinned
vendor SDK to check the actual packing/decoding layouts for both Windows targets;
GitHub Actions runs it before release. Linux supports offline FITS analysis, the
demo, and tests; hardware access is Windows-only.

```sh
go test ./...
go vet ./...
go run . --demo --out /tmp/sbig-example --no-pause
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o sbig-debug-win64.exe .
GOOS=windows GOARCH=386 CGO_ENABLED=0 go build -o sbig-debug-win32.exe .
```

GitHub Actions tests on Linux and Windows, builds x64 and x86 executables, and
executes each Windows binary in demo mode. Tests cover API layouts, exposure and
readout cleanup, control restoration, numeric diagnostics, FITS encoding, and PNG
pixel preservation. These tests cannot establish hardware correctness without a
camera. Every branch build provides executable artifacts; pushing a `v*` tag
publishes tested executables, a ZIP with documentation, and SHA-256 checksums.
The EXEs are not Authenticode-signed. No proprietary SDK files are redistributed.

MIT license for this project's code. Manufacturer documents and drivers retain
their own licenses.
