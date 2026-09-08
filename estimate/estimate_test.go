package estimate_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/lpi/estimate"
)

// base is the reference clock every synthetic run in this file starts at.
var base = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// Step is a caller's own token type, which is what the generic API is for.
type Step string

// byStep is the Tokenizer these tests estimate and record through.
var byStep = estimate.Identifiers[Step]

// steps builds a run of n steps under prefix, evenly spaced in time.
func steps(prefix string, n int) []Step {
	out := make([]Step, n)
	for i := range out {
		out[i] = Step(fmt.Sprintf("%s-%04d", prefix, i))
	}
	return out
}

// recordRun digests run as a completed run, evenly spaced in time.
func recordRun(t *testing.T, source string, run []Step) *estimate.Run {
	t.Helper()
	rec := estimate.NewRecorder(source, byStep)
	for i, step := range run {
		rec.Observe(step, base.Add(time.Duration(i)*time.Second))
	}
	done, err := rec.Finish()
	require.NoError(t, err)
	return done
}

// modelOf builds a model of a lone run over the steps.
func modelOf(t *testing.T, key string, run []Step) *estimate.Model {
	t.Helper()
	m := estimate.NewModel(key)
	m.Add(recordRun(t, key+".run", run))
	return m
}

func TestTokenOfIsStableAndDistinct(t *testing.T) {
	assert.Equal(t, estimate.TokenOf("compile widget.c"), estimate.TokenOf("compile widget.c"))
	assert.NotEqual(t, estimate.TokenOf("compile widget.c"), estimate.TokenOf("compile gadget.c"))
	// TokenOf hashes what it is given: noise the caller leaves in is identity.
	assert.NotEqual(t, estimate.TokenOf("step 1"), estimate.TokenOf("step 2"))
}

func TestTokenOfLineCollapsesNoise(t *testing.T) {
	a, ok := estimate.TokenOfLine("10:04:07 [ 62%] Building C object src/net/tls.c.o")
	require.True(t, ok)
	b, ok := estimate.TokenOfLine("09:31:02 [ 58%] Building C object src/net/tls.c.o")
	require.True(t, ok)
	assert.Equal(t, a, b, "two runs of the same step share one token")

	c, ok := estimate.TokenOfLine("Building C object src/net/http.c.o")
	require.True(t, ok)
	assert.NotEqual(t, a, c)

	_, ok = estimate.TokenOfLine("   ")
	assert.False(t, ok, "a blank line carries nothing identifying")
	assert.Contains(t, estimate.Normalize("build 1234 done"), "#")
}

func TestEstimatorTracksATimedRun(t *testing.T) {
	run := steps("step", 60)
	m := modelOf(t, "job", run)
	require.Equal(t, 60, m.Units())
	require.True(t, m.HasTimes())
	require.Equal(t, 59*time.Second, m.RefDuration())

	est := estimate.NewEstimator(m, byStep)
	for i, step := range run[:30] {
		est.Observe(step, base.Add(time.Duration(i)*time.Second))
	}
	mid := est.Estimate()
	assert.InDelta(t, 0.5, mid.Progress, 0.06, "half the steps is about half the run")
	assert.Equal(t, 30, mid.UnitsDone)
	assert.Equal(t, 60, mid.UnitsTotal)
	assert.Equal(t, "high", mid.Confidence)
	assert.Equal(t, "pace", mid.ETAKind)
	assert.InDelta(t, 30*time.Second, mid.ETA, float64(6*time.Second))

	for i, step := range run[30:] {
		est.Observe(step, base.Add(time.Duration(30+i)*time.Second))
	}
	done := est.Estimate()
	assert.InDelta(t, 1, done.Progress, 0.02)
	assert.Equal(t, 60, done.UnitsDone)
	assert.Less(t, done.ETA, 3*time.Second)
}

func TestEstimatorWithoutTimestamps(t *testing.T) {
	events := steps("event", 40)
	rec := estimate.NewRecorder("untimed", byStep)
	for _, ev := range events {
		rec.Observe(ev, time.Time{})
	}
	run, err := rec.Finish()
	require.NoError(t, err)
	assert.False(t, run.HasTimes, "nothing carried a clock")

	m := estimate.NewModel("untimed")
	m.Add(run)
	est := estimate.NewEstimator(m, byStep)
	for _, ev := range events[:20] {
		est.Observe(ev, time.Time{})
	}
	got := est.Estimate()
	assert.InDelta(t, 0.5, got.Progress, 0.05, "tokens weigh equally without a clock")
	assert.Equal(t, "none", got.ETAKind, "no reference clock, no ETA")
	assert.False(t, got.ElapsedKnown)
}

func TestEstimatorTickSuppliesTheClock(t *testing.T) {
	run := steps("step", 40)
	m := modelOf(t, "ticked", run)
	est := estimate.NewEstimator(m, byStep)
	// The live run's steps carry no times of their own, so the caller ticks.
	est.Observe(run[0], base)
	for _, step := range run[1:20] {
		est.Observe(step, time.Time{})
	}
	est.Tick(base.Add(30 * time.Second))
	got := est.Estimate()
	assert.True(t, got.ElapsedKnown)
	assert.Equal(t, 30*time.Second, got.Elapsed)
	assert.Equal(t, "pace", got.ETAKind)
	assert.Greater(t, got.Pace, 1.0, "30s for what the reference did in ~19s is slow")
}

func TestNovelTokensLowerConfidence(t *testing.T) {
	m := modelOf(t, "known", steps("step", 40))
	est := estimate.NewEstimator(m, byStep)
	for i, step := range steps("other", 20) {
		est.Observe(step, base.Add(time.Duration(i)*time.Second))
	}
	got := est.Estimate()
	assert.Equal(t, "low", got.Confidence)
	assert.Equal(t, 20, got.NovelLines)
	assert.Zero(t, got.UnitsDone)
}

func TestEmptyModelRecordsABaseline(t *testing.T) {
	m := estimate.NewModel("first-ever")
	est := estimate.NewEstimator(m, byStep)
	est.Observe(Step("anything"), base)
	got := est.Estimate()
	assert.Equal(t, "none", got.Confidence)
	assert.Zero(t, got.UnitsTotal)
	assert.Zero(t, got.Progress)
}

func TestRecorderNeedsTwoTokens(t *testing.T) {
	rec := estimate.NewRecorder("tiny", byStep)
	rec.Observe(Step("only"), base)
	_, err := rec.Finish()
	assert.Error(t, err, "a lone token places nothing")
}

func TestModelMergesRunsAndKeepsALabel(t *testing.T) {
	run := steps("step", 30)
	m := estimate.NewModel("merged")
	m.Add(recordRun(t, "run1", run))
	m.Add(recordRun(t, "run2", run))
	assert.Len(t, m.Runs(), 2)
	assert.Equal(t, 30, m.Units(), "the same work seen twice is still 30 units")
	assert.Equal(t, "merged", m.Label(), "no label recorded yet: the key stands in")
	m.AddLabel("nightly import")
	assert.Equal(t, "nightly import", m.Label())
}

func TestModelEvictsBeyondMaxRuns(t *testing.T) {
	run := steps("step", 10)
	m := estimate.NewModel("capped")
	for i := 0; i < estimate.MaxRuns+3; i++ {
		m.Add(recordRun(t, fmt.Sprintf("run%d", i), run))
	}
	assert.Len(t, m.Runs(), estimate.MaxRuns)
}

func TestContentKeyFollowsTheTokens(t *testing.T) {
	run := steps("step", 20)
	same := estimate.ContentKey(recordRun(t, "a", run))
	assert.Equal(t, same, estimate.ContentKey(recordRun(t, "b", run)))
	assert.NotEqual(t, same, estimate.ContentKey(recordRun(t, "c", steps("other", 20))))
	assert.True(t, strings.HasPrefix(same, "auto."), "content keys live in their own namespace")
}

func TestStoreRoundTrip(t *testing.T) {
	store := estimate.OpenStore(t.TempDir())
	m := modelOf(t, "job/one", steps("step", 20))
	m.AddLabel("make -j8")
	require.NoError(t, store.Save(m))

	keys, err := store.Keys()
	require.NoError(t, err)
	require.Len(t, keys, 1)

	back, err := store.Load(m.Key())
	require.NoError(t, err)
	assert.Equal(t, "job/one", back.Key(), "the key survives the file-name sanitizing")
	assert.Equal(t, "make -j8", back.Label())
	assert.Equal(t, m.Units(), back.Units())
	assert.Equal(t, m.RefDuration(), back.RefDuration())

	models, err := store.Models()
	require.NoError(t, err)
	require.Len(t, models, 1)

	back, fresh, err := store.LoadOrNew(m.Key())
	require.NoError(t, err)
	assert.False(t, fresh)
	assert.Equal(t, m.Units(), back.Units())

	require.NoError(t, store.Remove(m.Key()))
	_, err = store.Load(m.Key())
	assert.ErrorIs(t, err, fs.ErrNotExist, "a key never learned reads as not-found")

	empty, fresh, err := store.LoadOrNew("never-learned")
	require.NoError(t, err)
	assert.True(t, fresh, "a key with no model starts one instead of failing")
	assert.Zero(t, empty.Units())
}

func TestStoreOnAMissingDirectoryIsEmpty(t *testing.T) {
	store := estimate.OpenStore(filepath.Join(t.TempDir(), "never-created"))
	keys, err := store.Keys()
	require.NoError(t, err)
	assert.Empty(t, keys)
	models, err := store.Models()
	require.NoError(t, err)
	assert.Empty(t, models)
}

func TestOpenStoreDefaultsToTheCLIDatabase(t *testing.T) {
	t.Setenv("LPI_DB", t.TempDir())
	assert.Equal(t, estimate.DefaultDir(), estimate.OpenStore("").Dir())
}

func TestMatcherLocksOntoTheRightModel(t *testing.T) {
	build := steps("build", 40)
	deploy := steps("deploy", 40)
	mt := estimate.NewMatcher(byStep, modelOf(t, "build", build), modelOf(t, "deploy", deploy))

	_, _, ok := mt.Locked()
	assert.False(t, ok, "nothing is identified before any token arrives")

	for i, step := range deploy {
		mt.Observe(step, base.Add(time.Duration(i)*time.Second))
	}
	key, label, ok := mt.Locked()
	require.True(t, ok)
	assert.Equal(t, "deploy", key)
	assert.Equal(t, "deploy", label)

	best, rate, ok := mt.Best()
	require.True(t, ok)
	assert.Equal(t, "deploy", best)
	assert.InDelta(t, 1, rate, 0.001)

	got := mt.Estimate()
	assert.False(t, got.Identifying)
	assert.Equal(t, "deploy", got.Label)
	assert.InDelta(t, 1, got.Progress, 0.02)

	target, _, ok := mt.MergeTarget()
	require.True(t, ok, "a run that matched throughout refines its own model")
	assert.Equal(t, "deploy", target)
}

func TestMatcherReportsIdentifyingUntilItFits(t *testing.T) {
	mt := estimate.NewMatcher(byStep, modelOf(t, "build", steps("build", 40)))
	mt.Observe(Step("build-0000"), base)
	got := mt.Estimate()
	assert.True(t, got.Identifying, "a lone token cannot identify a run")
	assert.Zero(t, got.Progress)
}

func TestMatcherOnUnknownOutputNeverLocks(t *testing.T) {
	mt := estimate.NewMatcher(byStep, modelOf(t, "build", steps("build", 40)))
	for i, step := range steps("something-else", 40) {
		mt.Observe(step, base.Add(time.Duration(i)*time.Second))
	}
	_, _, ok := mt.Locked()
	assert.False(t, ok)
	_, _, ok = mt.MergeTarget()
	assert.False(t, ok, "an unrecognized run belongs under a new key")
}

func TestMatcherWithNoModelsJustCounts(t *testing.T) {
	mt := estimate.NewMatcher(byStep)
	mt.Observe(Step("a"), base)
	mt.Observe(Step("b"), base.Add(time.Second))
	mt.Tick(base.Add(2 * time.Second))
	got := mt.Estimate()
	assert.False(t, got.Identifying, "with nothing to identify against there is nothing to wait for")
	assert.Equal(t, 2, got.CurrentLines)
}

func TestLineAPIEstimatesALog(t *testing.T) {
	run, err := estimate.RecordFile(filepath.Join("..", "testdata", "demo", "build1.log"))
	require.NoError(t, err)
	assert.Greater(t, run.Lines, 100)
	assert.True(t, run.HasTimes)

	m := estimate.NewModel("demo")
	m.Add(run)
	// A log is just a token stream whose Tokenizer is TokenOfLine.
	est := estimate.NewEstimator(m, estimate.TokenOfLine)
	est.Observe("[  1%] Building C object src/core/alloc.c.o", base)
	assert.Positive(t, est.Estimate().CurrentLines)

	rec := estimate.NewRecorder("lines", estimate.TokenOfLine)
	rec.Observe("first step", base)
	rec.Observe("second step", base.Add(time.Second))
	byLine, err := rec.Finish()
	require.NoError(t, err)
	assert.Equal(t, 2, byLine.Lines)
}

func TestPinnedFormatReadsStampsNoDetectorKnows(t *testing.T) {
	log := filepath.Join(t.TempDir(), "odd.log")
	body := "(01.03.2026 09h00m00s) start\n(01.03.2026 09h04m00s) middle\n" +
		"(01.03.2026 09h10m00s) done\n"
	require.NoError(t, os.WriteFile(log, []byte(body), 0o644))

	format, err := estimate.CompileFormat(`^\((?P<time>[^)]+)\)`, "02.01.2006 15h04m05s")
	require.NoError(t, err)
	run, err := estimate.RecordFileWith(log, format)
	require.NoError(t, err)
	assert.True(t, run.HasTimes)
	assert.Equal(t, 10*time.Minute, run.Duration)

	// Nothing detects that shape, so the same log reads as untimed without it.
	plain, err := estimate.RecordFile(log)
	require.NoError(t, err)
	assert.False(t, plain.HasTimes)
	assert.Nil(t, estimate.DetectFormat([]string{"(01.03.2026 09h00m00s) start"}))
	assert.NotEmpty(t, estimate.FormatNames())
	assert.NotEmpty(t, estimate.FormatGroups())
}

func TestStamperIsTheClockEveryPathShares(t *testing.T) {
	format, err := estimate.CompileFormat("clock", "")
	require.NoError(t, err)
	s := estimate.NewStamper(format)
	_, _, timed := s.Stamp("no stamp here")
	assert.False(t, timed, "nothing has stamped the stream yet")

	at, _, timed := s.Stamp("09:00:00 start")
	require.True(t, timed)
	assert.Equal(t, 9, at.Hour())

	carried, gap, timed := s.Stamp("still working")
	assert.True(t, timed, "an unstamped line carries the previous time")
	assert.Equal(t, at, carried)
	assert.Zero(t, gap)

	back, gap, _ := s.Stamp("08:00:00 out of order")
	assert.Equal(t, at, back, "the clock never moves backwards")
	assert.Zero(t, gap)

	on, gap, _ := s.Stamp("09:02:30 later")
	assert.Equal(t, 150*time.Second, gap)
	assert.Equal(t, on, s.Last())
}

func TestReplayFileScoresAFinishedLog(t *testing.T) {
	log := filepath.Join(t.TempDir(), "run.log")
	body := "09:00:00 start\n09:02:00 middle\n09:05:00 done\n"
	require.NoError(t, os.WriteFile(log, []byte(body), 0o644))
	run, err := estimate.RecordFile(log)
	require.NoError(t, err)

	m := estimate.NewModel("replayed")
	m.Add(run)
	est := estimate.NewEstimator(m, estimate.TokenOfLine)
	require.NoError(t, estimate.ReplayFile(log, nil, est.Observe))
	got := est.Estimate()
	assert.Equal(t, 3, got.UnitsDone)
	assert.InDelta(t, 1, got.Progress, 0.02, "replaying a log covers the run it came from")
}

func TestCaptureSurvivesARunThatNeverFinished(t *testing.T) {
	dir := t.TempDir()
	cap, err := estimate.NewCapture(dir, "job", "run under test")
	require.NoError(t, err)
	assert.Equal(t, estimate.PendingDir(dir), filepath.Dir(cap.Path()))
	require.NoError(t, cap.Add("step one", base))
	require.NoError(t, cap.Add("step two", base.Add(time.Minute)))
	require.NoError(t, cap.Close())

	// The kept file is what "lpi learn" ingests: the run, with its own times.
	run, err := estimate.RecordFile(cap.Path())
	require.NoError(t, err)
	assert.Equal(t, 2, run.Lines)
	assert.Equal(t, time.Minute, run.Duration)

	cap.Discard()
	_, err = os.Stat(cap.Path())
	assert.ErrorIs(t, err, fs.ErrNotExist)

	var absent *estimate.Capture
	assert.NoError(t, absent.Add("dropped", base), "a capture that never opened swallows lines")
	assert.NoError(t, absent.Close())
	assert.Empty(t, absent.Path())
	absent.Discard()
}

func TestRecordReaderDigestsAStream(t *testing.T) {
	run, err := estimate.RecordReader(strings.NewReader("alpha\nbeta\ngamma\n"), "stream")
	require.NoError(t, err)
	assert.Equal(t, 3, run.Lines)
	assert.False(t, run.HasTimes, "a bare stream carries no clock")
}
