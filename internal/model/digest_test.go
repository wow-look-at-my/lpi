package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/log-progress-indicator/internal/fingerprint"
	"github.com/wow-look-at-my/log-progress-indicator/internal/timeparse"
)

var digestBase = time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)

// digestAt builds a timed run from parallel
func digestAt(t *testing.T, source string, lines []string, offsets []time.Duration) *Run {
	t.Helper()
	require.Equal(t, len(lines), len(offsets))
	d := NewDigester(source, nil)
	for i, ln := range lines {
		d.LineAt(ln, digestBase.Add(offsets[i]))
	}
	run, err := d.Finish()
	require.NoError(t, err)
	return run
}

// digestPlain builds a position-mode run
func digestPlain(t *testing.T, source string, lines []string) *Run {
	t.Helper()
	d := NewDigester(source, nil)
	for _, ln := range lines {
		d.Line(ln)
	}
	run, err := d.Finish()
	require.NoError(t, err)
	return run
}

// clockFormat obtains a bare-time format through
func clockFormat(t *testing.T) *timeparse.Format {
	t.Helper()
	f := timeparse.Detect([]string{
		"10:00:00 a", "10:00:01 b", "10:00:02 c", "10:00:03 d", "10:00:04 e",
	})
	require.NotNil(t, f)
	require.Equal(t, "clock", f.Name())
	return f
}

func occOf(t *testing.T, r *Run, line string) []Occurrence {
	t.Helper()
	occs, ok := r.Occ[fingerprint.Fingerprint(line)]
	require.True(t, ok, "line %q not digested", line)
	return occs
}

func TestDigestPositionMode(t *testing.T) {
	lines := []string{"alpha start", "beta compile", "gamma link", "delta done"}
	run := digestPlain(t, "ref", lines)
	assert.Equal(t, "ref", run.Source)
	assert.Equal(t, 4, run.Lines)
	assert.False(t, run.HasTimes)
	assert.Zero(t, run.Duration)

	wantTF := []float64{0, 1.0 / 3, 2.0 / 3, 1}
	wantWF := []float64{0, 1.0 / 3, 1.0 / 3, 1.0 / 3}
	var sum float64
	for i, ln := range lines {
		occs := occOf(t, run, ln)
		require.Len(t, occs, 1)
		assert.InDelta(t, wantTF[i], float64(occs[0].TimeFrac), 1e-6, "TimeFrac of %q", ln)
		assert.InDelta(t, wantWF[i], float64(occs[0].WeightFrac), 1e-6, "WeightFrac of %q", ln)
		sum += float64(occs[0].WeightFrac)
	}
	assert.InDelta(t, 1.0, sum, 1e-6)
}

func TestDigestTimedGapOwnership(t *testing.T) {
	lines := []string{"alpha start", "beta compile", "gamma link", "delta done"}
	offsets := []time.Duration{0, 10 * time.Second, 40 * time.Second, 100 * time.Second}
	run := digestAt(t, "timed", lines, offsets)
	assert.True(t, run.HasTimes)
	assert.Equal(t, 100*time.Second, run.Duration)

	wantTF := []float64{0, 0.1, 0.4, 1.0}
	wantWF := []float64{0, 0.1, 0.3, 0.6}
	for i, ln := range lines {
		occs := occOf(t, run, ln)
		require.Len(t, occs, 1)
		assert.InDelta(t, wantTF[i], float64(occs[0].TimeFrac), 1e-6, "TimeFrac of %q", ln)
		assert.InDelta(t, wantWF[i], float64(occs[0].WeightFrac), 1e-6, "WeightFrac of %q", ln)
	}
}

func TestDigestRepeatedLinesKeepOrder(t *testing.T) {
	lines := []string{"work unit", "work unit", "work unit"}
	offsets := []time.Duration{0, 20 * time.Second, 100 * time.Second}
	run := digestAt(t, "rep", lines, offsets)
	occs := occOf(t, run, "work unit")
	require.Len(t, occs, 3)
	assert.InDelta(t, 0.0, float64(occs[0].WeightFrac), 1e-6)
	assert.InDelta(t, 0.2, float64(occs[1].WeightFrac), 1e-6)
	assert.InDelta(t, 0.8, float64(occs[2].WeightFrac), 1e-6)
	assert.InDelta(t, 0.2, float64(occs[1].TimeFrac), 1e-6)
}

func TestDigestFormatCarryForward(t *testing.T) {
	d := NewDigester("carry", clockFormat(t))
	d.Line("10:00:00 alpha")
	d.Line("no stamp on this beta line")
	d.Line("10:00:40 gamma")
	d.Line("10:01:40 delta")
	run, err := d.Finish()
	require.NoError(t, err)
	assert.True(t, run.HasTimes)
	assert.Equal(t, 100*time.Second, run.Duration)

	beta := occOf(t, run, "no stamp on this beta line")
	require.Len(t, beta, 1)
	assert.Zero(t, beta[0].WeightFrac, "carried line owns no gap")
	assert.Zero(t, beta[0].TimeFrac)

	gamma := occOf(t, run, "10:00:40 gamma")
	require.Len(t, gamma, 1)
	assert.InDelta(t, 0.4, float64(gamma[0].WeightFrac), 1e-6, "gap spans the carried line")
}

func TestDigestNegativeGapClamped(t *testing.T) {
	lines := []string{"alpha a", "beta b", "gamma c", "delta d"}
	offsets := []time.Duration{0, 30 * time.Second, 20 * time.Second, 100 * time.Second}
	run := digestAt(t, "clamp", lines, offsets)
	assert.Equal(t, 100*time.Second, run.Duration)

	gamma := occOf(t, run, "gamma c")
	assert.Zero(t, gamma[0].WeightFrac, "backwards time owns no gap")
	assert.InDelta(t, 0.3, float64(gamma[0].TimeFrac), 1e-6, "clamped to previous time")
	delta := occOf(t, run, "delta d")
	assert.InDelta(t, 0.7, float64(delta[0].WeightFrac), 1e-6)
}

func TestDigestLinesBeforeFirstTimestampPinned(t *testing.T) {
	d := NewDigester("pin", clockFormat(t))
	d.Line("preamble banner alpha")
	d.Line("10:00:00 beta")
	d.Line("10:01:40 gamma")
	run, err := d.Finish()
	require.NoError(t, err)
	assert.Equal(t, 100*time.Second, run.Duration)

	pre := occOf(t, run, "preamble banner alpha")
	assert.Zero(t, pre[0].TimeFrac)
	assert.Zero(t, pre[0].WeightFrac)
	gamma := occOf(t, run, "10:01:40 gamma")
	assert.InDelta(t, 1.0, float64(gamma[0].WeightFrac), 1e-6)
}

func TestDigestSkipsEmptyNormalizedLines(t *testing.T) {
	d := NewDigester("skip", nil)
	d.Line("alpha one")
	d.Line("")
	d.Line("   \t ")
	d.Line("\x1b[2K\x1b[1G") // ANSI-only
	d.Line("beta two")
	run, err := d.Finish()
	require.NoError(t, err)
	assert.Equal(t, 2, run.Lines)
}

func TestDigestZeroTimeLineAtCarries(t *testing.T) {
	d := NewDigester("zero", nil)
	d.LineAt("alpha one", digestBase)
	d.LineAt("beta two", time.Time{}) // unknown time: carried
	d.LineAt("gamma three", digestBase.Add(100*time.Second))
	run, err := d.Finish()
	require.NoError(t, err)
	assert.True(t, run.HasTimes)
	beta := occOf(t, run, "beta two")
	assert.Zero(t, beta[0].TimeFrac)
	assert.Zero(t, beta[0].WeightFrac)
}

func TestDigestFallsBackToPositionMode(t *testing.T) {
	t.Run("format given but nothing parses", func(t *testing.T) {
		d := NewDigester("nofmt", clockFormat(t))
		d.Line("alpha one")
		d.Line("beta two")
		d.Line("gamma three")
		run, err := d.Finish()
		require.NoError(t, err)
		assert.False(t, run.HasTimes)
		assert.Zero(t, run.Duration)
		gamma := occOf(t, run, "gamma three")
		assert.InDelta(t, 1.0, float64(gamma[0].TimeFrac), 1e-6)
	})
	t.Run("all timestamps identical", func(t *testing.T) {
		d := NewDigester("flat", nil)
		d.LineAt("alpha one", digestBase)
		d.LineAt("beta two", digestBase)
		run, err := d.Finish()
		require.NoError(t, err)
		assert.False(t, run.HasTimes)
		beta := occOf(t, run, "beta two")
		assert.InDelta(t, 1.0, float64(beta[0].WeightFrac), 1e-6)
	})
}

func TestDigestFinishTooShort(t *testing.T) {
	d := NewDigester("short", nil)
	_, err := d.Finish()
	assert.Error(t, err)

	d = NewDigester("short", nil)
	d.Line("only line")
	_, err = d.Finish()
	assert.Error(t, err)
}

func TestDigestReader(t *testing.T) {
	run, err := DigestReader(strings.NewReader("alpha one\nbeta two\ngamma three\n"), "r", nil)
	require.NoError(t, err)
	assert.Equal(t, 3, run.Lines)
	assert.Equal(t, "r", run.Source)
}

type errReader struct{ err error }

func (r *errReader) Read([]byte) (int, error) { return 0, r.err }

func TestDigestReaderError(t *testing.T) {
	boom := errors.New("boom")
	_, err := DigestReader(&errReader{err: boom}, "r", nil)
	assert.ErrorIs(t, err, boom)
}
