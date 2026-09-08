package timeparse

import "time"

// DetectLines is how many leading lines a reader samples to pick a format.
const DetectLines = 300

// Stamper is the clock of a line stream: stamps read, carried over unstamped
// lines, never backwards. Shared, so no path can disagree.
type Stamper struct {
	format *Format
	last   time.Time
	have   bool
}

// NewStamper returns a Stamper reading stamps with format, nil to read none.
func NewStamper(format *Format) *Stamper { return &Stamper{format: format} }

// Format is the reader the Stamper parses with.
func (s *Stamper) Format() *Format { return s.format }

// Last is the effective time of the newest line, unset until a stamp is read.
func (s *Stamper) Last() time.Time { return s.last }

// Stamp reads line's own stamp and folds it into the stream's clock.
func (s *Stamper) Stamp(line string) (eff time.Time, gap time.Duration, timed bool) {
	var at time.Time
	if s.format != nil {
		if t, ok := s.format.Parse(line); ok {
			at = t
		}
	}
	return s.At(at)
}

// At folds an observed time into the stream's clock. An unset at carries the
// previous time, and a backwards at is clamped to it. gap is the distance from
// the previous line. timed is false until the stream has been stamped at all.
func (s *Stamper) At(at time.Time) (eff time.Time, gap time.Duration, timed bool) {
	switch {
	case !at.IsZero():
		eff = at
		if s.have {
			if eff.Before(s.last) {
				eff = s.last
			}
			gap = eff.Sub(s.last)
		}
		s.last, s.have = eff, true
		return eff, gap, true
	case s.have:
		return s.last, 0, true
	default:
		return time.Time{}, 0, false
	}
}
