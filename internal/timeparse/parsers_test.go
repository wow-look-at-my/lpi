package timeparse

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func date(y, mo, d, h, mi, sec, ns int) time.Time {
	return time.Date(y, time.Month(mo), d, h, mi, sec, ns, time.UTC)
}

type parseCase struct {
	name, in string
	want     time.Time
	ok       bool
}

func runParseCases(t *testing.T, k kind, cases []parseCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &Format{kind: k}
			got, ok := f.Parse(tc.in)
			require.Equal(t, tc.ok, ok)
			if ok {
				assert.True(t, got.Equal(tc.want), "got %v want %v", got, tc.want)
			}
		})
	}
}

func TestParseISO(t *testing.T) {
	runParseCases(t, kindISO, []parseCase{
		{"plain", "2026-07-02T15:04:05 starting", date(2026, 7, 2, 15, 4, 5, 0), true},
		{"frac dot z", "2026-07-02T15:04:05.123Z ok", date(2026, 7, 2, 15, 4, 5, 123000000), true},
		{"python comma space", "2026-07-02 15:04:05,123 INFO x", date(2026, 7, 2, 15, 4, 5, 123000000), true},
		{"long fraction", "2026-07-02T15:04:05.1234567899 x", date(2026, 7, 2, 15, 4, 5, 123456789), true},
		{"zone plus", "2026-07-02T15:04:05+02:00 x", date(2026, 7, 2, 13, 4, 5, 0), true},
		{"zone minus", "2026-07-02T15:04:05-01:30 x", date(2026, 7, 2, 16, 34, 5, 0), true},
		{"bracketed", "[2026-07-02T15:04:05] msg", date(2026, 7, 2, 15, 4, 5, 0), true},
		{"leading spaces", "   2026-07-02T15:04:05 x", date(2026, 7, 2, 15, 4, 5, 0), true},
		{"malformed zone ignored", "2026-07-02T15:04:05+xx msg", date(2026, 7, 2, 15, 4, 5, 0), true},
		{"bad month", "2026-13-02T15:04:05 x", time.Time{}, false},
		{"bad day", "2026-07-32T15:04:05 x", time.Time{}, false},
		{"bad separator", "2026-07-02X15:04:05 x", time.Time{}, false},
		{"missing seconds", "2026-07-02T15:04 x", time.Time{}, false},
		{"bad hour", "2026-07-02T25:04:05 x", time.Time{}, false},
		{"bad minute", "2026-07-02T15:61:05 x", time.Time{}, false},
		{"too short", "2026-07-02", time.Time{}, false},
		{"no timestamp", "just some log text", time.Time{}, false},
		{"empty", "", time.Time{}, false},
	})
}

func TestParseGoLog(t *testing.T) {
	runParseCases(t, kindGoLog, []parseCase{
		{"plain", "2026/07/02 15:04:05 starting worker", date(2026, 7, 2, 15, 4, 5, 0), true},
		{"frac", "2026/07/02 15:04:05.123456 x", date(2026, 7, 2, 15, 4, 5, 123456000), true},
		{"iso separators rejected", "2026-07-02 15:04:05 x", time.Time{}, false},
		{"missing space", "2026/07/02T15:04:05 x", time.Time{}, false},
		{"garbage", "worker 7 started", time.Time{}, false},
	})
}

func TestParseSyslog(t *testing.T) {
	runParseCases(t, kindSyslog, []parseCase{
		{"padded day", "Jan  2 15:04:05 host prog[123]: msg", date(2000, 1, 2, 15, 4, 5, 0), true},
		{"two digit day", "Dec 31 23:59:59 host x", date(2000, 12, 31, 23, 59, 59, 0), true},
		{"mid year", "Jul 15 08:30:00 y", date(2000, 7, 15, 8, 30, 0, 0), true},
		{"unknown month", "Foo 12 15:04:05 x", time.Time{}, false},
		{"day zero", "Jan  0 15:04:05 x", time.Time{}, false},
		{"day too big", "Jan 32 15:04:05 x", time.Time{}, false},
		{"missing time", "Jan  2 host msg", time.Time{}, false},
		{"short", "Jan", time.Time{}, false},
	})
}

func TestParseClock(t *testing.T) {
	runParseCases(t, kindClock, []parseCase{
		{"plain", "15:04:05 building", baseDay.Add(15*time.Hour + 4*time.Minute + 5*time.Second), true},
		{"bracketed", "[15:04:05] building", baseDay.Add(15*time.Hour + 4*time.Minute + 5*time.Second), true},
		{"single digit hour", "9:05:03 x", baseDay.Add(9*time.Hour + 5*time.Minute + 3*time.Second), true},
		{"fraction", "15:04:05.250 x", baseDay.Add(15*time.Hour + 4*time.Minute + 5*time.Second + 250*time.Millisecond), true},
		{"bad hour", "25:04:05 x", time.Time{}, false},
		{"not a time", "1234 things done", time.Time{}, false},
		{"date first", "2026-07-02T15:04:05 x", time.Time{}, false},
	})
}

func TestParseEpoch(t *testing.T) {
	runParseCases(t, kindEpoch, []parseCase{
		{"seconds", "1719931200 start", time.Unix(1719931200, 0).UTC(), true},
		{"seconds with frac", "1719931200.500 start", time.Unix(1719931200, 0).UTC(), true},
		{"millis", "1719931200123 start", time.UnixMilli(1719931200123).UTC(), true},
		{"below range", "1000000000 x", time.Time{}, false},
		{"above range", "2700000000 x", time.Time{}, false},
		{"millis out of range", "9999999999999 x", time.Time{}, false},
		{"nine digits", "171993120 x", time.Time{}, false},
		{"eleven digits", "17199312001 x", time.Time{}, false},
		{"fourteen digits", "17199312001234 x", time.Time{}, false},
		{"identifier prefix", "1719931200abc x", time.Time{}, false},
		{"not at start", "at 1719931200 x", time.Time{}, false},
	})
}

func TestParseDmesg(t *testing.T) {
	runParseCases(t, kindDmesg, []parseCase{
		{"boot", "[    0.000000] Linux version 6.1.0", baseDay, true},
		{"padded", "[   12.345678] usb 1-1: new device", baseDay.Add(12*time.Second + 345678*time.Microsecond), true},
		{"large", "[86400.500000] rotating", baseDay.Add(86400*time.Second + 500*time.Millisecond), true},
		{"space before bracket close", "[  1.5 ] x", baseDay.Add(1500 * time.Millisecond), true},
		{"no bracket", "12.345678] usb", time.Time{}, false},
		{"no close bracket", "[   12.345678 usb", time.Time{}, false},
		{"no fraction", "[   12] usb", time.Time{}, false},
		{"bare dot", "[   12.] usb", time.Time{}, false},
		{"clock inside bracket", "[15:04:05] x", time.Time{}, false},
	})
}

func TestParseTimeDifferences(t *testing.T) {
	iso := &Format{kind: kindISO}
	a, ok := iso.Parse("2026-07-02T10:00:00 begin")
	require.True(t, ok)
	b, ok := iso.Parse("2026-07-02T10:05:30 end")
	require.True(t, ok)
	assert.Equal(t, 5*time.Minute+30*time.Second, b.Sub(a))

	sys := &Format{kind: kindSyslog}
	c, ok := sys.Parse("Jan  2 10:00:00 x")
	require.True(t, ok)
	d, ok := sys.Parse("Jan 12 10:00:00 y")
	require.True(t, ok)
	assert.Equal(t, 240*time.Hour, d.Sub(c))

	dm := &Format{kind: kindDmesg}
	e, ok := dm.Parse("[   10.000000] a")
	require.True(t, ok)
	f, ok := dm.Parse("[  70.500000] b")
	require.True(t, ok)
	assert.Equal(t, 60*time.Second+500*time.Millisecond, f.Sub(e))
}
