package estimate

import (
	"time"

	"github.com/wow-look-at-my/lpi/internal/progress"
)

// Estimate reads a live run: Progress, ETA (valid unless ETAKind is "none"), units, Confidence.
type Estimate = progress.Snapshot

// Estimator scores a live run against a Model. It consumes what the run emits,
// and answers with an Estimate on demand.
type Estimator[T any] struct {
	tok Tokenizer[T]
	est *progress.Estimator
}

// NewEstimator returns an Estimator scoring against m, reading values with tok.
func NewEstimator[T any](m *Model, tok Tokenizer[T]) *Estimator[T] {
	return &Estimator[T]{tok: tok, est: progress.NewEstimator(m.m)}
}

// Observe feeds what the live run emitted at at. An unset at leaves the clock alone.
func (e *Estimator[T]) Observe(v T, at time.Time) {
	if tok, ok := e.tok(v); ok {
		e.est.ObserveToken(uint64(tok), at)
	}
}

// Tick moves the clock to at without observing: how an untimed run gets a paced ETA.
func (e *Estimator[T]) Tick(at time.Time) { e.est.Tick(at) }

// Estimate reads the current state, cheaply enough for every value or a redraw timer.
func (e *Estimator[T]) Estimate() Estimate { return e.est.Snapshot() }
