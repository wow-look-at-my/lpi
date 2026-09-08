package timeparse

import "time"

// DetectLines is how many leading lines a reader samples to pick a format.
const DetectLines = 300

// Detector buffers the leading lines of a stream until it can pick a format.
// A pinned format is ready immediately and buffers nothing.
type Detector struct {
	format *Format
	pinned bool
	sample []string
}

// NewDetector returns a Detector. A nil format means detect from the text.
func NewDetector(format *Format) *Detector {
	return &Detector{format: format, pinned: format != nil}
}

// Add buffers a line and reports whether the Detector can decide now.
func (d *Detector) Add(line string) bool {
	d.sample = append(d.sample, line)
	return d.Ready()
}

// Ready reports whether Decide has all it wants: a pinned format, or a full sample.
func (d *Detector) Ready() bool { return d.pinned || len(d.sample) >= DetectLines }

// Buffered is how many lines are waiting on the decision.
func (d *Detector) Buffered() int { return len(d.sample) }

// Decide commits to a format and hands back the buffered lines, for the caller
// to feed onward. A stream that goes quiet mid-sample may call it early.
func (d *Detector) Decide() (*Format, []string) {
	if !d.pinned {
		d.format = Detect(d.sample)
		d.pinned = true
	}
	sample := d.sample
	d.sample = nil
	return d.format, sample
}

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
