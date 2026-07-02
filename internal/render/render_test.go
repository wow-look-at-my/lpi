package render

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/log-progress-indicator/internal/progress"
)

// fullSnap mirrors the documented example snapshot.
func fullSnap() progress.Snapshot {
	return progress.Snapshot{
		Progress:      0.384,
		UnitsDone:     2451,
		UnitsTotal:    5948,
		UnitsPct:      2451.0 / 5948.0,
		HasTimes:      true,
		Elapsed:       134 * time.Second,
		ElapsedKnown:  true,
		RefDuration:   350 * time.Second,
		ETA:           215 * time.Second,
		ETAKind:       "pace",
		Pace:          1.07,
		MatchRate:     0.972,
		Confidence:    "high",
		CurrentLines:  2522,
		MatchedLines:  2451,
		NovelLines:    12,
		OverflowLines: 3,
	}
}

func TestBar(t *testing.T) {
	assert.Equal(t, "[>   ]", Bar(0, 4))
	assert.Equal(t, "[>   ]", Bar(-0.5, 4))
	assert.Equal(t, "[====]", Bar(1, 4))
	assert.Equal(t, "[====]", Bar(2.5, 4))
	assert.Equal(t, "[=====>    ]", Bar(0.5, 10))
	assert.Equal(t, "[===>]", Bar(0.999, 4))
	assert.Equal(t, "[]", Bar(0.5, 0))
	assert.Equal(t, "[]", Bar(0.5, -3))
}

func TestDuration(t *testing.T) {
	assert.Equal(t, "0s", Duration(0))
	assert.Equal(t, "0s", Duration(-5*time.Second))
	assert.Equal(t, "47s", Duration(47*time.Second))
	assert.Equal(t, "1m00s", Duration(59600*time.Millisecond)) // rounds up
	assert.Equal(t, "2m14s", Duration(134*time.Second))
	assert.Equal(t, "12m34s", Duration(12*time.Minute+34*time.Second))
	assert.Equal(t, "1h00m", Duration(3600*time.Second))
	assert.Equal(t, "1h02m", Duration(time.Hour+2*time.Minute+2*time.Second))
	assert.Equal(t, "2h59m", Duration(2*time.Hour+59*time.Minute))
}

func TestStatusLineFull(t *testing.T) {
	want := "[========>             ] 38.4%  units 2451/5948 (41.2%)  " +
		"elapsed 2m14s  eta ~3m35s  pace 1.07x  match 97%"
	assert.Equal(t, want, StatusLine(fullSnap()))
}

func TestStatusLineOmissions(t *testing.T) {
	s := progress.Snapshot{
		UnitsTotal: 5948,
		ETAKind:    "none",
		Confidence: "none",
	}
	want := "[>                     ] 0.0%  units 0/5948 (0.0%)  match 0%"
	assert.Equal(t, want, StatusLine(s))
}

func TestStatusLineRefPace(t *testing.T) {
	s := progress.Snapshot{
		Progress:   0.5,
		UnitsDone:  45,
		UnitsTotal: 90,
		UnitsPct:   0.5,
		HasTimes:   true,
		ETA:        175 * time.Second,
		ETAKind:    "ref-pace",
		MatchRate:  1,
	}
	want := "[===========>          ] 50.0%  units 45/90 (50.0%)  eta ~2m55s  match 100%"
	assert.Equal(t, want, StatusLine(s))
}

func TestSummaryFull(t *testing.T) {
	want := "Progress:    38.4% (time-weighted)\n" +
		"Units:       2451 / 5948 reference lines matched (41.2%)\n" +
		"Elapsed:     2m14s\n" +
		"ETA:         ~3m35s (pace 1.07x vs reference)\n" +
		"Confidence:  high (97.2% of lines matched; 12 novel, 3 overflow)\n" +
		"Reference:   5948 units over 5m50s\n"
	assert.Equal(t, want, Summary(fullSnap()))
}

func TestSummaryRefPaceAndUnknownElapsed(t *testing.T) {
	s := fullSnap()
	s.ElapsedKnown = false
	s.ETAKind = "ref-pace"
	s.ETA = 350 * time.Second
	s.Pace = 0
	want := "Progress:    38.4% (time-weighted)\n" +
		"Units:       2451 / 5948 reference lines matched (41.2%)\n" +
		"Elapsed:     unknown\n" +
		"ETA:         ~5m50s (assuming reference pace)\n" +
		"Confidence:  high (97.2% of lines matched; 12 novel, 3 overflow)\n" +
		"Reference:   5948 units over 5m50s\n"
	assert.Equal(t, want, Summary(s))
}

func TestSummaryNoETANoTimes(t *testing.T) {
	s := progress.Snapshot{
		Progress:   0.25,
		UnitsDone:  20,
		UnitsTotal: 80,
		UnitsPct:   0.25,
		ETAKind:    "none",
		MatchRate:  0.8,
		Confidence: "medium",
		NovelLines: 5,
	}
	want := "Progress:    25.0% (by line position)\n" +
		"Units:       20 / 80 reference lines matched (25.0%)\n" +
		"Elapsed:     unknown\n" +
		"Confidence:  medium (80.0% of lines matched; 5 novel, 0 overflow)\n" +
		"Reference:   80 units, no timing data\n"
	assert.Equal(t, want, Summary(s))
}

// forceTTY pins IsTTY to a fixed answer for the duration of the test.
func forceTTY(t *testing.T, tty bool) {
	t.Helper()
	old := IsTTY
	IsTTY = func(io.Writer) bool { return tty }
	t.Cleanup(func() { IsTTY = old })
}

func TestRendererTTYRepaintsInPlace(t *testing.T) {
	forceTTY(t, true)
	var buf bytes.Buffer
	r := New(&buf)
	s := fullSnap()
	r.Update(s)
	r.Update(s)
	r.Close(s)
	line := StatusLine(s)
	want := "\r\x1b[K" + line + "\r\x1b[K" + line + "\r\x1b[K" + line + "\n"
	assert.Equal(t, want, buf.String())
}

func TestRendererPlainThrottles(t *testing.T) {
	forceTTY(t, false)
	old := PlainInterval
	PlainInterval = time.Hour
	t.Cleanup(func() { PlainInterval = old })

	var buf bytes.Buffer
	r := New(&buf)
	s := fullSnap()
	r.Update(s) // first update always prints
	r.Update(s) // suppressed: same percent, interval not elapsed
	s2 := s
	s2.Progress = 0.41
	r.Update(s2) // whole percent changed: prints
	r.Close(s2)  // always prints
	want := StatusLine(s) + "\n" + StatusLine(s2) + "\n" + StatusLine(s2) + "\n"
	assert.Equal(t, want, buf.String())
}

func TestRendererPlainIntervalElapsed(t *testing.T) {
	forceTTY(t, false)
	old := PlainInterval
	PlainInterval = 0 // every update qualifies
	t.Cleanup(func() { PlainInterval = old })

	var buf bytes.Buffer
	r := New(&buf)
	s := fullSnap()
	r.Update(s)
	r.Update(s)
	assert.Equal(t, StatusLine(s)+"\n"+StatusLine(s)+"\n", buf.String())
}

func TestIsTTYDefault(t *testing.T) {
	assert.False(t, IsTTY(&bytes.Buffer{}))

	f, err := os.CreateTemp(t.TempDir(), "plain")
	require.NoError(t, err)
	defer f.Close()
	assert.False(t, IsTTY(f), "regular file is not a TTY")

	null, err := os.Open(os.DevNull)
	require.NoError(t, err)
	assert.True(t, IsTTY(null), "/dev/null is a character device")
	require.NoError(t, null.Close())
	assert.False(t, IsTTY(null), "Stat on a closed file fails")
}
