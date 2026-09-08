package estimate

import (
	"time"

	"github.com/wow-look-at-my/lpi/internal/progress"
)

// Matcher estimates a live run against several models in parallel, and locks onto the model it fits.
type Matcher[T any] struct {
	tok Tokenizer[T]
	ch  *progress.Chooser
}

// NewMatcher returns a Matcher over models, reading values with tok. Given no
// models it never locks, and only counts what it saw.
func NewMatcher[T any](tok Tokenizer[T], models ...*Model) *Matcher[T] {
	cands := make([]progress.Candidate, 0, len(models))
	for _, m := range models {
		cands = append(cands, progress.Candidate{Key: m.Key(), Label: m.Label(), Model: m.m})
	}
	return &Matcher[T]{tok: tok, ch: progress.NewChooser(cands)}
}

// Observe feeds what the live run emitted at at.
func (mt *Matcher[T]) Observe(v T, at time.Time) {
	if tok, ok := mt.tok(v); ok {
		mt.ch.ObserveToken(uint64(tok), at)
	}
}

// Tick moves the clock to at without observing anything.
func (mt *Matcher[T]) Tick(at time.Time) { mt.ch.Tick(at) }

// Estimate reads the locked model, or an Identifying estimate while nothing has locked.
func (mt *Matcher[T]) Estimate() Estimate { return mt.ch.Snapshot() }

// Locked names the model the run was identified as, when a fit is worth showing.
func (mt *Matcher[T]) Locked() (key, label string, ok bool) { return mt.ch.Locked() }

// Best names the closest model and its match rate, locked or not.
func (mt *Matcher[T]) Best() (key string, matchRate float64, ok bool) { return mt.ch.Best() }

// MergeTarget names the model a finished run belongs in, holding out for a fit good throughout.
func (mt *Matcher[T]) MergeTarget() (key, label string, ok bool) { return mt.ch.MergeTarget() }
