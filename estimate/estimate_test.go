package estimate_test

import (
	"fmt"
	"io/fs"
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

// tokens builds a run of n tokens under prefix, evenly spaced in time.
func tokens(prefix string, n int) []estimate.Token {
	toks := make([]estimate.Token, n)
	for i := range toks {
		toks[i] = estimate.TokenOf(fmt.Sprintf("%s-%04d", prefix, i))
	}
	return toks
}

// recordRun digests toks as a completed run, evenly spaced in time.
func recordRun(t *testing.T, source string, toks []estimate.Token) *estimate.Run {
	t.Helper()
	rec := estimate.NewRecorder(source)
	for i, tok := range toks {
		rec.Observe(tok, base.Add(time.Duration(i)*time.Second))
	}
	run, err := rec.Finish()
	require.NoError(t, err)
	return run
}

// modelOf builds a model of a lone run over toks.
func modelOf(t *testing.T, key string, toks []estimate.Token) *estimate.Model {
	t.Helper()
	m := estimate.NewModel(key)
	m.Add(recordRun(t, key+".run", toks))
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
	toks := tokens("step", 60)
	m := modelOf(t, "job", toks)
	require.Equal(t, 60, m.Units())
	require.True(t, m.HasTimes())
	require.Equal(t, 59*time.Second, m.RefDuration())

	est := estimate.NewEstimator(m)
	for i, tok := range toks[:30] {
		est.Observe(tok, base.Add(time.Duration(i)*time.Second))
	}
	mid := est.Estimate()
	assert.InDelta(t, 0.5, mid.Progress, 0.06, "half the tokens is about half the run")
	assert.Equal(t, 30, mid.UnitsDone)
	assert.Equal(t, 60, mid.UnitsTotal)
	assert.Equal(t, "high", mid.Confidence)
	assert.Equal(t, "pace", mid.ETAKind)
	assert.InDelta(t, 30*time.Second, mid.ETA, float64(6*time.Second))

	for i, tok := range toks[30:] {
		est.Observe(tok, base.Add(time.Duration(30+i)*time.Second))
	}
	done := est.Estimate()
	assert.InDelta(t, 1, done.Progress, 0.02)
	assert.Equal(t, 60, done.UnitsDone)
	assert.Less(t, done.ETA, 3*time.Second)
}

func TestEstimatorWithoutTimestamps(t *testing.T) {
	toks := tokens("event", 40)
	rec := estimate.NewRecorder("untimed")
	for _, tok := range toks {
		rec.Observe(tok, time.Time{})
	}
	run, err := rec.Finish()
	require.NoError(t, err)
	assert.False(t, run.HasTimes, "no token carried a clock")

	m := estimate.NewModel("untimed")
	m.Add(run)
	est := estimate.NewEstimator(m)
	for _, tok := range toks[:20] {
		est.Observe(tok, time.Time{})
	}
	got := est.Estimate()
	assert.InDelta(t, 0.5, got.Progress, 0.05, "tokens weigh equally without a clock")
	assert.Equal(t, "none", got.ETAKind, "no reference clock, no ETA")
	assert.False(t, got.ElapsedKnown)
}

func TestEstimatorTickSuppliesTheClock(t *testing.T) {
	toks := tokens("step", 40)
	m := modelOf(t, "ticked", toks)
	est := estimate.NewEstimator(m)
	// The live run's tokens carry no times of their own, so the caller ticks.
	est.Observe(toks[0], base)
	for _, tok := range toks[1:20] {
		est.Observe(tok, time.Time{})
	}
	est.Tick(base.Add(30 * time.Second))
	got := est.Estimate()
	assert.True(t, got.ElapsedKnown)
	assert.Equal(t, 30*time.Second, got.Elapsed)
	assert.Equal(t, "pace", got.ETAKind)
	assert.Greater(t, got.Pace, 1.0, "30s for what the reference did in ~19s is slow")
}

func TestNovelTokensLowerConfidence(t *testing.T) {
	m := modelOf(t, "known", tokens("step", 40))
	est := estimate.NewEstimator(m)
	for i, tok := range tokens("other", 20) {
		est.Observe(tok, base.Add(time.Duration(i)*time.Second))
	}
	got := est.Estimate()
	assert.Equal(t, "low", got.Confidence)
	assert.Equal(t, 20, got.NovelLines)
	assert.Zero(t, got.UnitsDone)
}

func TestEmptyModelRecordsABaseline(t *testing.T) {
	m := estimate.NewModel("first-ever")
	est := estimate.NewEstimator(m)
	est.Observe(estimate.TokenOf("anything"), base)
	got := est.Estimate()
	assert.Equal(t, "none", got.Confidence)
	assert.Zero(t, got.UnitsTotal)
	assert.Zero(t, got.Progress)
}

func TestRecorderNeedsTwoTokens(t *testing.T) {
	rec := estimate.NewRecorder("tiny")
	rec.Observe(estimate.TokenOf("only"), base)
	_, err := rec.Finish()
	assert.Error(t, err, "one token places nothing")
}

func TestModelMergesRunsAndKeepsALabel(t *testing.T) {
	toks := tokens("step", 30)
	m := estimate.NewModel("merged")
	m.Add(recordRun(t, "run1", toks))
	m.Add(recordRun(t, "run2", toks))
	assert.Equal(t, 2, m.Runs())
	assert.Equal(t, 30, m.Units(), "the same work seen twice is still 30 units")
	assert.Equal(t, "merged", m.Label(), "no label recorded yet: the key stands in")
	m.AddLabel("nightly import")
	assert.Equal(t, "nightly import", m.Label())
}

func TestModelEvictsBeyondMaxRuns(t *testing.T) {
	toks := tokens("step", 10)
	m := estimate.NewModel("capped")
	for i := 0; i < estimate.MaxRuns+3; i++ {
		m.Add(recordRun(t, fmt.Sprintf("run%d", i), toks))
	}
	assert.Equal(t, estimate.MaxRuns, m.Runs())
}

func TestContentKeyFollowsTheTokens(t *testing.T) {
	toks := tokens("step", 20)
	same := estimate.ContentKey(recordRun(t, "a", toks))
	assert.Equal(t, same, estimate.ContentKey(recordRun(t, "b", toks)))
	assert.NotEqual(t, same, estimate.ContentKey(recordRun(t, "c", tokens("other", 20))))
	assert.True(t, strings.HasPrefix(same, "auto."), "content keys live in their own namespace")
}

func TestStoreRoundTrip(t *testing.T) {
	store := estimate.OpenStore(t.TempDir())
	m := modelOf(t, "job/one", tokens("step", 20))
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

	require.NoError(t, store.Remove(m.Key()))
	_, err = store.Load(m.Key())
	assert.ErrorIs(t, err, fs.ErrNotExist, "a key never learned reads as not-found")
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
	build := tokens("build", 40)
	deploy := tokens("deploy", 40)
	mt := estimate.NewMatcher(modelOf(t, "build", build), modelOf(t, "deploy", deploy))

	_, _, ok := mt.Locked()
	assert.False(t, ok, "nothing is identified before any token arrives")

	for i, tok := range deploy {
		mt.Observe(tok, base.Add(time.Duration(i)*time.Second))
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
	mt := estimate.NewMatcher(modelOf(t, "build", tokens("build", 40)))
	mt.Observe(estimate.TokenOf("build-0000"), base)
	got := mt.Estimate()
	assert.True(t, got.Identifying, "one token is not enough to identify a run")
	assert.Zero(t, got.Progress)
}

func TestMatcherOnUnknownOutputNeverLocks(t *testing.T) {
	mt := estimate.NewMatcher(modelOf(t, "build", tokens("build", 40)))
	for i, tok := range tokens("something-else", 40) {
		mt.Observe(tok, base.Add(time.Duration(i)*time.Second))
	}
	_, _, ok := mt.Locked()
	assert.False(t, ok)
	_, _, ok = mt.MergeTarget()
	assert.False(t, ok, "an unrecognized run belongs under a new key")
}

func TestMatcherWithNoModelsJustCounts(t *testing.T) {
	mt := estimate.NewMatcher()
	mt.Observe(estimate.TokenOf("a"), base)
	mt.Observe(estimate.TokenOf("b"), base.Add(time.Second))
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
	est := estimate.NewEstimator(m)
	est.ObserveLine("[  1%] Building C object src/core/alloc.c.o", base)
	assert.Positive(t, est.Estimate().CurrentLines)

	rec := estimate.NewRecorder("lines")
	rec.ObserveLine("first step", base)
	rec.ObserveLine("second step", base.Add(time.Second))
	byLine, err := rec.Finish()
	require.NoError(t, err)
	assert.Equal(t, 2, byLine.Lines)
}

func TestRecordReaderDigestsAStream(t *testing.T) {
	run, err := estimate.RecordReader(strings.NewReader("alpha\nbeta\ngamma\n"), "stream")
	require.NoError(t, err)
	assert.Equal(t, 3, run.Lines)
	assert.False(t, run.HasTimes, "a bare stream carries no clock")
}
