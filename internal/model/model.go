package model

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"slices"
	"time"
)

// MaxRuns is the maximum number of reference runs
const MaxRuns = 8

// MaxInvocations is the maximum number of
const MaxInvocations = 5

// Model is the merged expectation built from up to
type Model struct {
	Key  string
	Runs []*Run

	// Invocations are the command lines recorded as
	Invocations []string

	// Derived by Rebuild

	// Expect maps each fingerprint to its expected
	Expect map[uint64][]Occurrence
	// TotalUnits is the total expected line count (sum
	TotalUnits int
	// RefDuration is the upper-median duration of the
	RefDuration time.Duration
	// HasTimes reports whether any run has usable times
	HasTimes bool
	// Timeline lists every expected occurrence in the order the reference
	Timeline []Point
}

// Point places an expected occurrence on the reference timeline, so a live
// run can tell which expectations it has gone past.
type Point struct {
	FP     uint64
	Idx    int
	At     float32
	Weight float32
}

// New returns an empty model for key
func New(key string) *Model {
	return &Model{Key: key, Expect: make(map[uint64][]Occurrence)}
}

// AddRun appends a run, evicts the oldest beyond
func (m *Model) AddRun(r *Run) {
	m.Runs = append(m.Runs, r)
	if len(m.Runs) > MaxRuns {
		m.Runs = slices.Delete(m.Runs, 0, len(m.Runs)-MaxRuns)
	}
	m.Rebuild()
}

// AddInvocation records cmd as the most recent
func (m *Model) AddInvocation(cmd string) {
	if cmd == "" {
		return
	}
	if i := slices.Index(m.Invocations, cmd); i >= 0 {
		m.Invocations = slices.Delete(m.Invocations, i, i+1)
	}
	m.Invocations = slices.Insert(m.Invocations, 0, cmd)
	if len(m.Invocations) > MaxInvocations {
		m.Invocations = m.Invocations[:MaxInvocations]
	}
}

// DisplayLabel is the model's human-facing name
func (m *Model) DisplayLabel() string {
	if len(m.Invocations) > 0 {
		return m.Invocations[0]
	}
	return m.Key
}

// AutoKey derives the storage id for an
func AutoKey(r *Run) string {
	fps := make([]uint64, 0, len(r.Occ))
	for fp := range r.Occ {
		fps = append(fps, fp)
	}
	slices.Sort(fps)
	h := fnv.New64a()
	var buf [8]byte
	for _, fp := range fps {
		binary.BigEndian.PutUint64(buf[:], fp)
		h.Write(buf[:])
		binary.BigEndian.PutUint64(buf[:], uint64(len(r.Occ[fp])))
		h.Write(buf[:])
	}
	return fmt.Sprintf("auto.%016x", h.Sum64())
}

// Rebuild recomputes the derived fields from Runs
func (m *Model) Rebuild() {
	m.Expect = make(map[uint64][]Occurrence)
	m.TotalUnits = 0
	m.RefDuration = 0
	m.HasTimes = false
	n := len(m.Runs)
	if n == 0 {
		return
	}
	m.deriveDuration()
	scales, norm := m.runScales()
	var fps []uint64
	for _, r := range m.Runs {
		for fp := range r.Occ {
			fps = append(fps, fp)
		}
	}
	slices.Sort(fps)
	fps = slices.Compact(fps)
	counts := make([]int, n)
	for _, fp := range fps {
		for i, r := range m.Runs {
			counts[i] = len(r.Occ[fp])
		}
		slices.Sort(counts)
		// Upper median: with runs this is the max, so
		expect := counts[n/2]
		if expect == 0 {
			continue
		}
		m.Expect[fp] = m.mergeOccurrences(fp, expect, scales)
		m.TotalUnits += expect
	}
	m.renormalize(norm)
	m.buildTimeline()
}

// buildTimeline lays every expected occurrence out in reference order.
func (m *Model) buildTimeline() {
	m.Timeline = make([]Point, 0, m.TotalUnits)
	for fp, occs := range m.Expect {
		for i, o := range occs {
			m.Timeline = append(m.Timeline, Point{FP: fp, Idx: i, At: o.TimeFrac, Weight: o.WeightFrac})
		}
	}
	slices.SortFunc(m.Timeline, func(a, b Point) int {
		if a.At != b.At {
			return cmp.Compare(a.At, b.At)
		}
		if a.FP != b.FP {
			return cmp.Compare(a.FP, b.FP)
		}
		return cmp.Compare(a.Idx, b.Idx)
	})
}

// runScales returns the factor that turns each run's fractions into seconds,
// plus the divisor that turns seconds back into fractions of the merged run.
// A run's fractions are shares of ITS duration, so a log covering a sliver of
// the work states shares that are far too large.
func (m *Model) runScales() ([]float64, float64) {
	norm := m.RefDuration.Seconds()
	if norm <= 0 {
		norm = 1
	}
	scales := make([]float64, len(m.Runs))
	for i, r := range m.Runs {
		scales[i] = norm
		if r.HasTimes && r.Duration > 0 {
			scales[i] = r.Duration.Seconds()
		}
	}
	return scales, norm
}

// mergeOccurrences averages occurrence k across the runs that have it, in
// seconds, and scales the weight by the share of runs that print the line at
// all: work only some runs do is only that often expected of the next.
func (m *Model) mergeOccurrences(fp uint64, expect int, scales []float64) []Occurrence {
	occs := make([]Occurrence, expect)
	for k := 0; k < expect; k++ {
		var tf, wf float64
		cnt := 0
		for i, r := range m.Runs {
			if list := r.Occ[fp]; k < len(list) {
				tf += float64(list[k].TimeFrac) * scales[i]
				wf += float64(list[k].WeightFrac) * scales[i]
				cnt++
			}
		}
		if cnt == 0 {
			continue
		}
		at := tf / float64(cnt)
		occs[k] = Occurrence{
			TimeFrac:   float32(at),
			WeightFrac: float32(wf / float64(cnt) * support(cnt, at, scales)),
		}
	}
	return occs
}

// support is the share of runs that could have printed this line and did. A
// run ending before this point never had the chance: short, not different.
func support(have int, at float64, scales []float64) float64 {
	could := 0
	for _, s := range scales {
		if s+1e-9 >= at {
			could++
		}
	}
	if could < have {
		could = have
	}
	return float64(have) / float64(could)
}

// renormalize turns the merged seconds back into fractions: weights that add
// up across the model, and times as a share of the reference duration.
func (m *Model) renormalize(norm float64) {
	var total float64
	for _, occs := range m.Expect {
		for _, o := range occs {
			total += float64(o.WeightFrac)
		}
	}
	for _, occs := range m.Expect {
		for i := range occs {
			if total > 0 {
				occs[i].WeightFrac = float32(float64(occs[i].WeightFrac) / total)
			}
			occs[i].TimeFrac = float32(min(float64(occs[i].TimeFrac)/norm, 1))
		}
	}
}

// deriveDuration sets RefDuration to the
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
