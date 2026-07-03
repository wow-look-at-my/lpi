package model

import (
	"slices"
	"time"
)

// MaxRuns is the maximum number of reference runs kept per model; older runs
// are evicted FIFO.
const MaxRuns = 8

// Model is the merged expectation built from up to MaxRuns reference runs.
type Model struct {
	Key  string
	Runs []*Run

	// Derived by Rebuild:

	// Expect maps each fingerprint to its expected occurrences.
	Expect map[uint64][]Occurrence
	// TotalUnits is the total expected line count (sum of expected
	// occurrence counts).
	TotalUnits int
	// RefDuration is the upper-median duration of the timed runs (0 when
	// no run has times).
	RefDuration time.Duration
	// HasTimes reports whether any run has usable times.
	HasTimes bool
}

// New returns an empty model for key.
func New(key string) *Model {
	return &Model{Key: key, Expect: make(map[uint64][]Occurrence)}
}

// AddRun appends a run, evicts the oldest beyond MaxRuns, and rebuilds.
func (m *Model) AddRun(r *Run) {
	m.Runs = append(m.Runs, r)
	if len(m.Runs) > MaxRuns {
		m.Runs = slices.Delete(m.Runs, 0, len(m.Runs)-MaxRuns)
	}
	m.Rebuild()
}

// Rebuild recomputes the derived fields from Runs.
func (m *Model) Rebuild() {
	m.Expect = make(map[uint64][]Occurrence)
	m.TotalUnits = 0
	m.RefDuration = 0
	m.HasTimes = false
	n := len(m.Runs)
	if n == 0 {
		return
	}
	fps := make(map[uint64]struct{})
	for _, r := range m.Runs {
		for fp := range r.Occ {
			fps[fp] = struct{}{}
		}
	}
	counts := make([]int, n)
	for fp := range fps {
		for i, r := range m.Runs {
			counts[i] = len(r.Occ[fp])
		}
		slices.Sort(counts)
		// Upper median: with 2 runs this is the max, so one incremental or
		// truncated run cannot drop expected lines.
		expect := counts[n/2]
		if expect == 0 {
			continue
		}
		m.Expect[fp] = m.mergeOccurrences(fp, expect)
		m.TotalUnits += expect
	}
	m.renormalizeWeights()
	m.deriveDuration()
}

// mergeOccurrences averages occurrence k's fractions over the runs that have
// that occurrence.
func (m *Model) mergeOccurrences(fp uint64, expect int) []Occurrence {
	occs := make([]Occurrence, expect)
	for k := 0; k < expect; k++ {
		var tf, wf float64
		cnt := 0
		for _, r := range m.Runs {
			if list := r.Occ[fp]; k < len(list) {
				tf += float64(list[k].TimeFrac)
				wf += float64(list[k].WeightFrac)
				cnt++
			}
		}
		if cnt > 0 {
			occs[k] = Occurrence{
				TimeFrac:   float32(tf / float64(cnt)),
				WeightFrac: float32(wf / float64(cnt)),
			}
		}
	}
	return occs
}

// renormalizeWeights scales all WeightFrac so their grand total is 1.
func (m *Model) renormalizeWeights() {
	var total float64
	for _, occs := range m.Expect {
		for _, o := range occs {
			total += float64(o.WeightFrac)
		}
	}
	if total <= 0 {
		return
	}
	for _, occs := range m.Expect {
		for i := range occs {
			occs[i].WeightFrac = float32(float64(occs[i].WeightFrac) / total)
		}
	}
}

// deriveDuration sets RefDuration to the upper-median duration among timed
// runs and HasTimes when any run has times.
func (m *Model) deriveDuration() {
	var durs []time.Duration
	for _, r := range m.Runs {
		if r.HasTimes {
			m.HasTimes = true
			durs = append(durs, r.Duration)
		}
	}
	if len(durs) > 0 {
		slices.Sort(durs)
		m.RefDuration = durs[len(durs)/2]
	}
}
