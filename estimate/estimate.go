package estimate

import (
	"time"

	"github.com/wow-look-at-my/lpi/internal/progress"
)

// Estimate reads a live run: Progress, ETA (valid unless ETAKind is "none"), units, Confidence.
type Estimate = progress.Snapshot

// Estimator scores a live run against a Model. It consumes the run's tokens as
// they are emitted, and answers with an Estimate on demand.
type Estimator struct {
	est *progress.Estimator
}

// NewEstimator returns an Estimator scoring against m.
func NewEstimator(m *Model) *Estimator {
	return &Estimator{est: progress.NewEstimator(m.m)}
}

// Observe feeds a token the live run emitted at at. An unset at leaves the clock alone.
func (e *Estimator) Observe(tok Token, at time.Time) { e.est.ObserveToken(uint64(tok), at) }

// ObserveLine feeds raw log text emitted at at, normalized as TokenOfLine describes.
func (e *Estimator) ObserveLine(line string, at time.Time) { e.est.Observe(line, at) }

// Tick moves the clock to at without observing: how an untimed run gets a paced ETA.
func (e *Estimator) Tick(at time.Time) { e.est.Tick(at) }

// Estimate reads the current state, cheaply enough for every token or a redraw timer.
func (e *Estimator) Estimate() Estimate { return e.est.Snapshot() }
