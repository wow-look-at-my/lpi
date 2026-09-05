// Package progress estimates how far along a live
package progress

import (
	"slices"
	"time"

	"github.com/wow-look-at-my/lpi/internal/fingerprint"
	"github.com/wow-look-at-my/lpi/internal/model"
)

// Snapshot is a point-in-time progress estimate
type Snapshot struct {
	// Progress is the primary estimate in
	Progress   float64
	UnitsDone  int
	UnitsTotal int
	UnitsPct   float64
	HasTimes   bool

	Elapsed      time.Duration
	ElapsedKnown bool
	RefDuration  time.Duration

	ETA     time.Duration
	ETAKind string  // "pace" | "ref-pace" | "none"
	Pace    float64 // elapsed vs reference speed ratio; if unknown
	// PaceApplied is the pace the ETA was actually
	PaceApplied float64

	MatchRate  float64
	Confidence string // "high" | "medium" | "low" | "none"
	// Skipped is the share of the reference this run went past without
	Skipped float64

	// Identifying is set by the Chooser while it is
	Identifying bool
	// Label is the display label of the pattern the
	Label string

	CurrentLines, MatchedLines, NovelLines, OverflowLines int
}

// The position is the earliest recent match, so a stray match ahead cannot
// drag the run forward. The window scales with the reference.
const (
	windowMax   = 32
	windowMin   = 8
	windowShare = 8
)

// windowFor sizes the position window for a reference of the given length.
func windowFor(units int) int {
	return min(windowMax, max(windowMin, units/windowShare))
}

// Estimator consumes live log lines and tracks
type Estimator struct {
	m          *model.Model
	seen       map[uint64]int
	weightDone float64
	current    int
	matched    int
	novel      int
	overflow   int
	firstAt    time.Time
	lastAt     time.Time

	// position is how far into the reference the run has been seen to reach,
	position float64
	skipped  float64
	window   []float32
	span     int
	cursor   int
}

// NewEstimator returns an Estimator matching
func NewEstimator(m *model.Model) *Estimator {
	span := windowFor(m.TotalUnits)
	return &Estimator{m: m, seen: make(map[uint64]int), span: span, window: make([]float32, 0, span)}
}

// Observe feeds live log line, stamped with the
func (e *Estimator) Observe(line string, at time.Time) {
	norm := fingerprint.Normalize(line)
	if norm == "" {
		return
	}
	e.current++
	fp := fingerprint.Sum64(norm)
	occs, known := e.m.Expect[fp]
	switch {
	case !known:
		e.novel++
	case e.seen[fp] < len(occs):
		// The k-th occurrence in the current log matches
		o := occs[e.seen[fp]]
		e.weightDone += float64(o.WeightFrac)
		e.seen[fp]++
		e.matched++
		e.advance(o)
	default:
		e.overflow++
	}
	if !at.IsZero() {
		if e.firstAt.IsZero() {
			e.firstAt = at
		}
		e.bumpClock(at)
	}
}

// advance moves the run's position along the reference and retires the
// expectations it has gone past. A reference line this run never printed is
// work this run does not do, so its share stops counting against the estimate
// rather than capping the bar below the end.
func (e *Estimator) advance(matched model.Occurrence) {
	if float64(matched.TimeFrac) < e.position {
		e.skipped -= float64(matched.WeightFrac) // it arrived after all
	}
	if len(e.window) < e.span {
		e.window = append(e.window, matched.TimeFrac)
	} else {
		copy(e.window, e.window[1:])
		e.window[len(e.window)-1] = matched.TimeFrac
	}
	if len(e.window) < e.span {
		return // too few matches to place the run yet
	}
	behind := float64(slices.Min(e.window))
	if behind <= e.position {
		return
	}
	e.position = behind
	for e.cursor < len(e.m.Timeline) && float64(e.m.Timeline[e.cursor].At) < e.position {
		p := e.m.Timeline[e.cursor]
		if e.seen[p.FP] <= p.Idx {
			e.skipped += float64(p.Weight)
		}
		e.cursor++
	}
}

// Tick advances the clock without a line, so
func (e *Estimator) Tick(at time.Time) {
	if !at.IsZero() {
		e.bumpClock(at)
	}
}

// bumpClock moves lastAt forward monotonically
func (e *Estimator) bumpClock(at time.Time) {
	if at.After(e.lastAt) {
		e.lastAt = at
	}
}

// ETA gating thresholds
const (
	minPaceProgress    = 0.02
	minPaceMatches     = 5
	minRefPaceProgress = 0.001
)

// Snapshot returns the current estimate
func (e *Estimator) Snapshot() Snapshot {
	s := Snapshot{
		UnitsDone:     e.matched,
		UnitsTotal:    e.m.TotalUnits,
		HasTimes:      e.m.HasTimes,
		RefDuration:   e.m.RefDuration,
		ETAKind:       "none",
		CurrentLines:  e.current,
		MatchedLines:  e.matched,
		NovelLines:    e.novel,
		OverflowLines: e.overflow,
	}
	// Retired expectations leave the denominator.
	s.Skipped = e.skipped
	s.Progress = e.weightDone
	if reachable := 1 - e.skipped; reachable > 0 {
		s.Progress /= reachable
	}
	if s.Progress > 1 {
		s.Progress = 1
	}
	if s.UnitsTotal > 0 {
		s.UnitsPct = float64(s.UnitsDone) / float64(s.UnitsTotal)
	}
	if !e.firstAt.IsZero() && !e.lastAt.IsZero() {
		s.Elapsed = e.lastAt.Sub(e.firstAt)
		s.ElapsedKnown = true
	}
	e.fillETA(&s)
	if e.current > 0 {
		s.MatchRate = float64(e.matched) / float64(e.current)
	}
	if s.UnitsTotal == 0 {
		// An empty model (baseline recording) has nothing
		s.Confidence = "none"
	} else {
		s.Confidence = confidence(e.current, s.MatchRate)
	}
	return s
}

// fillETA applies the ETA rules: a pace-adjusted
func (e *Estimator) fillETA(s *Snapshot) {
	ref := s.RefDuration.Seconds()
	switch {
	case s.ElapsedKnown && s.Progress >= minPaceProgress && s.MatchedLines >= minPaceMatches && ref > 0:
		s.Pace = s.Elapsed.Seconds() / (s.Progress * ref)
		// A pace from a sliver of the run counts only in proportion to that sliver.
		s.PaceApplied = 1 + (s.Pace-1)*s.Progress
		s.ETAKind = "pace"
		s.ETA = time.Duration((1 - s.Progress) * ref * s.PaceApplied * float64(time.Second))
	case !s.ElapsedKnown && ref > 0 && s.Progress >= minRefPaceProgress:
		s.ETAKind = "ref-pace"
		s.ETA = time.Duration((1 - s.Progress) * ref * float64(time.Second))
	}
	if s.ETA < 0 {
		s.ETA = 0
	}
}

func confidence(lines int, matchRate float64) string {
	switch {
	case lines == 0:
		return "none"
	case matchRate >= 0.9:
		return "high"
	case matchRate >= 0.6:
		return "medium"
	default:
		return "low"
	}
}
