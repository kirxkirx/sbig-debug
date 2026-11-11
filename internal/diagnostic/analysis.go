// Package diagnostic preserves raw unsigned camera pixels and describes their
// distribution without assuming a cause for image corruption.
package diagnostic

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"math/bits"
	"sort"
)

const NearSaturationADU = 65000

// Profile describes one complete row or column. Indices are zero based in driver
// readout order, with no optical orientation implied.
type Profile struct {
	Index                                                  int
	Min, Max                                               uint16
	Mean, Median, StdDev                                   float64
	ZeroFraction, SaturatedFraction, NearSaturatedFraction float64
}

type Stats struct {
	Valid                   bool    `json:"valid"`
	Error                   string  `json:"error,omitempty"`
	Count                   int     `json:"count"`
	Min                     uint16  `json:"min"`
	Max                     uint16  `json:"max"`
	Mean                    float64 `json:"mean"`
	Median                  float64 `json:"median"`
	StdDev                  float64 `json:"stddev"`
	P01                     float64 `json:"p01"`
	P99                     float64 `json:"p99"`
	P995                    float64 `json:"p995"`
	ZeroFraction            float64 `json:"zero_fraction"`
	SaturatedFraction       float64 `json:"saturated_fraction"`
	NearSaturatedFraction   float64 `json:"near_saturated_fraction"`
	NearSaturationThreshold int     `json:"near_saturation_threshold_adu"`
	UniqueValues            int     `json:"unique_values"`
	StrongestRowJump        float64 `json:"strongest_row_mean_jump_adu"`
	// BoundaryRow is the second row in the adjacent pair: boundary is before it.
	BoundaryRow           int           `json:"boundary_row_zero_based"`
	StrongestColumnJump   float64       `json:"strongest_column_mean_jump_adu"`
	BoundaryColumn        int           `json:"boundary_column_zero_based"`
	IdenticalAdjacentRows int           `json:"identical_adjacent_rows"`
	BitSetFraction        [16]float64   `json:"bit_set_fraction_lsb_first"`
	LeftHalf              RegionStats   `json:"left_half"`
	RightHalf             RegionStats   `json:"right_half"`
	EvenColumns           RegionStats   `json:"even_zero_based_columns"`
	OddColumns            RegionStats   `json:"odd_zero_based_columns"`
	RowMedianBoundaries   []Boundary    `json:"strongest_row_median_boundaries"`
	SHA256                string        `json:"unsigned_big_endian_pixel_sha256"`
	Warnings              []string      `json:"observations,omitempty"`
	Rows                  []Profile     `json:"-"`
	Columns               []Profile     `json:"-"`
	Histogram             [65536]uint64 `json:"-"`
}

// RegionStats is descriptive. Count=0 marks an absent region (for example the
// left half of a one-column image); its zero summary values are not measurements.
type RegionStats struct {
	Count  int     `json:"count"`
	Mean   float64 `json:"mean_adu"`
	Median float64 `json:"median_adu"`
}

// Boundary is the adjacent row pair with its second row's zero-based index.
// The five largest nonzero changes are reported without a fault threshold.
type Boundary struct {
	Row    int     `json:"row_zero_based"`
	Before float64 `json:"median_before_adu"`
	After  float64 `json:"median_after_adu"`
	Delta  float64 `json:"signed_change_adu"`
}

type accumulator struct {
	n, zeros, saturated, near int
	min, max                  uint16
	mean, m2                  float64
}

func (a *accumulator) add(v uint16) {
	if a.n == 0 || v < a.min {
		a.min = v
	}
	if a.n == 0 || v > a.max {
		a.max = v
	}
	a.n++
	delta := float64(v) - a.mean
	a.mean += delta / float64(a.n)
	a.m2 += delta * (float64(v) - a.mean)
	if v == 0 {
		a.zeros++
	}
	if v == 65535 {
		a.saturated++
	}
	if v >= NearSaturationADU {
		a.near++
	}
}

func (a accumulator) profile(i int) Profile {
	n := float64(a.n)
	return Profile{Index: i, Min: a.min, Max: a.max, Mean: a.mean,
		StdDev: math.Sqrt(math.Max(0, a.m2/n)), ZeroFraction: float64(a.zeros) / n,
		SaturatedFraction: float64(a.saturated) / n, NearSaturatedFraction: float64(a.near) / n}
}

func validDimensions(pixels []uint16, width, height int) bool {
	return width > 0 && height > 0 && width <= int(^uint(0)>>1)/height && len(pixels) == width*height
}

// Analyze computes population standard deviations and linearly interpolated
// histogram quantiles. Every input pixel contributes; there is no sample or clip.
// Invalid dimensions return Valid=false, not a partially analyzed image.
func Analyze(pixels []uint16, width, height int) Stats {
	s := Stats{BoundaryRow: -1, BoundaryColumn: -1, NearSaturationThreshold: NearSaturationADU}
	if !validDimensions(pixels, width, height) {
		s.Error = "pixel count does not match positive image dimensions"
		return s
	}
	s.Valid, s.Count = true, len(pixels)
	s.Rows = make([]Profile, height)
	s.Columns = make([]Profile, width)
	columns := make([]accumulator, width)
	var regions [4]accumulator
	regionHist := new([4][65536]uint64)
	var bitCounts [16]uint64
	lineHistogram := new(medianHistogram)
	var total accumulator
	hash := sha256.New()
	buf := make([]byte, width*2)
	for y := 0; y < height; y++ {
		var row accumulator
		identical := y > 0
		for x := 0; x < width; x++ {
			v := pixels[y*width+x]
			lineHistogram.add(v)
			half := 0
			if x >= width/2 {
				half = 1
			}
			regions[half].add(v)
			regionHist[half][v]++
			parity := 2 + x%2
			regions[parity].add(v)
			regionHist[parity][v]++
			for remaining := v; remaining != 0; remaining &= remaining - 1 {
				bitCounts[bits.TrailingZeros16(remaining)]++
			}
			row.add(v)
			columns[x].add(v)
			total.add(v)
			s.Histogram[int(v)]++
			buf[2*x], buf[2*x+1] = byte(v>>8), byte(v)
			if y > 0 && v != pixels[(y-1)*width+x] {
				identical = false
			}
		}
		_, _ = hash.Write(buf)
		if identical {
			s.IdenticalAdjacentRows++
		}
		s.Rows[y] = row.profile(y)
		s.Rows[y].Median = lineHistogram.median(width)
		lineHistogram.reset()
		if y > 0 {
			jump := math.Abs(s.Rows[y].Mean - s.Rows[y-1].Mean)
			if jump > s.StrongestRowJump {
				s.StrongestRowJump, s.BoundaryRow = jump, y
			}
		}
	}
	for x, a := range columns {
		s.Columns[x] = a.profile(x)
		for y := 0; y < height; y++ {
			lineHistogram.add(pixels[y*width+x])
		}
		s.Columns[x].Median = lineHistogram.median(height)
		lineHistogram.reset()
		if x > 0 {
			jump := math.Abs(s.Columns[x].Mean - s.Columns[x-1].Mean)
			if jump > s.StrongestColumnJump {
				s.StrongestColumnJump, s.BoundaryColumn = jump, x
			}
		}
	}
	for i, count := range bitCounts {
		s.BitSetFraction[i] = float64(count) / float64(s.Count)
	}
	regionResults := []*RegionStats{&s.LeftHalf, &s.RightHalf, &s.EvenColumns, &s.OddColumns}
	for i, result := range regionResults {
		result.Count, result.Mean = regions[i].n, regions[i].mean
		if result.Count > 0 {
			result.Median = quantile(&regionHist[i], result.Count, 0.5)
		}
	}
	for y := 1; y < height; y++ {
		before, after := s.Rows[y-1].Median, s.Rows[y].Median
		if before != after {
			s.RowMedianBoundaries = append(s.RowMedianBoundaries, Boundary{Row: y, Before: before, After: after, Delta: after - before})
		}
	}
	sort.Slice(s.RowMedianBoundaries, func(i, j int) bool {
		a, b := s.RowMedianBoundaries[i], s.RowMedianBoundaries[j]
		if math.Abs(a.Delta) == math.Abs(b.Delta) {
			return a.Row < b.Row
		}
		return math.Abs(a.Delta) > math.Abs(b.Delta)
	})
	if len(s.RowMedianBoundaries) > 5 {
		s.RowMedianBoundaries = s.RowMedianBoundaries[:5]
	}
	s.SHA256 = hex.EncodeToString(hash.Sum(nil))
	p := total.profile(0)
	s.Min, s.Max, s.Mean, s.StdDev = p.Min, p.Max, p.Mean, p.StdDev
	s.ZeroFraction, s.SaturatedFraction, s.NearSaturatedFraction = p.ZeroFraction, p.SaturatedFraction, p.NearSaturatedFraction
	for _, n := range s.Histogram {
		if n > 0 {
			s.UniqueValues++
		}
	}
	s.Median = quantile(&s.Histogram, s.Count, 0.5)
	s.P01 = quantile(&s.Histogram, s.Count, 0.01)
	s.P99 = quantile(&s.Histogram, s.Count, 0.99)
	s.P995 = quantile(&s.Histogram, s.Count, 0.995)
	if s.UniqueValues == 1 {
		s.Warnings = append(s.Warnings, "All pixels have the same digital value.")
	}
	if s.SaturatedFraction >= 0.01 {
		s.Warnings = append(s.Warnings, "At least 1% of pixels equal the 16-bit maximum (65535 ADU).")
	}
	if s.NearSaturatedFraction >= 0.10 {
		s.Warnings = append(s.Warnings, "At least 10% of pixels are at or above 65000 ADU.")
	}
	if s.ZeroFraction >= 0.10 {
		s.Warnings = append(s.Warnings, "At least 10% of pixels equal zero.")
	}
	if s.StrongestRowJump >= 1000 {
		s.Warnings = append(s.Warnings, "Adjacent row means differ by at least 1000 ADU; inspect the row profile and original image.")
	}
	if s.IdenticalAdjacentRows > 0 {
		s.Warnings = append(s.Warnings, "Some adjacent rows are exactly identical; saturation or a uniform field can also cause this.")
	}
	return s
}

// A two-level histogram finds each line's median by inspecting at most 512
// counters per rank, without sorting pixels or scanning 65536 bins per line.
// Reset visits only the values present in the preceding line.
type medianHistogram struct {
	fine    [65536]uint64
	coarse  [256]uint64
	touched []uint16
}

func (h *medianHistogram) add(v uint16) {
	if h.fine[v] == 0 {
		h.touched = append(h.touched, v)
	}
	h.fine[v]++
	h.coarse[v>>8]++
}

func (h *medianHistogram) median(n int) float64 {
	value := func(rank uint64) float64 {
		var seen uint64
		for high, count := range h.coarse {
			if seen+count <= rank {
				seen += count
				continue
			}
			for low := 0; low < 256; low++ {
				v := high*256 + low
				seen += h.fine[v]
				if seen > rank {
					return float64(v)
				}
			}
		}
		return 0
	}
	return (value(uint64((n-1)/2)) + value(uint64(n/2))) / 2
}

func (h *medianHistogram) reset() {
	for _, v := range h.touched {
		h.fine[v] = 0
	}
	clear(h.coarse[:])
	h.touched = h.touched[:0]
}

func quantile(hist *[65536]uint64, n int, q float64) float64 {
	rank := q * float64(n-1)
	lo, hi := uint64(math.Floor(rank)), uint64(math.Ceil(rank))
	var cumulative uint64
	var lowValue float64
	found := false
	for value, count := range hist {
		cumulative += count
		if !found && cumulative > lo {
			lowValue = float64(value)
			found = true
		}
		if cumulative > hi {
			return lowValue + (float64(value)-lowValue)*(rank-math.Floor(rank))
		}
	}
	return 0
}
