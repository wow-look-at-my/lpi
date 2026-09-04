package timeparse

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Named groups a --format regex may carry. A "time" group hands its text to
// the builtin parsers (or to a layout); the rest build the stamp piecewise.
const (
	grpTime    = "time"
	grpEpoch   = "epoch"
	grpEpochMs = "epochms"
	grpEpochNs = "epochns"
	grpYear    = "year"
	grpMonth   = "month"
	grpDay     = "day"
	grpHour    = "hour"
	grpMin     = "min"
	grpSec     = "sec"
	grpFrac    = "frac"
	grpZone    = "zone"
)

var knownGroups = []string{
	grpTime, grpEpoch, grpEpochMs, grpEpochNs,
	grpYear, grpMonth, grpDay, grpHour, grpMin, grpSec, grpFrac, grpZone,
}

// Names lists the builtin format names a spec may select.
func Names() []string { return append([]string(nil), kindNames[:]...) }

// Groups lists the named capture groups a --format regex may carry.
func Groups() []string { return append([]string(nil), knownGroups...) }

// custom is a user-specified timestamp reader: a regex, a Go layout, or both.
type custom struct {
	name   string
	re     *regexp.Regexp
	layout string
	idx    map[string]int
	probe  kind
	probed bool
}

// Compile builds a Format from a user spec and an optional Go layout. An
// empty spec with no layout asks for detection, reported as a nil Format.
func Compile(spec, layout string) (*Format, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.EqualFold(spec, "auto") {
		if layout == "" {
			return nil, nil
		}
		return &Format{custom: &custom{name: "layout", layout: layout}}, nil
	}
	if k, ok := builtinKind(spec); ok {
		if layout != "" {
			return nil, errors.New("--time-layout does not apply to the builtin format " + spec)
		}
		return &Format{kind: k}, nil
	}
	expr := strings.TrimPrefix(spec, "regex:")
	if expr == spec && !strings.Contains(spec, "(?P<") {
		return nil, fmt.Errorf("unknown format %q: use auto, a builtin (%s), or a regex with named groups (%s)",
			spec, strings.Join(kindNames[:], ", "), strings.Join(knownGroups, ", "))
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("--format regex: %w", err)
	}
	idx, err := groupIndex(re)
	if err != nil {
		return nil, err
	}
	return &Format{custom: &custom{name: "custom", re: re, layout: layout, idx: idx}}, nil
}

func builtinKind(name string) (kind, bool) {
	for k, n := range kindNames {
		if strings.EqualFold(name, n) {
			return kind(k), true
		}
	}
	return 0, false
}

// groupIndex maps the regex's known named groups to their submatch slots and
// rejects a regex that names none of them.
func groupIndex(re *regexp.Regexp) (map[string]int, error) {
	idx := make(map[string]int)
	for i, name := range re.SubexpNames() {
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		for _, known := range knownGroups {
			if lower == known {
				idx[known] = i
			}
		}
	}
	if len(idx) == 0 {
		return nil, errors.New("--format regex names no timestamp group; use one of: " +
			strings.Join(knownGroups, ", "))
	}
	return idx, nil
}

// group returns the text of a named group, empty when it did not take part.
func (c *custom) group(m []string, name string) string {
	i, ok := c.idx[name]
	if !ok || i >= len(m) {
		return ""
	}
	return m[i]
}

// parse reads the line's stamp. dated is false for a clock-only stamp, which
// the caller then carries through midnight itself.
func (c *custom) parse(line string) (t time.Time, dated, ok bool) {
	if c.re == nil {
		return c.parseLayoutPrefix(line)
	}
	m := c.re.FindStringSubmatch(line)
	if m == nil {
		return time.Time{}, false, false
	}
	if text := c.group(m, grpTime); text != "" {
		return c.parseText(text)
	}
	return c.fromGroups(m)
}

// parseLayoutPrefix parses a leading window of the line with the layout,
// widening the window because a layout's rendered width is not its parsed one.
func (c *custom) parseLayoutPrefix(line string) (time.Time, bool, bool) {
	base := len(c.layout)
	for n := base - 3; n <= base+6; n++ {
		if n <= 0 || n > len(line) {
			continue
		}
		if t, err := time.Parse(c.layout, line[:n]); err == nil {
			return t, dated(t), true
		}
	}
	return time.Time{}, false, false
}

// parseText reads one extracted stamp: with the layout when given, else with
// the builtin parser that first recognizes it, remembered for later lines.
func (c *custom) parseText(text string) (time.Time, bool, bool) {
	text = strings.TrimSpace(text)
	if c.layout != "" {
		t, err := time.Parse(c.layout, text)
		if err != nil {
			return time.Time{}, false, false
		}
		return t, dated(t), true
	}
	if c.probed {
		return probeKind(c.probe, text)
	}
	for k := kind(0); k < numKinds; k++ {
		t, d, ok := probeKind(k, text)
		if !ok {
			continue
		}
		c.probe, c.probed = k, true
		return t, d, true
	}
	return time.Time{}, false, false
}

// probeKind runs one builtin parser over an extracted stamp.
func probeKind(k kind, text string) (time.Time, bool, bool) {
	f := Format{kind: k}
	t, ok := f.Parse(text)
	if !ok {
		return time.Time{}, false, false
	}
	return t, k != kindClock && k != kindDmesg, true
}

// dated reports whether a parsed stamp carries a real date rather than the
// zero year a layout leaves behind.
func dated(t time.Time) bool { return t.Year() > 1 }

// fromGroups assembles a stamp from component groups.
func (c *custom) fromGroups(m []string) (time.Time, bool, bool) {
	if t, ok := c.fromEpoch(m); ok {
		return t, true, true
	}
	y, mo, d := baseDay.Year(), baseDay.Month(), baseDay.Day()
	hasDate := false
	if v, ok := atoiGroup(c.group(m, grpYear)); ok {
		if v < 100 {
			v += 2000
		}
		y, hasDate = v, true
	}
	if mv, ok := parseMonth(c.group(m, grpMonth)); ok {
		mo, hasDate = mv, true
	}
	if v, ok := atoiGroup(c.group(m, grpDay)); ok {
		d, hasDate = v, true
	}
	h, _ := atoiGroup(c.group(m, grpHour))
	mi, _ := atoiGroup(c.group(m, grpMin))
	sec, _ := atoiGroup(c.group(m, grpSec))
	if h > 23 || mi > 59 || sec > 60 || mo < time.January || mo > time.December || d < 1 || d > 31 {
		return time.Time{}, false, false
	}
	ns := parseFrac(c.group(m, grpFrac))
	loc := parseZoneText(c.group(m, grpZone))
	return time.Date(y, mo, d, h, mi, sec, ns, loc), hasDate, true
}

// fromEpoch handles the epoch groups, which carry a whole stamp on their own.
func (c *custom) fromEpoch(m []string) (time.Time, bool) {
	if s := c.group(m, grpEpochNs); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		return time.Unix(0, v).UTC(), err == nil
	}
	if s := c.group(m, grpEpochMs); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		return time.UnixMilli(v).UTC(), err == nil
	}
	s := c.group(m, grpEpoch)
	if s == "" {
		return time.Time{}, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return time.Time{}, false
	}
	whole := int64(v)
	return time.Unix(whole, int64((v-float64(whole))*1e9)).UTC(), true
}

func atoiGroup(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

var longMonths = map[string]time.Month{
	"january": time.January, "february": time.February, "march": time.March,
	"april": time.April, "may": time.May, "june": time.June, "july": time.July,
	"august": time.August, "september": time.September, "october": time.October,
	"november": time.November, "december": time.December,
}

// parseMonth accepts a number, an abbreviation, or a full month name.
func parseMonth(s string) (time.Month, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if v, ok := atoiGroup(s); ok {
		if v < 1 || v > 12 {
			return 0, false
		}
		return time.Month(v), true
	}
	if mo, ok := longMonths[strings.ToLower(s)]; ok {
		return mo, true
	}
	if len(s) >= 3 {
		if mo, ok := syslogMonths[strings.ToUpper(s[:1])+strings.ToLower(s[1:3])]; ok {
			return mo, true
		}
	}
	return 0, false
}

// parseFrac reads fractional seconds, with or without their leading point.
func parseFrac(s string) int {
	s = strings.TrimLeft(strings.TrimSpace(s), ".,")
	if s == "" {
		return 0
	}
	ns, _ := frac("."+s, 0)
	return ns
}

// parseZoneText reads Z, a numeric offset, or a name Go knows.
func parseZoneText(s string) *time.Location {
	s = strings.TrimSpace(s)
	switch {
	case s == "" || s == "Z" || s == "z":
		return time.UTC
	case s[0] == '+' || s[0] == '-':
		return offsetZone(s)
	}
	if loc, err := time.LoadLocation(s); err == nil {
		return loc
	}
	return time.UTC
}

// offsetZone reads +hh:mm, +hhmm, or +hh.
func offsetZone(s string) *time.Location {
	digits := strings.ReplaceAll(s[1:], ":", "")
	if len(digits) == 2 {
		digits += "00"
	}
	if len(digits) != 4 {
		return time.UTC
	}
	h, ok1 := fixed(digits, 0, 2)
	m, ok2 := fixed(digits, 2, 2)
	if !ok1 || !ok2 {
		return time.UTC
	}
	off := (h*60 + m) * 60
	if s[0] == '-' {
		off = -off
	}
	return time.FixedZone("", off)
}
