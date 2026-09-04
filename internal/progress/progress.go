// Package progress estimates how far along a live
package progress

import (
	"time"

	"github.com/wow-look-at-my/log-progress-indicator/internal/fingerprint"
	"github.com/wow-look-at-my/log-progress-indicator/internal/model"
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

	MatchRate  float64
	Confidence string // "high" | "medium" | "low" | "none"

	// Identifying is set by the Chooser while it is
	Identifying bool
	// Label is the display label of the pattern the
	Label string

	CurrentLines, MatchedLines, NovelLines, OverflowLines int
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
}

// NewEstimator returns an Estimator matching
func NewEstimator(m *model.Model) *Estimator {
	return &Estimator{m: m, seen: make(map[uint64]int)}
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
		e.weightDone += float64(occs[e.seen[fp]].WeightFrac)
		e.seen[fp]++
		e.matched++
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
	s.Progress = e.weightDone
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
		s.ETAKind = "pace"
		s.ETA = time.Duration((1 - s.Progress) * ref * s.Pace * float64(time.Second))
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
