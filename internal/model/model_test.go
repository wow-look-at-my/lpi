package model

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/lpi/internal/fingerprint"
)

func expectOf(t *testing.T, m *Model, line string) []Occurrence {
	t.Helper()
	occs, ok := m.Expect[fingerprint.Fingerprint(line)]
	require.True(t, ok, "line %q not in Expect", line)
	return occs
}

func weightTotal(m *Model) float64 {
	var total float64
	for _, occs := range m.Expect {
		for _, o := range occs {
			total += float64(o.WeightFrac)
		}
	}
	return total
}

func TestModelSingleRun(t *testing.T) {
	m := New("build")
	m.AddRun(digestPlain(t, "r1", []string{"alpha", "beta", "gamma"}))
	assert.Equal(t, "build", m.Key)
	assert.Equal(t, 3, m.TotalUnits)
	assert.False(t, m.HasTimes)
	assert.Zero(t, m.RefDuration)
	assert.Len(t, m.Expect, 3)
	assert.InDelta(t, 1.0, weightTotal(m), 1e-3)
}

func TestModelUpperMedianTwoRuns(t *testing.T) {
	m := New("k")
	m.AddRun(digestPlain(t, "r1", []string{"alpha", "beta"}))
	m.AddRun(digestPlain(t, "r2", []string{"alpha", "beta", "beta"}))

	assert.Len(t, expectOf(t, m, "alpha"), 1)
	assert.Len(t, expectOf(t, m, "beta"), 2, "2 runs take the max count")
	assert.Equal(t, 3, m.TotalUnits)
	assert.InDelta(t, 1.0, weightTotal(m), 1e-3)
}

func TestModelUpperMedianThreeRuns(t *testing.T) {
	m := New("k")
	m.AddRun(digestPlain(t, "r1", []string{"alpha", "beta"}))
	m.AddRun(digestPlain(t, "r2", []string{"alpha", "beta", "beta"}))
	m.AddRun(digestPlain(t, "r3", []string{"alpha", "beta", "beta", "beta", "beta"}))

	assert.Len(t, expectOf(t, m, "beta"), 2, "counts [1 2 4] take the middle")
	assert.Equal(t, 3, m.TotalUnits)
}

func TestModelFingerprintAbsentFromOneOfTwoRunsKept(t *testing.T) {
	m := New("k")
	m.AddRun(digestPlain(t, "r1", []string{"alpha", "beta"}))
	m.AddRun(digestPlain(t, "r2", []string{"alpha", "gamma"}))

	assert.Len(t, expectOf(t, m, "beta"), 1, "counts [0 1] keep the line")
	assert.Len(t, expectOf(t, m, "gamma"), 1)
	assert.Equal(t, 3, m.TotalUnits)
}

func TestModelFingerprintInOneOfThreeRunsDropped(t *testing.T) {
	m := New("k")
	m.AddRun(digestPlain(t, "r1", []string{"alpha", "beta"}))
	m.AddRun(digestPlain(t, "r2", []string{"alpha", "beta"}))
	m.AddRun(digestPlain(t, "r3", []string{"alpha", "beta", "stray"}))

	_, ok := m.Expect[fingerprint.Fingerprint("stray")]
	assert.False(t, ok, "counts [0 0 1] drop the line")
	assert.Equal(t, 2, m.TotalUnits)
}

func TestModelDisjointRunsYieldEmptyExpectation(t *testing.T) {
	m := New("k")
	m.AddRun(digestPlain(t, "r1", []string{"alpha", "beta"}))
	m.AddRun(digestPlain(t, "r2", []string{"gamma", "delta"}))
	m.AddRun(digestPlain(t, "r3", []string{"epsilon", "zeta"}))

	assert.Empty(t, m.Expect)
	assert.Zero(t, m.TotalUnits)
}

func TestModelMergeAveragesFractions(t *testing.T) {
	lines := []string{"alpha", "beta", "gamma"}
	m := New("k")
	m.AddRun(digestAt(t, "r1", lines, []time.Duration{0, 40 * time.Second, 100 * time.Second}))
	m.AddRun(digestAt(t, "r2", lines, []time.Duration{0, 60 * time.Second, 100 * time.Second}))

	beta := expectOf(t, m, "beta")
	require.Len(t, beta, 1)
	assert.InDelta(t, 0.5, float64(beta[0].TimeFrac), 1e-3, "mean of 0.4 and 0.6")
	assert.InDelta(t, 0.5, float64(beta[0].WeightFrac), 1e-3)
	assert.InDelta(t, 1.0, weightTotal(m), 1e-3)
	assert.True(t, m.HasTimes)
	assert.Equal(t, 100*time.Second, m.RefDuration)
}

func TestModelWeightsRenormalizedAcrossDifferingRuns(t *testing.T) {
	m := New("k")
	m.AddRun(digestPlain(t, "r1", []string{"alpha", "beta"}))
	m.AddRun(digestPlain(t, "r2", []string{"alpha", "beta", "beta", "gamma"}))
	assert.InDelta(t, 1.0, weightTotal(m), 1e-3)
}

func TestModelRefDurationUpperMedian(t *testing.T) {
	lines := []string{"alpha", "beta"}
	m := New("k")
	m.AddRun(digestAt(t, "fast", lines, []time.Duration{0, 100 * time.Second}))
	m.AddRun(digestAt(t, "slow", lines, []time.Duration{0, 200 * time.Second}))
	assert.Equal(t, 200*time.Second, m.RefDuration, "upper median of two durations is the max")

	m.AddRun(digestPlain(t, "untimed", lines))
	assert.Equal(t, 200*time.Second, m.RefDuration, "untimed runs do not vote on duration")
	assert.True(t, m.HasTimes)
}

func TestModelFIFOEviction(t *testing.T) {
	m := New("k")
	for i := 0; i < MaxRuns+1; i++ {
		m.AddRun(digestPlain(t, fmt.Sprintf("r%d", i), []string{"alpha", "beta"}))
	}
	require.Len(t, m.Runs, MaxRuns)
	assert.Equal(t, "r1", m.Runs[0].Source, "oldest run evicted first")
	assert.Equal(t, fmt.Sprintf("r%d", MaxRuns), m.Runs[MaxRuns-1].Source)
}

func TestModelRebuildEmpty(t *testing.T) {
	m := New("k")
	m.Rebuild()
	assert.Empty(t, m.Expect)
	assert.Zero(t, m.TotalUnits)
	assert.Zero(t, m.RefDuration)
	assert.False(t, m.HasTimes)
}

func TestAddInvocation(t *testing.T) {
	m := New("k")

	m.AddInvocation("")
	assert.Empty(t, m.Invocations, "empty invocations are ignored")

	m.AddInvocation("make -j8")
	m.AddInvocation("make")
	assert.Equal(t, []string{"make", "make -j8"}, m.Invocations, "most recent first")

	m.AddInvocation("make -j8")
	assert.Equal(t, []string{"make -j8", "make"}, m.Invocations,
		"a duplicate moves to the front instead of repeating")

	for _, cmd := range []string{"a", "b", "c", "d", "e", "f"} {
		m.AddInvocation(cmd)
	}
	assert.Equal(t, []string{"f", "e", "d", "c", "b"}, m.Invocations,
		"the list is capped at MaxInvocations, oldest dropped")
	assert.Len(t, m.Invocations, MaxInvocations)
}

func TestDisplayLabel(t *testing.T) {
	m := New("mykey")
	assert.Equal(t, "mykey", m.DisplayLabel(), "no invocations falls back to the key")
	m.AddInvocation("cmake --build build")
	m.AddInvocation("make -j8")
	assert.Equal(t, "make -j8", m.DisplayLabel(), "the most recent invocation wins")
}

func TestAutoKey(t *testing.T) {
	lines := []string{"alpha", "beta", "beta", "gamma"}
	r1 := digestPlain(t, "r1", lines)
	r2 := digestPlain(t, "r2", lines)

	key := AutoKey(r1)
	assert.Regexp(t, `^auto\.[0-9a-f]{16}$`, key)
	assert.Equal(t, key, AutoKey(r2),
		"the id depends only on the fingerprint multiset, not on source or times")

	timed := digestAt(t, "r3", lines,
		[]time.Duration{0, time.Second, 2 * time.Second, 9 * time.Second})
	assert.Equal(t, key, AutoKey(timed), "timing does not participate in identity")

	assert.NotEqual(t, key, AutoKey(digestPlain(t, "r4", []string{"alpha", "beta", "gamma"})),
		"a different occurrence count is a different pattern")
	assert.NotEqual(t, key, AutoKey(digestPlain(t, "r5", []string{"alpha", "beta", "beta", "delta"})),
		"a different fingerprint is a different pattern")

	// The auto
	assert.Equal(t, key, sanitizeKey(key))
	assert.Equal(t, filepath.Join("/db", key+".lpi"), PathForKey("/db", key))
}
