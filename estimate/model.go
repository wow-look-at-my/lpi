package estimate

import (
	"time"

	"github.com/wow-look-at-my/lpi/internal/model"
)

// MaxRuns is how many reference runs a Model keeps: Add evicts the oldest beyond it.
const MaxRuns = model.MaxRuns

// Model is the merged expectation of what a task emits, built from up to MaxRuns runs of it.
type Model struct {
	m *model.Model
}

// NewModel returns an empty model under key, which estimates nothing until a run is added.
func NewModel(key string) *Model { return &Model{m: model.New(key)} }

// Add merges a completed Run in and recomputes what the model expects.
func (m *Model) Add(r *Run) { m.m.AddRun(r) }

// AddLabel records a human-facing name. Label reports the newest.
func (m *Model) AddLabel(label string) { m.m.AddInvocation(label) }

// Key is the model's storage key.
func (m *Model) Key() string { return m.m.Key }

// Label is the model's display name: its newest label, else the key.
func (m *Model) Label() string { return m.m.DisplayLabel() }

// Runs is how many reference runs the model holds.
func (m *Model) Runs() int { return len(m.m.Runs) }

// Units is the token occurrences a full run is expected to emit: Estimate's denominator.
func (m *Model) Units() int { return m.m.TotalUnits }

// RefDuration is how long a reference run takes, and is unset without HasTimes.
func (m *Model) RefDuration() time.Duration { return m.m.RefDuration }

// HasTimes reports whether any recorded run carried a usable clock.
func (m *Model) HasTimes() bool { return m.m.HasTimes }

// Save writes the model to path atomically, as a small gzipped file.
func (m *Model) Save(path string) error { return m.m.Save(path) }

// LoadModel reads a model written by Save.
func LoadModel(path string) (*Model, error) {
	inner, err := model.Load(path)
	if err != nil {
		return nil, err
	}
	return &Model{m: inner}, nil
}

// ContentKey derives a storage key from what a run emitted: same token multiset, same key.
func ContentKey(r *Run) string { return model.AutoKey(r) }
