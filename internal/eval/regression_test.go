package eval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every test here states a property a real corpus caught lpi breaking. Each
// scores the same holdout against models differing only in what was learned.

func TestSecondReferenceNeverHurts(t *testing.T) {
	dir := t.TempDir()
	holdout := corpusTarget(t, dir, runSpec{name: "holdout", quirk: 10})
	refs := []Target{
		corpusTarget(t, dir, runSpec{name: "refa", quirk: 10}),
		corpusTarget(t, dir, runSpec{name: "refb", quirk: 10}),
		corpusTarget(t, dir, runSpec{name: "refc", quirk: 10}),
	}

	one := scoreAgainst(t, holdout, refs[0])
	two := scoreAgainst(t, holdout, refs[0], refs[1])
	three := scoreAgainst(t, holdout, refs...)

	t.Logf("mean abs err: 1 ref %.3f, 2 refs %.3f, 3 refs %.3f",
		one.MeanAbsErr, two.MeanAbsErr, three.MeanAbsErr)
	assert.LessOrEqual(t, two.MeanAbsErr, one.MeanAbsErr+0.005,
		"a second reference of the same task must not make the estimate worse")
	assert.LessOrEqual(t, three.MeanAbsErr, one.MeanAbsErr+0.005,
		"a third reference must not make the estimate worse either")
}

func TestLinesOnlyOneReferenceEverPrintsDoNotStealProgress(t *testing.T) {
	dir := t.TempDir()
	holdout := corpusTarget(t, dir, runSpec{name: "holdout"})
	plain := corpusTarget(t, dir, runSpec{name: "plain"})
	quirky := corpusTarget(t, dir, runSpec{name: "quirky", quirk: 20})

	// The quirky reference prints lines no other run does: its own habit.
	clean := scoreAgainst(t, holdout, plain)
	mixed := scoreAgainst(t, holdout, plain, quirky)

	t.Logf("final progress: clean %.3f, with a quirky reference %.3f",
		clean.FinalPred, mixed.FinalPred)
	assert.LessOrEqual(t, mixed.MeanAbsErr, clean.MeanAbsErr+0.02)
	assert.GreaterOrEqual(t, mixed.FinalPred, 0.9,
		"a finished run must read as nearly finished")
}

func TestTruncatedReferenceDoesNotSkewTheModel(t *testing.T) {
	dir := t.TempDir()
	holdout := corpusTarget(t, dir, runSpec{name: "holdout"})
	good := corpusTarget(t, dir, runSpec{name: "good"})
	cut := corpusTarget(t, dir, runSpec{name: "cut", truncate: 0.1})

	clean := scoreAgainst(t, holdout, good)
	polluted := scoreAgainst(t, holdout, good, cut)

	t.Logf("mean abs err: clean %.3f, with a truncated reference %.3f (bias %+.3f)",
		clean.MeanAbsErr, polluted.MeanAbsErr, polluted.Bias)
	// A log cut short states per-line shares of its own short duration, so
	// merging them at face value makes the bar race away early.
	assert.Less(t, polluted.Bias, 0.05,
		"a truncated reference must not make the estimate run ahead")
	assert.LessOrEqual(t, polluted.MeanAbsErr, clean.MeanAbsErr+0.03)
}

func TestSlowerReferenceIsJustAsGood(t *testing.T) {
	dir := t.TempDir()
	holdout := corpusTarget(t, dir, runSpec{name: "holdout"})
	fast := corpusTarget(t, dir, runSpec{name: "fast"})
	slow := corpusTarget(t, dir, runSpec{name: "slow", pace: 2})

	// Same work at half the speed has the same shape, so the same estimate.
	quick := scoreAgainst(t, holdout, fast)
	both := scoreAgainst(t, holdout, fast, slow)

	require.Positive(t, quick.Lines)
	assert.InDelta(t, quick.MeanAbsErr, both.MeanAbsErr, 0.02,
		"a run of the same work at another speed carries the same shape")
}

func TestFinishedRunReadsAsFinished(t *testing.T) {
	dir := t.TempDir()
	holdout := corpusTarget(t, dir, runSpec{name: "holdout", quirk: 10})
	refs := []Target{
		corpusTarget(t, dir, runSpec{name: "refa", quirk: 10}),
		corpusTarget(t, dir, runSpec{name: "refb", quirk: 10}),
	}
	r := scoreAgainst(t, holdout, refs...)

	t.Logf("final progress %.3f, match rate %.3f, bias %+.3f", r.FinalPred, r.MatchRate, r.Bias)
	assert.GreaterOrEqual(t, r.FinalPred, 0.95,
		"the estimate must reach the end of a run that reached the end")
}
