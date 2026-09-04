// Package timeparse detects and parses per-line
package timeparse

import (
	"strings"
	"time"
)

// baseDay anchors date-less formats (bare clock
var baseDay = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

type kind int

// Kinds are listed in detection priority order
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

// Format parses timestamp flavor
type Format struct {
	kind      kind
	custom    *custom
	last      time.Time
	dayOffset time.Duration
}

// Name returns a short identifier for the format
func (f *Format) Name() string {
	if f == nil {
		return "none"
	}
	if f.custom != nil {
		return f.custom.name
	}
	return kindNames[f.kind]
}

// Clone returns the same format with its rollover state reset, so one
// compiled format can read several logs.
func (f *Format) Clone() *Format {
	if f == nil {
		return nil
	}
	c := f.custom
	if c != nil {
		copied := *c
		c = &copied
	}
	return &Format{kind: f.kind, custom: c}
}

// Parse extracts this format's timestamp from the
func (f *Format) Parse(line string) (time.Time, bool) {
	if f.custom != nil {
		t, dated, ok := f.custom.parse(line)
		switch {
		case !ok:
			return time.Time{}, false
		case dated:
			return t, true
		}
		return f.rollover(t), true
	}
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

// rollover applies stateful midnight handling for
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

// Detect samples up to lines and returns the format
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
