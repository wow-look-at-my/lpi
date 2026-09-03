package progress

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/log-progress-indicator/internal/model"
)

var refBase = time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)

// steps returns n distinct log lines with no digits (digits would be
// normalized away and collapse the fingerprints).
func steps(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("building step %c of run", 'a'+rune(i))
	}
	return lines
}

// uniform returns offsets gap, ... for n lines.
func uniform(n int, gap time.Duration) []time.Duration {
	offs := make([]time.Duration, n)
	for i := range offs {
		offs[i] = time.Duration(i) * gap
	}
	return offs
}

func timedModel(t *testing.T, lines []string, offsets []time.Duration) *model.Model {
	t.Helper()
	require.Equal(t, len(lines), len(offsets))
	d := model.NewDigester("ref", nil)
	for i, ln := range lines {
		d.LineAt(ln, refBase.Add(offsets[i]))
	}
	run, err := d.Finish()
	require.NoError(t, err)
	m := model.New("test")
	m.AddRun(run)
	return m
}

func plainModel(t *testing.T, lines []string) *model.Model {
	t.Helper()
	d := model.NewDigester("ref", nil)
	for _, ln := range lines {
		d.Line(ln)
	}
	run, err := d.Finish()
	require.NoError(t, err)
	m := model.New("test")
	m.AddRun(run)
	return m
}

func TestInOrderReplayIsMonotonicAndComplete(t *testing.T) {
	lines := steps(11)
	e := NewEstimator(timedModel(t, lines, uniform(11, 10*time.Second)))
	prev := 0.0
	for _, ln := range lines {
		e.Observe(ln, time.Time{})
		s := e.Snapshot()
		assert.GreaterOrEqual(t, s.Progress, prev, "progress must not decrease")
		prev = s.Progress
	}
	s := e.Snapshot()
	assert.InDelta(t, 1.0, s.Progress, 1e-3)
	assert.Equal(t, s.UnitsTotal, s.UnitsDone)
	assert.Equal(t, 11, s.UnitsTotal)
	assert.Equal(t, 11, s.MatchedLines)
	assert.Zero(t, s.NovelLines)
	assert.Zero(t, s.OverflowLines)
	assert.InDelta(t, 1.0, s.UnitsPct, 1e-9)
}

func TestOutOfOrderReplayReachesSameFinalState(t *testing.T) {
	lines := steps(11)
	m := timedModel(t, lines, uniform(11, 10*time.Second))

	inOrder := NewEstimator(m)
	for _, ln := range lines {
		inOrder.Observe(ln, time.Time{})
	}
	reversed := NewEstimator(m)
	for i := len(lines) - 1; i >= 0; i-- {
		reversed.Observe(lines[i], time.Time{})
	}

	a, b := inOrder.Snapshot(), reversed.Snapshot()
	assert.InDelta(t, a.Progress, b.Progress, 1e-9)
	assert.Equal(t, a.UnitsDone, b.UnitsDone)
	assert.Equal(t, a.MatchedLines, b.MatchedLines)
	assert.InDelta(t, 1.0, b.Progress, 1e-3)
}

func TestOutOfOrderWithDuplicateLines(t *testing.T) {
	lines := []string{"task setup", "work unit", "work unit", "task teardown"}
	offsets := []time.Duration{0, 10 * time.Second, 20 * time.Second, 100 * time.Second}
	m := timedModel(t, lines, offsets)

	e := NewEstimator(m)
	for _, ln := range []string{"task teardown", "work unit", "task setup", "work unit"} {
		e.Observe(ln, time.Time{})
	}
	s := e.Snapshot()
	assert.InDelta(t, 1.0, s.Progress, 1e-3)
	assert.Equal(t, 4, s.MatchedLines)
	assert.Zero(t, s.OverflowLines)
}

func TestPrefixReplayIsProportional(t *testing.T) {
	lines := steps(11)
	e := NewEstimator(timedModel(t, lines, uniform(11, 10*time.Second)))
	for _, ln := range lines[:6] {
		e.Observe(ln, time.Time{})
	}
	s := e.Snapshot()
	assert.InDelta(t, 0.5, s.Progress, 0.01, "6 of 11 uniform lines cover half the gaps")
	assert.InDelta(t, 6.0/11, s.UnitsPct, 1e-9)
}

func TestSilentGapCreditedOnlyWhenLineAppears(t *testing.T) {
	lines := []string{"start job", "before the silence", "after the silence"}
	offsets := []time.Duration{0, 50 * time.Second, 100 * time.Second}
	e := NewEstimator(timedModel(t, lines, offsets))

	e.Observe("start job", time.Time{})
	assert.InDelta(t, 0.0, e.Snapshot().Progress, 1e-6)

	e.Observe("before the silence", time.Time{})
	assert.InDelta(t, 0.5, e.Snapshot().Progress, 1e-6)

	e.Observe("after the silence", time.Time{})
	assert.InDelta(t, 1.0, e.Snapshot().Progress, 1e-6)
}

func TestNovelAndOverflowCounting(t *testing.T) {
	e := NewEstimator(plainModel(t, []string{"alpha line", "beta line"}))

	e.Observe("totally unknown zebra", time.Time{})
	e.Observe("alpha line", time.Time{})
	e.Observe("alpha line", time.Time{}) // reference has only occurrence
	e.Observe("", time.Time{})           // skipped entirely
	e.Observe("   ", time.Time{})        // skipped entirely

	s := e.Snapshot()
	assert.Equal(t, 3, s.CurrentLines)
	assert.Equal(t, 1, s.MatchedLines)
	assert.Equal(t, 1, s.NovelLines)
	assert.Equal(t, 1, s.OverflowLines)
	assert.Equal(t, 1, s.UnitsDone)
	assert.Equal(t, 2, s.UnitsTotal)
}

func TestOverflowAfterFullReplayKeepsProgressAtOne(t *testing.T) {
	lines := steps(6)
	e := NewEstimator(timedModel(t, lines, uniform(6, 10*time.Second)))
	for _, ln := range lines {
		e.Observe(ln, time.Time{})
	}
	e.Observe(lines[0], time.Time{})
	e.Observe(lines[1], time.Time{})
	s := e.Snapshot()
	assert.InDelta(t, 1.0, s.Progress, 1e-3)
	assert.Equal(t, 2, s.OverflowLines)
	assert.Equal(t, 6, s.UnitsDone)
}

func TestNoTimesModelProgressTracksUnits(t *testing.T) {
	lines := steps(11)
	e := NewEstimator(plainModel(t, lines))
	for _, ln := range lines[:6] {
		e.Observe(ln, time.Time{})
	}
	s := e.Snapshot()
	assert.False(t, s.HasTimes)
	assert.Zero(t, s.RefDuration)
	assert.InDelta(t, 0.5, s.Progress, 1e-6, "5 of 10 equal position weights")
	assert.InDelta(t, 6.0/11, s.UnitsPct, 1e-9)
	assert.InDelta(t, float64(s.UnitsPct), s.Progress, 0.06, "progress approximates units share")

	for _, ln := range lines[6:] {
		e.Observe(ln, time.Time{})
	}
	s = e.Snapshot()
	assert.InDelta(t, 1.0, s.Progress, 1e-3)
	assert.InDelta(t, 1.0, s.UnitsPct, 1e-9)
}

func TestEmptyModelEverythingNovel(t *testing.T) {
	e := NewEstimator(model.New("empty"))
	e.Observe("some line here", time.Time{})
	s := e.Snapshot()
	assert.Equal(t, 1, s.NovelLines)
	assert.Zero(t, s.UnitsTotal)
	assert.Zero(t, s.UnitsPct)
	assert.Zero(t, s.Progress)
	assert.Equal(t, "none", s.ETAKind)
	assert.Equal(t, "none", s.Confidence, "an empty model has nothing to be confident about")
}

// TestEmptyModelSnapshotStaysFinite pins the baseline-recording state: an
// estimator over an empty model (as used by the live-learning bootstrap)
// must keep every field sane and never emit NaN or Inf, even with a live
// clock running.
func TestEmptyModelSnapshotStaysFinite(t *testing.T) {
	e := NewEstimator(model.New("empty"))
	e.Observe("first baseline line", refBase)
	e.Observe("second baseline line", refBase.Add(3*time.Second))
	e.Tick(refBase.Add(10 * time.Second))

	s := e.Snapshot()
	assert.Zero(t, s.Progress)
	assert.Zero(t, s.UnitsDone)
	assert.Zero(t, s.UnitsTotal)
	assert.Zero(t, s.UnitsPct)
	assert.Zero(t, s.MatchRate)
	assert.Zero(t, s.Pace)
	assert.Zero(t, s.ETA)
	assert.Equal(t, "none", s.ETAKind)
	assert.Equal(t, "none", s.Confidence)
	require.True(t, s.ElapsedKnown)
	assert.Equal(t, 10*time.Second, s.Elapsed)
	assert.Equal(t, 2, s.CurrentLines)
	assert.Equal(t, 2, s.NovelLines)
	for name, v := range map[string]float64{
		"Progress": s.Progress, "UnitsPct": s.UnitsPct,
		"MatchRate": s.MatchRate, "Pace": s.Pace,
	} {
		assert.False(t, math.IsNaN(v), "%s must not be NaN", name)
		assert.False(t, math.IsInf(v, 0), "%s must not be Inf", name)
	}
}
