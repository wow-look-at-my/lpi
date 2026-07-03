package timeparse

import "time"

// stripLead skips leading spaces/tabs and one optional '[' (plus any spaces
// after it), reporting whether a bracket was consumed.
func stripLead(s string) (string, bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	bracket := false
	if i < len(s) && s[i] == '[' {
		bracket = true
		i++
		for i < len(s) && s[i] == ' ' {
			i++
		}
	}
	return s[i:], bracket
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || isDigit(c)
}

// fixed parses exactly n digits at s[i:].
func fixed(s string, i, n int) (int, bool) {
	if i+n > len(s) {
		return 0, false
	}
	v := 0
	for k := i; k < i+n; k++ {
		if !isDigit(s[k]) {
			return 0, false
		}
		v = v*10 + int(s[k]-'0')
	}
	return v, true
}

// frac parses an optional fractional-seconds suffix ('.' or ',' followed by
// digits) at s[i:], returning nanoseconds and the index just past it. Without
// a fraction it returns (0, i).
func frac(s string, i int) (int, int) {
	if i >= len(s) || (s[i] != '.' && s[i] != ',') {
		return 0, i
	}
	j := i + 1
	if j >= len(s) || !isDigit(s[j]) {
		return 0, i // a bare '.' is not a fraction
	}
	ns, scale := 0, 100000000
	for j < len(s) && isDigit(s[j]) {
		ns += int(s[j]-'0') * scale
		scale /= 10
		if scale == 0 { // more than 9 digits: ignore the rest
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			return ns, j
		}
		j++
	}
	return ns, j
}

// hms parses hh:mm:ss with an optional fraction at s[i:]. With flexHour the
// hour may be a single digit. It returns the components and the index just
// past what was consumed.
func hms(s string, i int, flexHour bool) (h, m, sec, ns, end int, ok bool) {
	h = -1
	if v, vok := fixed(s, i, 2); vok && i+2 < len(s) && s[i+2] == ':' {
		h = v
		i += 3
	} else if flexHour {
		if v, vok := fixed(s, i, 1); vok && i+1 < len(s) && s[i+1] == ':' {
			h = v
			i += 2
		}
	}
	if h < 0 {
		return 0, 0, 0, 0, 0, false
	}
	m, mok := fixed(s, i, 2)
	if !mok || i+2 >= len(s) || s[i+2] != ':' {
		return 0, 0, 0, 0, 0, false
	}
	i += 3
	sec, sok := fixed(s, i, 2)
	if !sok {
		return 0, 0, 0, 0, 0, false
	}
	i += 2
	ns, i = frac(s, i)
	if h > 23 || m > 59 || sec > 60 {
		return 0, 0, 0, 0, 0, false
	}
	return h, m, sec, ns, i, true
}

// parseYMD parses yyyy<sep>mm<sep>dd at the start of s.
func parseYMD(s string, sep byte) (int, time.Month, int, bool) {
	if len(s) < 10 || s[4] != sep || s[7] != sep {
		return 0, 0, 0, false
	}
	y, ok1 := fixed(s, 0, 4)
	mo, ok2 := fixed(s, 5, 2)
	d, ok3 := fixed(s, 8, 2)
	if !ok1 || !ok2 || !ok3 || mo < 1 || mo > 12 || d < 1 || d > 31 {
		return 0, 0, 0, false
	}
	return y, time.Month(mo), d, true
}

// parseZone parses an optional trailing zone ('Z' or +hh:mm / -hh:mm) at
// s[i:]. Malformed or absent zones fall back to UTC.
func parseZone(s string, i int) *time.Location {
	if i >= len(s) || (s[i] != '+' && s[i] != '-') {
		return time.UTC
	}
	oh, ok1 := fixed(s, i+1, 2)
	if !ok1 || i+3 >= len(s) || s[i+3] != ':' {
		return time.UTC
	}
	om, ok2 := fixed(s, i+4, 2)
	if !ok2 {
		return time.UTC
	}
	off := (oh*60 + om) * 60
	if s[i] == '-' {
		off = -off
	}
	return time.FixedZone("", off)
}

// parseISO handles ISO-8601 / RFC3339: 2026-07-02T15:04:05 with optional
// .frac/,frac and optional Z/+hh:mm/-hh:mm; a space may stand in for 'T'
// (python's "2026-07-02 15:04:05,123").
func parseISO(s string) (time.Time, bool) {
	y, mo, d, ok := parseYMD(s, '-')
	if !ok || len(s) < 12 || (s[10] != 'T' && s[10] != ' ') {
		return time.Time{}, false
	}
	h, mi, sec, ns, end, ok := hms(s, 11, false)
	if !ok {
		return time.Time{}, false
	}
	return time.Date(y, mo, d, h, mi, sec, ns, parseZone(s, end)), true
}

// parseGoLog handles the Go log default: 2026/07/02 15:04:05 (optional frac).
func parseGoLog(s string) (time.Time, bool) {
	y, mo, d, ok := parseYMD(s, '/')
	if !ok || len(s) < 12 || s[10] != ' ' {
		return time.Time{}, false
	}
	h, mi, sec, ns, _, ok := hms(s, 11, false)
	if !ok {
		return time.Time{}, false
	}
	return time.Date(y, mo, d, h, mi, sec, ns, time.UTC), true
}

var syslogMonths = map[string]time.Month{
	"Jan": time.January, "Feb": time.February, "Mar": time.March,
	"Apr": time.April, "May": time.May, "Jun": time.June,
	"Jul": time.July, "Aug": time.August, "Sep": time.September,
	"Oct": time.October, "Nov": time.November, "Dec": time.December,
}

// parseSyslog handles the classic syslog stamp: "Jan  2 15:04:05". There is
// no year; the base year 2000 is used (only differences matter).
func parseSyslog(s string) (time.Time, bool) {
	if len(s) < 4 || s[3] != ' ' {
		return time.Time{}, false
	}
	mo, ok := syslogMonths[s[:3]]
	if !ok {
		return time.Time{}, false
	}
	i := 4
	for i < len(s) && s[i] == ' ' {
		i++
	}
	d, nd := 0, 0
	for i < len(s) && isDigit(s[i]) && nd < 2 {
		d = d*10 + int(s[i]-'0')
		i++
		nd++
	}
	if nd == 0 || d < 1 || d > 31 || i >= len(s) || s[i] != ' ' {
		return time.Time{}, false
	}
	for i < len(s) && s[i] == ' ' {
		i++
	}
	h, mi, sec, ns, _, ok := hms(s, i, false)
	if !ok {
		return time.Time{}, false
	}
	return time.Date(2000, mo, d, h, mi, sec, ns, time.UTC), true
}

// parseClock handles bare times: 15:04:05 with optional fraction. The hour
// may be a single digit. Midnight rollover is applied by the caller.
func parseClock(s string) (time.Time, bool) {
	h, mi, sec, ns, _, ok := hms(s, 0, true)
	if !ok {
		return time.Time{}, false
	}
	return baseDay.Add(time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute +
		time.Duration(sec)*time.Second + time.Duration(ns)), true
}

const (
	epochMinSec = 1_400_000_000 // ~2014
	epochMaxSec = 2_600_000_000 // ~2052
)

// parseEpoch handles a Unix epoch at line start: exactly 10 digits in the
// plausible seconds range, or exactly 13 digits whose seconds value falls in
// the same range (milliseconds).
func parseEpoch(s string) (time.Time, bool) {
	n := 0
	var v int64
	for n < len(s) && isDigit(s[n]) {
		if n < 14 { // enough for 13 digits; longer runs are rejected below
			v = v*10 + int64(s[n]-'0')
		}
		n++
	}
	if n < len(s) && isWordByte(s[n]) {
		return time.Time{}, false // part of an identifier, not a number
	}
	switch n {
	case 10:
		if v < epochMinSec || v > epochMaxSec {
			return time.Time{}, false
		}
		return time.Unix(v, 0).UTC(), true
	case 13:
		if v/1000 < epochMinSec || v/1000 > epochMaxSec {
			return time.Time{}, false
		}
		return time.UnixMilli(v).UTC(), true
	}
	return time.Time{}, false
}

// parseDmesg handles dmesg-style relative stamps: "[   12.345678]". The
// leading bracket (already consumed by stripLead) is required.
func parseDmesg(s string, bracket bool) (time.Time, bool) {
	if !bracket {
		return time.Time{}, false
	}
	i := 0
	var sec int64
	nd := 0
	for i < len(s) && isDigit(s[i]) && nd < 12 {
		sec = sec*10 + int64(s[i]-'0')
		i++
		nd++
	}
	if nd == 0 || i >= len(s) || s[i] != '.' {
		return time.Time{}, false
	}
	ns, j := frac(s, i)
	if j == i {
		return time.Time{}, false // '.' without digits
	}
	i = j
	for i < len(s) && s[i] == ' ' {
		i++
	}
	if i >= len(s) || s[i] != ']' {
		return time.Time{}, false
	}
	return baseDay.Add(time.Duration(sec)*time.Second + time.Duration(ns)), true
}
