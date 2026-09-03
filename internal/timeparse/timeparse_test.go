package timeparse

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParse(t *testing.T, f *Format, line string) time.Time {
	t.Helper()
	tt, ok := f.Parse(line)
	require.True(t, ok, "line %q must parse", line)
	return tt
}

func TestClockMidnightRollover(t *testing.T) {
	f := &Format{kind: kindClock}
	t1 := mustParse(t, f, "23:30:00 packing")
	t2 := mustParse(t, f, "00:10:00 linking") // crossed midnight
	assert.Equal(t, 40*time.Minute, t2.Sub(t1))

	t3 := mustParse(t, f, "00:20:00 still linking") // offset persists
	assert.Equal(t, 50*time.Minute, t3.Sub(t1))

	t4 := mustParse(t, f, "23:00:00 long day") // same (new) day
	assert.Equal(t, 23*time.Hour+30*time.Minute, t4.Sub(t1))

	t5 := mustParse(t, f, "01:00:00 second midnight")
	assert.Equal(t, 2*time.Hour, t5.Sub(t4))
}

func TestClockBackwardsWithinTwoHoursNoRollover(t *testing.T) {
	f := &Format{kind: kindClock}
	t1 := mustParse(t, f, "15:00:00 a")
	t2 := mustParse(t, f, "14:30:00 b") // out-of-order, not a midnight
	assert.Equal(t, -30*time.Minute, t2.Sub(t1))
}

func genLines(n int, format string) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf(format, i)
	}
	return lines
}

func TestDetectPerFormat(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"iso", genLines(10, "2026-07-02T15:04:%02d starting step"), "iso8601"},
		{"golog", genLines(10, "2026/07/02 15:04:%02d step"), "golog"},
		{"syslog", genLines(10, "Jan  2 15:04:%02d host prog: msg"), "syslog"},
		{"epoch", genLines(10, "17199312%02d start"), "epoch"},
		{"dmesg", genLines(10, "[   12.3456%02d] usb event"), "dmesg"},
		{"clock", genLines(10, "15:04:%02d building"), "clock"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Detect(tc.lines)
			require.NotNil(t, f)
			assert.Equal(t, tc.want, f.Name())
		})
	}
}

func TestDetectThresholds(t *testing.T) {
	plain := func(n int) []string { return genLines(n, "compiling unit number %d of many") }

	t.Run("nil on empty input", func(t *testing.T) {
		assert.Nil(t, Detect(nil))
		assert.Nil(t, Detect([]string{}))
	})
	t.Run("nil on only empty lines", func(t *testing.T) {
		assert.Nil(t, Detect([]string{"", "   ", "\t"}))
	})
	t.Run("nil below five hits", func(t *testing.T) {
		assert.Nil(t, Detect(genLines(4, "2026-07-02T15:04:%02d x")))
	})
	t.Run("five hits at full rate detected", func(t *testing.T) {
		f := Detect(genLines(5, "2026-07-02T15:04:%02d x"))
		require.NotNil(t, f)
		assert.Equal(t, "iso8601", f.Name())
	})
	t.Run("nil below thirty percent", func(t *testing.T) {
		lines := append(genLines(5, "2026-07-02T15:04:%02d x"), plain(15)...)
		assert.Nil(t, Detect(lines)) // =
	})
	t.Run("thirty percent detected", func(t *testing.T) {
		lines := append(genLines(6, "2026-07-02T15:04:%02d x"), plain(14)...)
		f := Detect(lines) // =
		require.NotNil(t, f)
		assert.Equal(t, "iso8601", f.Name())
	})
	t.Run("empty lines not counted in rate", func(t *testing.T) {
		lines := append(genLines(6, "2026-07-02T15:04:%02d x"), "", "", "", "")
		f := Detect(lines)
		require.NotNil(t, f)
		assert.Equal(t, "iso8601", f.Name())
	})
	t.Run("only first 300 lines sampled", func(t *testing.T) {
		lines := append(plain(300), genLines(50, "2026-07-02T15:04:%02d x")...)
		assert.Nil(t, Detect(lines))
	})
	t.Run("most hits wins", func(t *testing.T) {
		lines := append(genLines(10, "2026-07-02T15:04:%02d x"), genLines(6, "15:04:%02d y")...)
		f := Detect(lines)
		require.NotNil(t, f)
		assert.Equal(t, "iso8601", f.Name())
	})
}

func TestDetectReturnsFreshState(t *testing.T) {
	// The returned Format must start with clean rollover state even though
	// detection itself exercised the probes.
	f := Detect(genLines(10, "15:04:%02d building"))
	require.NotNil(t, f)
	assert.True(t, f.last.IsZero())
	assert.Zero(t, f.dayOffset)
}
