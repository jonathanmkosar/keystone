package operations

import (
	"math"
	"time"
)

// Point is where a limit stood at one moment.
type Point struct {
	At   time.Time `json:"at"`
	Used int64     `json:"used"`
	Of   int64     `json:"of"`
}

// Trend is how a limit has moved over its recent past, and where it is
// heading at that rate.
type Trend struct {
	// Points are oldest first; the last is the reading now. A trend needs
	// two points at different times to say anything more.
	Points []Point `json:"points"`
	// Known says there are enough points to tell a rate.
	Known bool `json:"known"`
	// PerDay is how much the limit's use grew in a day, in its unit, as the
	// line that fits the points best: negative when it shrank.
	PerDay int64 `json:"per_day"`
	// FullAt is when its use would reach the limit at that rate. Absent
	// when it is not growing, is full already, or would take longer than a
	// hundred years.
	FullAt *time.Time `json:"full_at,omitempty"`
	// Resets is when the limit starts again from nothing: the next month,
	// for the monthly cap and the memory reserve.
	Resets *time.Time `json:"resets,omitempty"`
	// AtReset is what would be used by the time it starts again, at that
	// rate: "at this rate, $51 by the end of the month".
	AtReset *int64 `json:"at_reset,omitempty"`
}

// trend fits a line through the points by least squares, with time in
// days, and says when the use would reach the limit at its slope.
func trend(points []Point, used, of int64, now time.Time, resets *time.Time) Trend {
	t := Trend{Points: points, Resets: resets}
	if t.Points == nil {
		t.Points = []Point{}
	}
	if len(points) < 2 || !points[len(points)-1].At.After(points[0].At) {
		return t
	}
	t0 := points[0].At
	var sx, sy, sxx, sxy float64
	n := float64(len(points))
	for _, p := range points {
		x := p.At.Sub(t0).Hours() / 24
		y := float64(p.Used)
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	den := n*sxx - sx*sx
	if den <= 0 {
		return t
	}
	slope := (n*sxy - sx*sy) / den
	if math.IsNaN(slope) || math.IsInf(slope, 0) {
		return t
	}
	t.Known = true
	t.PerDay = int64(math.Round(slope))
	if slope > 0 && of > 0 && used < of {
		days := float64(of-used) / slope
		if days < 100*365 {
			at := now.UTC().Add(time.Duration(days * 24 * float64(time.Hour))).Truncate(time.Minute)
			t.FullAt = &at
		}
	}
	if resets != nil && resets.After(now) && slope >= 0 {
		v := used + int64(math.Round(slope*resets.Sub(now).Hours()/24))
		t.AtReset = &v
	}
	return t
}
