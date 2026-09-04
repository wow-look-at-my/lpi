package progress

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var wallBase = time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)

// paceModel: lines, apart, reference duration every
func paceEstimator(t *testing.T) *Estimator {
	t.Helper()
	return NewEstimator(timedModel(t, steps(12), uniform(12, 10*time.Second)))
}

func TestETAPaceMath(t *testing.T) {
	e := paceEstimator(t)
	lines := steps(12)
	// Replay lines at wall intervals: the reference
	for i, ln := range lines[:6] {
		e.Observe(ln, wallBase.Add(time.Duration(i)*20*time.Second))
	}
	s := e.Snapshot()
	require.True(t, s.ElapsedKnown)
	assert.Equal(t, 100*time.Second, s.Elapsed)
	assert.Equal(t, 110*time.Second, s.RefDuration)
	assert.InDelta(t, 5.0/11, s.Progress, 1e-4)

	require.Equal(t, "pace", s.ETAKind)
	assert.InDelta(t, 2.0, s.Pace, 1e-3, "running at half the reference speed")
	// Remaining of a reference at speed
	assert.InDelta(t, 120.0, s.ETA.Seconds(), 0.1)
}

func TestETAPaceAtCompletionIsZero(t *testing.T) {
	e := paceEstimator(t)
	for i, ln := range steps(12) {
		e.Observe(ln, wallBase.Add(time.Duration(i)*10*time.Second))
	}
	s := e.Snapshot()
	require.Equal(t, "pace", s.ETAKind)
	assert.InDelta(t, 0.0, s.ETA.Seconds(), 0.1)
	assert.InDelta(t, 1.0, s.Pace, 1e-3, "same speed as the reference")
}

func TestETARefPaceFallbackWithoutElapsed(t *testing.T) {
	e := paceEstimator(t)
	for _, ln := range steps(12)[:6] {
		e.Observe(ln, time.Time{}) // no wall clock available
	}
	s := e.Snapshot()
	require.False(t, s.ElapsedKnown)
	require.Equal(t, "ref-pace", s.ETAKind)
	assert.Zero(t, s.Pace)
	// Remaining of the reference at face value
	assert.InDelta(t, 60.0, s.ETA.Seconds(), 0.1)
}

func TestETANoneCases(t *testing.T) {
	t.Run("fresh estimator", func(t *testing.T) {
		s := paceEstimator(t).Snapshot()
		assert.Equal(t, "none", s.ETAKind)
		assert.Zero(t, s.ETA)
		assert.Zero(t, s.Pace)
	})
	t.Run("elapsed known but too few matches", func(t *testing.T) {
		e := paceEstimator(t)
		lines := steps(12)
		for i, ln := range lines[:3] { // progress but only matches
			e.Observe(ln, wallBase.Add(time.Duration(i)*10*time.Second))
		}
		s := e.Snapshot()
		require.True(t, s.ElapsedKnown)
		assert.GreaterOrEqual(t, s.Progress, 0.02)
		assert.Equal(t, "none", s.ETAKind, "the ref-pace fallback requires elapsed to be unknown")
	})
	t.Run("no reference duration", func(t *testing.T) {
		e := NewEstimator(plainModel(t, steps(11)))
		for i, ln := range steps(11)[:6] {
			e.Observe(ln, wallBase.Add(time.Duration(i)*10*time.Second))
		}
		s := e.Snapshot()
		assert.Equal(t, "none", s.ETAKind)
	})
	t.Run("progress below ref-pace floor", func(t *testing.T) {
		e := paceEstimator(t)
		e.Observe(steps(12)[0], time.Time{}) // line owns no weight
		s := e.Snapshot()
		assert.Zero(t, s.Progress)
		assert.Equal(t, "none", s.ETAKind)
	})
}

func TestConfidenceTiers(t *testing.T) {
	lines := steps(10)
	m := plainModel(t, lines)

	t.Run("none before any line", func(t *testing.T) {
		s := NewEstimator(m).Snapshot()
		assert.Equal(t, "none", s.Confidence)
		assert.Zero(t, s.MatchRate)
	})
	t.Run("high at ninety percent", func(t *testing.T) {
		e := NewEstimator(m)
		for _, ln := range lines[:9] {
			e.Observe(ln, time.Time{})
		}
		e.Observe("unexpected chatter", time.Time{})
		s := e.Snapshot()
		assert.InDelta(t, 0.9, s.MatchRate, 1e-9)
		assert.Equal(t, "high", s.Confidence)
	})
	t.Run("medium at sixty percent", func(t *testing.T) {
		e := NewEstimator(m)
		for _, ln := range lines[:6] {
			e.Observe(ln, time.Time{})
		}
		for _, ln := range []string{"noise w", "noise x", "noise y", "noise z"} {
			e.Observe(ln, time.Time{})
		}
		s := e.Snapshot()
		assert.InDelta(t, 0.6, s.MatchRate, 1e-9)
		assert.Equal(t, "medium", s.Confidence)
	})
	t.Run("low below sixty percent", func(t *testing.T) {
		e := NewEstimator(m)
		e.Observe(lines[0], time.Time{})
		e.Observe("some unknown noise", time.Time{})
		s := e.Snapshot()
		assert.InDelta(t, 0.5, s.MatchRate, 1e-9)
		assert.Equal(t, "low", s.Confidence)
	})
}

func TestTickDrivenElapsed(t *testing.T) {
	e := paceEstimator(t)

	e.Tick(wallBase) // before any observation: lastAt only
	s := e.Snapshot()
	assert.False(t, s.ElapsedKnown, "Tick alone never starts the clock")

	e.Observe(steps(12)[0], wallBase.Add(10*time.Second))
	s = e.Snapshot()
	require.True(t, s.ElapsedKnown)
	assert.Zero(t, s.Elapsed)

	e.Tick(wallBase.Add(30 * time.Second)) // silence, clock advances
	s = e.Snapshot()
	assert.Equal(t, 20*time.Second, s.Elapsed)

	e.Tick(time.Time{})                   // time is a no-op
	e.Tick(wallBase.Add(5 * time.Second)) // stale time never rewinds
	e.Observe(steps(12)[1], wallBase.Add(15*time.Second))
	s = e.Snapshot()
	assert.Equal(t, 20*time.Second, s.Elapsed, "clock is monotonic")
}
