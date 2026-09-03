package render

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/log-progress-indicator/internal/progress"
)

// fullSnap mirrors the documented example snapshot
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
	t.Serial()
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
	t.Serial()
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
	t.Serial()
	want := "[========>             ] 38.4%  units 2451/5948 (41.2%)  " +
		"elapsed 2m14s  eta ~3m35s  pace 1.07x  match 97%"
	assert.Equal(t, want, StatusLine(fullSnap()))
}

func TestStatusLineOmissions(t *testing.T) {
	t.Serial()
	s := progress.Snapshot{
		UnitsTotal: 5948,
		ETAKind:    "none",
		Confidence: "none",
	}
	want := "[>                     ] 0.0%  units 0/5948 (0.0%)  match 0%"
	assert.Equal(t, want, StatusLine(s))
}

func TestStatusLineRefPace(t *testing.T) {
	t.Serial()
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

// baselineSnap is what the estimator produces while
func baselineSnap() progress.Snapshot {
	return progress.Snapshot{
		Elapsed:      134 * time.Second,
		ElapsedKnown: true,
		ETAKind:      "none",
		Confidence:   "none",
		CurrentLines: 1234,
		NovelLines:   1234,
	}
}

func TestStatusLineRecordingBaseline(t *testing.T) {
	t.Serial()
	s := baselineSnap()
	assert.Equal(t, "recording baseline  lines 1234  elapsed 2m14s", StatusLine(s))

	s.ElapsedKnown = false
	assert.Equal(t, "recording baseline  lines 1234", StatusLine(s))
}

func TestStatusLineIdentifying(t *testing.T) {
	t.Serial()
	s := baselineSnap()
	s.Identifying = true
	assert.Equal(t, "identifying pattern  lines 1234  elapsed 2m14s", StatusLine(s))

	s.ElapsedKnown = false
	assert.Equal(t, "identifying pattern  lines 1234", StatusLine(s))
}

func TestStatusLineRefLabel(t *testing.T) {
	t.Serial()
	s := fullSnap()
	s.Label = "make -j8"
	want := "[========>             ] 38.4%  units 2451/5948 (41.2%)  " +
		"elapsed 2m14s  eta ~3m35s  pace 1.07x  match 97%  ref make -j8"
	assert.Equal(t, want, StatusLine(s))

	s.Label = "cmake --build build --parallel everything"
	assert.Contains(t, StatusLine(s), "ref cmake --build build --parall...",
		"labels longer than 28 bytes are truncated")
}

func TestSummaryPatternLabel(t *testing.T) {
	t.Serial()
	s := fullSnap()
	s.Label = "make -j8"
	want := "Progress:    38.4% (time-weighted)\n" +
		"Units:       2451 / 5948 reference lines matched (41.2%)\n" +
		"Elapsed:     2m14s\n" +
		"ETA:         ~3m35s (pace 1.07x vs reference)\n" +
		"Confidence:  high (97.2% of lines matched; 12 novel, 3 overflow)\n" +
		"Reference:   5948 units over 5m50s\n" +
		"Pattern:     make -j8\n"
	assert.Equal(t, want, Summary(s))
}

func TestSummaryRecordingBaseline(t *testing.T) {
	t.Serial()
	s := baselineSnap()
	want := "Reference:   none yet (recording baseline)\n" +
		"Lines:       1234\n" +
		"Elapsed:     2m14s\n"
	assert.Equal(t, want, Summary(s))

	s.ElapsedKnown = false
	want = "Reference:   none yet (recording baseline)\n" +
		"Lines:       1234\n" +
		"Elapsed:     unknown\n"
	assert.Equal(t, want, Summary(s))
}

func TestSummaryFull(t *testing.T) {
	t.Serial()
	want := "Progress:    38.4% (time-weighted)\n" +
		"Units:       2451 / 5948 reference lines matched (41.2%)\n" +
		"Elapsed:     2m14s\n" +
		"ETA:         ~3m35s (pace 1.07x vs reference)\n" +
		"Confidence:  high (97.2% of lines matched; 12 novel, 3 overflow)\n" +
		"Reference:   5948 units over 5m50s\n"
	assert.Equal(t, want, Summary(fullSnap()))
}

func TestSummaryRefPaceAndUnknownElapsed(t *testing.T) {
	t.Serial()
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
	t.Serial()
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

