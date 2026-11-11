package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"github.com/kirxkirx/sbig-debug/internal/diagnostic"
)

func saveReport(dir string, r report) error {
	data, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	if e = atomicWrite(filepath.Join(dir, "report.json"), data); e != nil {
		return e
	}
	var html bytes.Buffer
	if e = reportTemplate.Execute(&html, r); e != nil {
		return e
	}
	return atomicWrite(filepath.Join(dir, "report.html"), html.Bytes())
}

func atomicWrite(path string, data []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".report-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	_, e = f.Write(data)
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(name, path)
}

// This SVG contains only numeric values and constant text, never raw metadata.
// Axes have no background grids. Full-scale ADU makes frames directly comparable.
func profileSVG(points []diagnostic.Profile) template.HTML {
	if len(points) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<svg viewBox="0 0 640 210" role="img" aria-label="Mean (blue) and median (orange) ADU profile, full 0 to 65535 range"><path d="M52 12V178H625" fill="none" stroke="#667080"/><text x="2" y="17">65535</text><text x="37" y="181">0</text><text x="50" y="202">0</text>`)
	fmt.Fprintf(&b, `<text x="590" y="202">%d</text><polyline fill="none" stroke="#1769aa" stroke-width="1.5" points="`, len(points)-1)
	denom := len(points) - 1
	if denom < 1 {
		denom = 1
	}
	for i, p := range points {
		fmt.Fprintf(&b, "%.2f,%.2f ", 52+float64(i)*573/float64(denom), 178-p.Mean*166/65535)
	}
	b.WriteString(`"/><polyline fill="none" stroke="#bc5300" stroke-width="1.5" stroke-dasharray="4 3" points="`)
	for i, p := range points {
		fmt.Fprintf(&b, "%.2f,%.2f ", 52+float64(i)*573/float64(denom), 178-p.Median*166/65535)
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String())
}

var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"pct": func(v float64) string { return fmt.Sprintf("%.3f%%", v*100) },
	"num": func(v float64) string { return fmt.Sprintf("%.2f", v) },
	"json": func(v any) string {
		b, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			return e.Error()
		}
		return string(b)
	},
	"profile": profileSVG,
}).Parse(`<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>SBIG diagnostic report</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;color:#182334;background:#f3f5f8;max-width:1400px;margin:24px auto;padding:0 22px}h1{margin-bottom:4px}h2{margin-top:28px}h3{overflow-wrap:anywhere}a{color:#075da5}section,details,.card{background:white;border:1px solid #d3dbe5;border-radius:8px;padding:18px;margin:16px 0}summary{cursor:pointer;font-weight:600}pre{white-space:pre-wrap;overflow-wrap:anywhere;font-size:13px}table{border-collapse:collapse;width:100%;font-size:14px}td,th{text-align:right;padding:8px;border-bottom:1px solid #dde3eb}td:first-child,th:first-child{text-align:left;overflow-wrap:anywhere}.scroll{overflow-x:auto}.images,.plots{display:flex;gap:20px;flex-wrap:wrap}.images figure,.plots figure{margin:0;flex:1;min-width:260px}.images img{display:block;width:100%;max-height:480px;object-fit:contain;background:#111}.plots svg{width:100%;font:12px system-ui,sans-serif}figcaption{color:#46556b;font-size:14px} .notice{background:#fff2cd;border:1px solid #e5c76a;padding:14px;border-radius:6px}.error{background:#ffe4e4;color:#8a1515}.small{font-size:14px;color:#46556b}code{overflow-wrap:anywhere}
</style>
<h1>SBIG camera diagnostics</h1><div class="small">Version {{.Version}} · {{.OS}}/{{.Arch}} · Started {{.Started}} · {{if .Finished}}Finished {{.Finished}}{{else}}Run in progress / partial report{{end}}</div>
{{if .Demo}}<p class="notice"><strong>SIMULATED DATA ONLY.</strong> This report demonstrates normal, striped, and saturated arrays. No driver or camera was used.</p>{{end}}
{{range .Errors}}<p class="notice error">{{.}}</p>{{end}}
<p>This report describes delivered pixel values and driver responses. It does not identify a failed component or measure the camera's analog supply voltages. The initial state belongs to this utility's driver process.</p>
<p><a href="report.json">Complete JSON report</a> · <a href="console.log">Console log</a> · <a href="driver-calls.jsonl">Driver call trace</a> · <a href="crash.log">Crash log (normally empty)</a></p>
{{if .Notes}}<section><h2>Run notes</h2><ul>{{range .Notes}}<li>{{.}}</li>{{end}}</ul></section>{{end}}
<details><summary>Run settings</summary><pre>{{json .Settings}}</pre></details>
{{if .Frames}}
<section><h2>Frames in {{if eq .Action "analyze"}}input{{else}}acquisition{{end}} order</h2><p class="small">All values are ADU. Saturated means exactly 65535; near saturation means at least 65000. Boundary row is zero based, immediately after the strongest adjacent row-mean jump; −1 means no jump. This maximum alone is not a fault detector.</p>
<div class="scroll"><table><thead><tr><th>Frame</th><th>Median</th><th>Mean</th><th>Std.dev.</th><th>Min / Max</th><th>Saturated</th><th>≥65000</th><th>Zero</th><th>Boundary row</th><th>Jump ADU</th></tr></thead><tbody>
{{range .Frames}}<tr><td><a href="#{{.Stem}}">{{.Stem}}</a></td><td>{{num .Stats.Median}}</td><td>{{num .Stats.Mean}}</td><td>{{num .Stats.StdDev}}</td><td>{{.Stats.Min}} / {{.Stats.Max}}</td><td>{{pct .Stats.SaturatedFraction}}</td><td>{{pct .Stats.NearSaturatedFraction}}</td><td>{{pct .Stats.ZeroFraction}}</td><td>{{.Stats.BoundaryRow}}</td><td>{{num .Stats.StrongestRowJump}}</td></tr>{{end}}
</tbody></table></div></section>
{{range .Frames}}<section id="{{.Stem}}"><h2>{{.Stem}}</h2><p>{{.Width}} × {{.Height}} pixels · <a href="{{.FITS}}">Pixel FITS</a> · <a href="{{.RowsCSV}}">Row CSV</a> · <a href="{{.ColumnsCSV}}">Column CSV</a> · <a href="{{.HistogramCSV}}">Full histogram CSV</a>{{with index .Metadata "source_file"}} · <a href="{{.}}">Original input file (unchanged)</a>{{end}}{{with index .Metadata "line_timing_csv"}} · <a href="{{.}}">Readout timing CSV</a>{{end}}</p>
{{with index .Metadata "source_path"}}<p>Input: <code>{{.}}</code></p>{{end}}
{{if .Stats.Warnings}}<ul>{{range .Stats.Warnings}}<li>{{.}}</li>{{end}}</ul>{{end}}
<div class="images"><figure><a href="{{.PNG}}"><img src="{{.PNG}}" loading="lazy" alt="Fixed-scale image"></a><figcaption>Fixed full scale: 0–65535 ADU. PNG preserves 16-bit pixel values.</figcaption></figure><figure><a href="{{.StretchedPNG}}"><img src="{{.StretchedPNG}}" loading="lazy" alt="Contrast-stretched preview"></a><figcaption>Display stretch: {{num .StretchLow}}–{{num .StretchHigh}} ADU into 8 bits. Outside values clipped; use FITS for measurements.</figcaption></figure></div>
<div class="plots"><figure>{{profile .Stats.Rows}}<figcaption>Mean (blue) and median (orange) ADU versus row, in stored pixel order.</figcaption></figure><figure>{{profile .Stats.Columns}}<figcaption>Mean (blue) and median (orange) ADU versus column.</figcaption></figure></div>
{{if .Stats.RowMedianBoundaries}}<details><summary>Largest adjacent row-median changes</summary><p>Descriptive changes, ranked by magnitude; these are not calibrated fault thresholds.</p><table><tr><th>Row after change</th><th>Previous median</th><th>Current median</th><th>Signed change ADU</th></tr>{{range .Stats.RowMedianBoundaries}}<tr><td>{{.Row}}</td><td>{{num .Before}}</td><td>{{num .After}}</td><td>{{num .Delta}}</td></tr>{{end}}</table></details>{{end}}
<details><summary>Bit occupancy and region statistics</summary><p>Fractions count pixels with each bit set, from bit 0 (least significant) to bit 15. They describe this image's values; unusual fractions alone do not establish an ADC fault. Even/odd columns use zero-based stored indices.</p><table><tr><th>Bit</th><th>Set fraction</th></tr>{{range $bit, $fraction := .Stats.BitSetFraction}}<tr><td>{{$bit}}</td><td>{{pct $fraction}}</td></tr>{{end}}</table><pre>Left half: {{json .Stats.LeftHalf}}
Right half: {{json .Stats.RightHalf}}
Even columns: {{json .Stats.EvenColumns}}
Odd columns: {{json .Stats.OddColumns}}</pre></details>
<details><summary>Pixel statistics and hash</summary><pre>{{json .Stats}}</pre></details>
<details><summary>Exposure metadata and driver snapshots before / after</summary><pre>{{json .Metadata}}</pre></details></section>{{end}}{{end}}
<h2>Camera and driver snapshots</h2><p>Unsupported queries are recorded as errors, not zero measurements. A recognized control does not prove the camera implements it. Temperature fields may be unavailable or model dependent.</p>
{{range .Snapshots}}<details><summary>{{.Label}} · {{.Time}}</summary><pre>{{json .Values}}</pre></details>{{end}}
<p class="small">No frames are uploaded automatically. Keep this whole folder when sharing diagnostics. Firmware, driver installation, cooler setpoint, and nonvolatile camera options are not changed by the utility.</p></html>`))
