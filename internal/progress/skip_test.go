package progress

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// feed replays lines through an estimator at fixed intervals.
func feed(e *Estimator, lines []string) Snapshot {
	for i, ln := range lines {
		e.Observe(ln, wallBase.Add(time.Duration(i)*10*time.Second))
	}
	return e.Snapshot()
}

func TestSkippedWorkStopsCappingTheBar(t *testing.T) {
	lines := steps(80)
	m := timedModel(t, lines, uniform(80, 10*time.Second))

	// This run skips the middle, as an incremental build does. It still ends.
	var run []string
	run = append(run, lines[:20]...)
	run = append(run, lines[60:]...)
	s := feed(NewEstimator(m), run)

	t.Logf("progress %.3f, skipped %.3f, matched %d", s.Progress, s.Skipped, s.MatchedLines)
	assert.Positive(t, s.Skipped, "the stretch it never printed is retired")
	assert.InDelta(t, 1.0, s.Progress, 0.05, "a finished run reads as finished")
}

func TestNothingIsRetiredWithoutEnoughEvidence(t *testing.T) {
	lines := steps(80)
	m := timedModel(t, lines, uniform(80, 10*time.Second))

	// Fewer matches than the window: the run has not been placed at all.
	s := feed(NewEstimator(m), lines[70:79])
	require.Positive(t, s.MatchedLines)
	assert.Zero(t, s.Skipped, "too few matches to retire anything")
	assert.Less(t, s.Progress, 0.35, "the bar does not jump to the end")
}

func TestAStrayLateMatchDoesNotMoveTheRun(t *testing.T) {
	lines := steps(80)
	m := timedModel(t, lines, uniform(80, 10*time.Second))

	// A line from the end of the reference shows up early.
	var run []string
	run = append(run, lines[:35]...)
	run = append(run, lines[79])
	s := feed(NewEstimator(m), run)

	t.Logf("progress %.3f, skipped %.3f", s.Progress, s.Skipped)
	assert.Less(t, s.Progress, 0.6, "one late line is not proof the run is nearly done")
}

func TestOutOfOrderLinesDoNotRetireWork(t *testing.T) {
	lines := steps(80)
	m := timedModel(t, lines, uniform(80, 10*time.Second))

	// A parallel build prints jumbled: nothing behind it may be retired.
	jumbled := make([]string, 40)
	for i := range jumbled {
		jumbled[i] = lines[39-i]
	}
	s := feed(NewEstimator(m), jumbled)

	t.Logf("progress %.3f, skipped %.3f", s.Progress, s.Skipped)
	assert.InDelta(t, 0.5, s.Progress, 0.1, "half the work is half the progress")
}
