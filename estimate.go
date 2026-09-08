package lpi

import (
	"time"

	"github.com/wow-look-at-my/lpi/internal/progress"
)

// Estimate is a point-in-time reading of a live run.
//
// Progress is the primary number, in 0..1: the share of the reference run's
// time the live run has covered. UnitsDone and UnitsTotal are the raw token
// counts behind it. ETA is valid only when ETAKind is not "none". Confidence
// grades the reading by how many live tokens matched the reference at all:
// output the reference never saw drags it down, and a "low" reading is one to
// show with a caveat or not at all.
type Estimate = progress.Snapshot

// Estimator scores a live run against a Model. It consumes the run's tokens as
// they are emitted and answers with an Estimate whenever one is wanted.
type Estimator struct {
	est *progress.Estimator
}

// NewEstimator returns an Estimator scoring against m.
func NewEstimator(m *Model) *Estimator {
	return &Estimator{est: progress.NewEstimator(m.m)}
}

// Observe feeds one token emitted by the live run, at time at. An unset at
// leaves the clock where it is, which costs the ETA nothing when other tokens
// are timed and means there is no ETA when none are.
func (e *Estimator) Observe(tok Token, at time.Time) { e.est.ObserveToken(uint64(tok), at) }

// ObserveLine feeds one raw line of log text emitted at at, normalized as
// TokenOfLine describes. A line with nothing identifying in it is dropped.
func (e *Estimator) ObserveLine(line string, at time.Time) { e.est.Observe(line, at) }

// Tick moves the clock to at without observing anything. A run whose tokens
// carry no times of their own gets its elapsed time, and so its paced ETA,
// from a caller ticking the wall clock.
func (e *Estimator) Tick(at time.Time) { e.est.Tick(at) }

// Estimate reads the current state. It is cheap enough to call on every token
// or on a redraw timer.
func (e *Estimator) Estimate() Estimate { return e.est.Snapshot() }
