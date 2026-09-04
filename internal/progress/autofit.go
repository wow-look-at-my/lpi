package progress

import (
	"time"

	"github.com/wow-look-at-my/log-progress-indicator/internal/model"
)

// Candidate is stored pattern offered to the Chooser
type Candidate struct {
	Key   string
	Label string // DisplayLabel of the model, for status surfacing
	Model *model.Model
}

// Fit decision thresholds
const (
	lockMinLines    = 12   // never lock before this many counted lines
	earlyLockRate   = 0.80 // rate that locks as soon as lockMinLines is reached
	lockWindowLines = 32   // standard decision point
	lockRate        = 0.50 // minimum rate to lock at/after the window
	switchMargin    = 0.15 // a rival must beat the locked rate by this to
	mergeRate       = 0.60 // minimum final rate to merge the run into the
)

// Chooser feeds every observed line to Estimator
type Chooser struct {
	cands  []Candidate
	ests   []*Estimator
	null   *Estimator
	locked int // index into cands; while unlocked
}

// NewChooser returns a Chooser over the given
func NewChooser(cands []Candidate) *Chooser {
	c := &Chooser{cands: cands, locked: -1, null: NewEstimator(model.New(""))}
	c.ests = make([]*Estimator, len(cands))
	for i, cand := range cands {
		c.ests[i] = NewEstimator(cand.Model)
	}
	return c
}

// Observe feeds live line to every estimator, then
func (c *Chooser) Observe(line string, at time.Time) {
	c.null.Observe(line, at)
	for _, e := range c.ests {
		e.Observe(line, at)
	}
	c.decide()
}

// Tick advances every estimator's clock without a
func (c *Chooser) Tick(at time.Time) {
	c.null.Tick(at)
	for _, e := range c.ests {
		e.Tick(at)
	}
}

// decide applies the lock state machine after a
func (c *Chooser) decide() {
	if len(c.cands) == 0 || c.null.current < lockMinLines {
		return
	}
	best := c.bestIdx()
	br := c.rate(best)
	if c.locked < 0 {
		if br >= earlyLockRate || (c.null.current >= lockWindowLines && br >= lockRate) {
			c.locked = best
		}
		return
	}
	// A rival with full history can steal the lock, but
	if best != c.locked && br >= lockRate && br >= c.rate(c.locked)+switchMargin {
		c.locked = best
	}
}

// rate is candidate i's cumulative match rate
func (c *Chooser) rate(i int) float64 {
	e := c.ests[i]
	if e.current == 0 {
		return 0
	}
	return float64(e.matched) / float64(e.current)
}

// progressOf is candidate i's matched weight
func (c *Chooser) progressOf(i int) float64 {
	return min(c.ests[i].weightDone, 1)
}

// bestIdx returns the current best candidate
func (c *Chooser) bestIdx() int {
	best := 0
	for i := 1; i < len(c.cands); i++ {
		if c.better(i, best) {
			best = i
		}
	}
	return best
}

func (c *Chooser) better(i, j int) bool {
	if ri, rj := c.rate(i), c.rate(j); ri != rj {
		return ri > rj
	}
	if pi, pj := c.progressOf(i), c.progressOf(j); pi != pj {
		return pi > pj
	}
	if ni, nj := len(c.cands[i].Model.Runs), len(c.cands[j].Model.Runs); ni != nj {
		return ni > nj
	}
	return c.cands[i].Key < c.cands[j].Key
}

// Snapshot returns the locked candidate's estimate
func (c *Chooser) Snapshot() Snapshot {
	if c.locked >= 0 {
		s := c.ests[c.locked].Snapshot()
		s.Label = c.cands[c.locked].Label
		return s
	}
	s := c.null.Snapshot()
	s.Identifying = len(c.cands) > 0
	return s
}

// Locked reports the pattern the Chooser has locked
func (c *Chooser) Locked() (key, label string, ok bool) {
	if c.locked < 0 {
		return "", "", false
	}
	return c.cands[c.locked].Key, c.cands[c.locked].Label, true
}

// Best reports the current best candidate by
func (c *Chooser) Best() (key string, rate float64, ok bool) {
	if len(c.cands) == 0 || c.null.current == 0 {
		return "", 0, false
	}
	i := c.bestIdx()
	return c.cands[i].Key, c.rate(i), true
}

// FinalRate returns key's cumulative match rate for
func (c *Chooser) FinalRate(key string) float64 {
	for i, cand := range c.cands {
		if cand.Key == key {
			return c.rate(i)
		}
	}
	return 0
}

// MergeTarget returns the pattern a finished run
func (c *Chooser) MergeTarget() (key, label string, ok bool) {
	if c.locked < 0 || c.rate(c.locked) < mergeRate {
		return "", "", false
	}
	return c.cands[c.locked].Key, c.cands[c.locked].Label, true
}
