package lpi

import (
	"time"

	"github.com/wow-look-at-my/lpi/internal/model"
)

// MaxRuns is how many reference runs a Model keeps. Add evicts the oldest
// beyond this.
const MaxRuns = model.MaxRuns

// Model is the merged expectation of what a task emits, built from up to
// MaxRuns completed runs of it. An Estimator scores a live run against one.
type Model struct {
	m *model.Model
}

// NewModel returns an empty model under key. An empty model estimates nothing
// (Estimate reports confidence "none"), which is how a first run is recorded
// while it is still the only one.
func NewModel(key string) *Model { return &Model{m: model.New(key)} }

// Add merges a completed Run into the model and recomputes its expectations.
// Expected counts take the upper median across runs, and times and weights
// average in seconds, so one short or partial run cannot inflate its share.
func (m *Model) Add(r *Run) { m.m.AddRun(r) }

// AddLabel records a human-facing name for the model, most recent first, and
// keeps the last few. Label reports the newest one.
func (m *Model) AddLabel(label string) { m.m.AddInvocation(label) }

// Key is the model's storage key.
func (m *Model) Key() string { return m.m.Key }

// Label is the model's display name: the newest label if one was recorded,
// else the key.
func (m *Model) Label() string { return m.m.DisplayLabel() }

// Runs is how many reference runs the model holds.
func (m *Model) Runs() int { return len(m.m.Runs) }

// Units is the total number of token occurrences a full run is expected to
// emit. It is the denominator behind Estimate's UnitsDone/UnitsTotal.
func (m *Model) Units() int { return m.m.TotalUnits }

// RefDuration is how long a reference run takes. It is unset, and HasTimes is
// false, when no recorded run carried a usable clock; there is no ETA then.
func (m *Model) RefDuration() time.Duration { return m.m.RefDuration }

// HasTimes reports whether any recorded run carried a usable clock.
func (m *Model) HasTimes() bool { return m.m.HasTimes }

// Save writes the model to path as one small gzipped file. The write is
// atomic (temp file plus rename), so an interrupted save cannot corrupt an
// existing model.
func (m *Model) Save(path string) error { return m.m.Save(path) }

// LoadModel reads a model written by Save.
func LoadModel(path string) (*Model, error) {
	inner, err := model.Load(path)
	if err != nil {
		return nil, err
	}
	return &Model{m: inner}, nil
}

// ContentKey derives a storage key from what a run emitted, not from what
// launched it. Two runs that emit the same token multiset get the same key, so
// a caller with no name to give a pattern can still file it and find it again.
func ContentKey(r *Run) string { return model.AutoKey(r) }
