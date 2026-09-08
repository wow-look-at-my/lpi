package lpi

import (
	"time"

	"github.com/wow-look-at-my/lpi/internal/progress"
)

// Matcher estimates a live run against several models at once, for a caller
// who does not know which reference applies. It scores every model as the run
// emits tokens and locks onto the one the output actually fits.
//
// Until it locks, Estimate reports Identifying and no useful progress. After
// it locks, Estimate is the locked model's reading, tagged with that model's
// Label. A clearly better rival can still take the lock later.
type Matcher struct {
	ch *progress.Chooser
}

// NewMatcher returns a Matcher over models. With no models it never locks, and
// only counts what it saw, which is what recording a first run looks like.
func NewMatcher(models ...*Model) *Matcher {
	cands := make([]progress.Candidate, 0, len(models))
	for _, m := range models {
		cands = append(cands, progress.Candidate{Key: m.Key(), Label: m.Label(), Model: m.m})
	}
	return &Matcher{ch: progress.NewChooser(cands)}
}

// Observe feeds one token emitted by the live run, at time at.
func (mt *Matcher) Observe(tok Token, at time.Time) { mt.ch.ObserveToken(uint64(tok), at) }

// ObserveLine feeds one raw line of log text emitted at at, normalized as
// TokenOfLine describes.
func (mt *Matcher) ObserveLine(line string, at time.Time) { mt.ch.Observe(line, at) }

// Tick moves the clock to at without observing anything.
func (mt *Matcher) Tick(at time.Time) { mt.ch.Tick(at) }

// Estimate reads the locked model's current state, or an Identifying estimate
// while nothing has locked yet.
func (mt *Matcher) Estimate() Estimate { return mt.ch.Snapshot() }

// Locked reports the model the run has been identified as, once one fits well
// enough for its estimate to be worth showing.
func (mt *Matcher) Locked() (key, label string, ok bool) { return mt.ch.Locked() }

// Best reports the closest model so far and the share of live tokens it
// matched, locked or not. It is the honest answer while Locked says no.
func (mt *Matcher) Best() (key string, matchRate float64, ok bool) { return mt.ch.Best() }

// MergeTarget names the model a finished run should be recorded into: the
// locked model, and only when the run matched it well enough throughout that
// merging it improves the reference rather than blurring it. When ok is false
// the run belongs under a new key, which ContentKey can derive.
func (mt *Matcher) MergeTarget() (key, label string, ok bool) { return mt.ch.MergeTarget() }
