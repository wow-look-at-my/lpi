package eval

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/lpi/internal/model"
)

var base = time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)

// writeLog writes a stamped log whose lines are gap apart, and returns its path.
func writeLog(t *testing.T, name string, lines int, gap time.Duration) string {
	t.Helper()
	var b strings.Builder
	for i := range lines {
		at := base.Add(time.Duration(i) * gap)
		fmt.Fprintf(&b, "%s compiling widget %c of the build\n",
			at.Format("2006-01-02T15:04:05Z"), 'a'+rune(i%26))
	}
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o600))
	return path
}

// target digests a log the way the eval command does.
func target(t *testing.T, path string) Target {
	t.Helper()
	run, err := model.DigestFileWith(path, nil)
	require.NoError(t, err)
	return Target{Path: path, Run: run}
}

func TestScoreIdenticalRunsIsNearlyPerfect(t *testing.T) {
	a := writeLog(t, "a.log", 40, 10*time.Second)
	b := writeLog(t, "b.log", 40, 10*time.Second)

	results, err := LeaveOneOut([]Target{target(t, a), target(t, b)}, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)

	for _, r := range results {
		assert.False(t, r.SelfFit, "each log is scored against the other one")
		assert.Equal(t, 1, r.RefRuns)
		assert.InDelta(t, 1.0, r.MatchRate, 1e-9)
		assert.Less(t, r.MeanAbsErr, 0.05, "a repeat of the reference is tracked closely")
		assert.Equal(t, "excellent", r.Grade())
		assert.InDelta(t, 1.0, r.FinalPred, 0.02)
	}
	o := Aggregate(results)
	assert.Equal(t, "excellent", o.Grade())
	assert.False(t, o.SelfFit)
	assert.Contains(t, Verdict(o), "excellent")
}

func TestScoreCheckpointsCoverTheWholeRun(t *testing.T) {
	a := writeLog(t, "a.log", 40, 10*time.Second)
	b := writeLog(t, "b.log", 40, 10*time.Second)
	results, err := LeaveOneOut([]Target{target(t, a), target(t, b)}, nil)
	require.NoError(t, err)

	r := results[0]
	require.Len(t, r.Checkpoints, len(checkpoints))
	assert.InDelta(t, 1.0, r.Checkpoints[len(r.Checkpoints)-1].Truth, 1e-9)
	for i, p := range r.Checkpoints {
		assert.GreaterOrEqual(t, p.Truth, checkpoints[i]-1e-9,
			"a checkpoint reports the first estimate at or past its mark")
		if i > 0 {
			assert.GreaterOrEqual(t, p.Truth, r.Checkpoints[i-1].Truth)
		}
	}
}

func TestScoreUntimedLogFallsBackToLineCount(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, "linking module %c of the build\n", 'a'+rune(i%26))
	}
	path := filepath.Join(dir, "plain.log")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o600))

	results, err := LeaveOneOut([]Target{target(t, path)}, nil)
	require.NoError(t, err)
	r := results[0]
	assert.True(t, r.SelfFit, "a lone log has only itself to be judged against")
	assert.False(t, r.HasTimes)
	assert.Zero(t, r.ETAPoints, "no clock means no ETA to score")
	assert.Less(t, r.MeanAbsErr, 0.1)
}

func TestScoreAgainstStoredModel(t *testing.T) {
	a := writeLog(t, "a.log", 40, 10*time.Second)
	b := writeLog(t, "b.log", 40, 10*time.Second)
	m := model.New("stored")
	m.AddRun(target(t, a).Run)

	results, err := Against(m, []Target{target(t, b)}, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 1, results[0].RefRuns)
	assert.False(t, results[0].SelfFit)
	assert.Positive(t, results[0].ETAPoints, "a timed run is scored on its ETA too")
}

func TestScoreSlowerRunIsBehindTheReference(t *testing.T) {
	fast := writeLog(t, "fast.log", 40, 10*time.Second)
	slow := writeLog(t, "slow.log", 40, 20*time.Second)
	m := model.New("stored")
	m.AddRun(target(t, fast).Run)

	results, err := Against(m, []Target{target(t, slow)}, nil)
	require.NoError(t, err)
	r := results[0]
	// Same lines, slower clock: lpi reports more progress than was made.
	assert.Positive(t, r.Bias)
	assert.Positive(t, r.MaxAbsErr)
	assert.Positive(t, r.ETAMeanRelErr)
}

func TestScoreRejectsShortAndMissingInput(t *testing.T) {
	_, err := Score(model.New("m"), Target{Path: "nope.log"}, nil)
	assert.ErrorContains(t, err, "no digest")

	a := writeLog(t, "a.log", 40, 10*time.Second)
	tg := target(t, a)
	tg.Path = filepath.Join(t.TempDir(), "gone.log")
	_, err = Score(model.New("m"), tg, nil)
	assert.ErrorContains(t, err, "gone.log")

	_, err = LeaveOneOut(nil, nil)
	assert.ErrorContains(t, err, "no logs")
}

func TestGradeTiers(t *testing.T) {
	assert.Equal(t, "excellent", Grade(0.01))
	assert.Equal(t, "good", Grade(0.07))
	assert.Equal(t, "rough", Grade(0.15))
	assert.Equal(t, "poor", Grade(0.5))
}

func TestReportShowsEveryLogAndAVerdict(t *testing.T) {
	a := writeLog(t, "a.log", 40, 10*time.Second)
	b := writeLog(t, "b.log", 40, 10*time.Second)
	results, err := LeaveOneOut([]Target{target(t, a), target(t, b)}, nil)
	require.NoError(t, err)

	var buf bytes.Buffer
	Report(&buf, results, true)
	out := buf.String()
	assert.Contains(t, out, "a.log")
	assert.Contains(t, out, "b.log")
	assert.Contains(t, out, "all", "several logs get an overall row")
	assert.Contains(t, out, "verdict: excellent")
	assert.Contains(t, out, "true left", "--detail prints the checkpoint table")
	assert.NotContains(t, out, "scored against itself")
}

func TestReportFlagsSelfFitAndUntimedLogs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.log")
	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, "packing asset %c\n", 'a'+rune(i%26))
	}
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o600))
	results, err := LeaveOneOut([]Target{target(t, path)}, nil)
	require.NoError(t, err)

	var buf bytes.Buffer
	Report(&buf, results, false)
	out := buf.String()
	assert.Contains(t, out, "scored against itself")
	assert.Contains(t, out, "no timestamps")
	assert.Contains(t, out, "untimed")

	var empty bytes.Buffer
	Report(&empty, nil, false)
	assert.Contains(t, empty.String(), "nothing to score")
}

func TestJSONCarriesTheScores(t *testing.T) {
	a := writeLog(t, "a.log", 40, 10*time.Second)
	b := writeLog(t, "b.log", 40, 10*time.Second)
	results, err := LeaveOneOut([]Target{target(t, a), target(t, b)}, nil)
	require.NoError(t, err)

	j := JSON(results[0])
	assert.Equal(t, results[0].Source, j.Source)
	assert.Equal(t, "excellent", j.Grade)
	assert.Len(t, j.Checkpoints, len(checkpoints))
	assert.Positive(t, j.Checkpoints[0].TrueLeftSecs)
	assert.InDelta(t, results[0].MeanAbsErr, j.MeanAbsErr, 1e-12)
}

func TestTrimNameKeepsTheIdentifyingTail(t *testing.T) {
	assert.Equal(t, "short.log", trimName("short.log", 24))
	assert.Equal(t, "build.log", trimName("/very/deep/path/to/the/build.log", 24))
	assert.Equal(t, "...gth.log", trimName("/deep/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaalength.log", 10))
}
