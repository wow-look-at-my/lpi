package estimate

import (
	"time"

	"github.com/wow-look-at-my/lpi/internal/progress"
)

// Matcher estimates a live run against several models in parallel, and locks onto the model it fits.
type Matcher struct {
	ch *progress.Chooser
}

// NewMatcher returns a Matcher over models. Given none it never locks, and
// only counts what it saw.
func NewMatcher(models ...*Model) *Matcher {
	cands := make([]progress.Candidate, 0, len(models))
	for _, m := range models {
		cands = append(cands, progress.Candidate{Key: m.Key(), Label: m.Label(), Model: m.m})
	}
	return &Matcher{ch: progress.NewChooser(cands)}
}

// Observe feeds a token the live run emitted at at.
func (mt *Matcher) Observe(tok Token, at time.Time) { mt.ch.ObserveToken(uint64(tok), at) }

// ObserveLine feeds raw log text emitted at at, normalized as TokenOfLine describes.
func (mt *Matcher) ObserveLine(line string, at time.Time) { mt.ch.Observe(line, at) }

// Tick moves the clock to at without observing anything.
func (mt *Matcher) Tick(at time.Time) { mt.ch.Tick(at) }

// Estimate reads the locked model, or an Identifying estimate while nothing has locked.
func (mt *Matcher) Estimate() Estimate { return mt.ch.Snapshot() }

// Locked names the model the run was identified as, when a fit is worth showing.
func (mt *Matcher) Locked() (key, label string, ok bool) { return mt.ch.Locked() }

// Best names the closest model and its match rate, locked or not.
func (mt *Matcher) Best() (key string, matchRate float64, ok bool) { return mt.ch.Best() }

// MergeTarget names the model a finished run belongs in, holding out for a fit good throughout.
func (mt *Matcher) MergeTarget() (key, label string, ok bool) { return mt.ch.MergeTarget() }
