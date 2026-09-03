package model

import (
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
		// Upper median: with runs this is the max, so
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

// mergeOccurrences averages occurrence k's
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

// renormalizeWeights scales all WeightFrac so their
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
