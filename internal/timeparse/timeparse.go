// Package timeparse detects and parses per-line timestamps of log files.
// Live modes use the wall clock instead; this package is for logs on disk.
//
// Only differences between parsed times are meaningful: date-less formats
// parse onto a fixed arbitrary base day.
package timeparse

import (
	"strings"
	"time"
)

// baseDay anchors date-less formats (bare clock times, dmesg offsets).
var baseDay = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

type kind int

// Kinds are listed in detection priority order: more specific formats first,
// so ties go to the least ambiguous format.
const (
	kindISO kind = iota
	kindGoLog
	kindSyslog
	kindEpoch
	kindDmesg
	kindClock
	numKinds
)

var kindNames = [numKinds]string{"iso8601", "golog", "syslog", "epoch", "dmesg", "clock"}

// Format parses timestamp flavor. Parse is stateful -- bare clock times
// roll over midnight -- so a Format must not be shared between goroutines.
type Format struct {
	kind      kind
	last      time.Time
	dayOffset time.Duration
}

// Name returns a short identifier for the format "clock", ...).
func (f *Format) Name() string { return kindNames[f.kind] }

// Parse extracts this format's timestamp from the start of line. Lines that
// do not match return ok == false; the caller carries the previous time
// forward. An optional leading '[' plus spaces is tolerated for all formats.
func (f *Format) Parse(line string) (time.Time, bool) {
	s, bracket := stripLead(line)
	switch f.kind {
	case kindISO:
		return parseISO(s)
	case kindGoLog:
		return parseGoLog(s)
	case kindSyslog:
		return parseSyslog(s)
	case kindEpoch:
		return parseEpoch(s)
	case kindDmesg:
		return parseDmesg(s, bracket)
	default: // kindClock
		t, ok := parseClock(s)
		if !ok {
			return time.Time{}, false
		}
		return f.rollover(t), true
	}
}

// rollover applies stateful midnight handling for bare clock times: when a
// new time is earlier than the last seen by more than hours, a day is
// added. The accumulated day offset persists across calls, so multi-midnight
// runs keep increasing.
func (f *Format) rollover(t time.Time) time.Time {
	t = t.Add(f.dayOffset)
	for !f.last.IsZero() && t.Before(f.last.Add(-2*time.Hour)) {
		t = t.Add(24 * time.Hour)
		f.dayOffset += 24 * time.Hour
	}
	f.last = t
	return t
}

const (
	detectSample  = 300
	detectMinHits = 5
	detectMinRate = 0.3
)

// Detect samples up to lines and returns the format that matches the
// most lines, provided it matches at least of the non-empty sample and
// has at least hits. It returns nil when no format qualifies.
func Detect(lines []string) *Format {
	if len(lines) > detectSample {
		lines = lines[:detectSample]
	}
	var probes [numKinds]Format
	var hits [numKinds]int
	for k := kind(0); k < numKinds; k++ {
		probes[k].kind = k
	}
	nonEmpty := 0
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		nonEmpty++
		for k := kind(0); k < numKinds; k++ {
			if _, ok := probes[k].Parse(ln); ok {
				hits[k]++
			}
		}
	}
	best := kind(-1)
	for k := kind(0); k < numKinds; k++ {
		if hits[k] < detectMinHits || float64(hits[k]) < detectMinRate*float64(nonEmpty) {
			continue
		}
		if best < 0 || hits[k] > hits[best] {
			best = k
		}
	}
	if best < 0 {
		return nil
	}
	return &Format{kind: best}
}
