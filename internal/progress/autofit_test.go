package progress

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/log-progress-indicator/internal/model"
)

// novelLines returns n distinct all-letter lines guaranteed absent from
// steps() models.
func novelLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("unrelated noise %c%c seen", 'a'+rune(i/26), 'a'+rune(i%26))
	}
	return lines
}

// namedLines returns n distinct lines carrying the given tag word.
func namedLines(tag string, n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("%s work item %c%c", tag, 'a'+rune(i/26), 'a'+rune(i%26))
	}
	return lines
}

// multiRunModel builds a model with `runs` identical position-mode runs.
func multiRunModel(t *testing.T, key string, runs int, lines []string) *model.Model {
	t.Helper()
	m := model.New(key)
	for i := 0; i < runs; i++ {
		d := model.NewDigester(fmt.Sprintf("r%d", i), nil)
		for _, ln := range lines {
			d.Line(ln)
		}
		run, err := d.Finish()
		require.NoError(t, err)
		m.AddRun(run)
	}
	return m
}

func cand(key, label string, m *model.Model) Candidate {
	return Candidate{Key: key, Label: label, Model: m}
}

func observeAll(ch *Chooser, lines []string) {
	for _, ln := range lines {
		ch.Observe(ln, time.Time{})
	}
}

func lockedKey(t *testing.T, ch *Chooser) string {
	t.Helper()
	key, _, ok := ch.Locked()
	require.True(t, ok, "expected the chooser to be locked")
	return key
}

func TestChooserEarlyLockObviousMatch(t *testing.T) {
	lines := steps(20)
	ch := NewChooser([]Candidate{cand("k", "make -j8", plainModel(t, lines))})

	observeAll(ch, lines[:11])
	_, _, ok := ch.Locked()
	assert.False(t, ok, "11 counted lines is below lockMinLines")
	s := ch.Snapshot()
	assert.True(t, s.Identifying, "pre-lock snapshots carry Identifying")
	assert.Empty(t, s.Label)
	assert.Equal(t, 11, s.CurrentLines, "line counts come from the null estimator")
	assert.Zero(t, s.UnitsTotal, "pre-lock snapshots come from the null estimator")

	ch.Observe(lines[11], time.Time{})
	key, label, ok := ch.Locked()
	require.True(t, ok, "rate 1.0 at lockMinLines locks early")
	assert.Equal(t, "k", key)
	assert.Equal(t, "make -j8", label)

	s = ch.Snapshot()
	assert.False(t, s.Identifying, "Identifying clears once locked")
	assert.Equal(t, "make -j8", s.Label)
	assert.Equal(t, 12, s.UnitsDone, "the locked estimator has full history")
	assert.Equal(t, 20, s.UnitsTotal)

	bk, rate, ok := ch.Best()
	require.True(t, ok)
	assert.Equal(t, "k", bk)
	assert.InDelta(t, 1.0, rate, 1e-9)
}

func TestChooserStandardWindowLock(t *testing.T) {
	mLines := steps(30)
	noise := novelLines(11)
	var feed []string
	for i := 0; len(feed) < 33; i++ {
		feed = append(feed, mLines[2*i], mLines[2*i+1], noise[i])
	}
	ch := NewChooser([]Candidate{cand("k", "k", plainModel(t, mLines))})

	// Rate holds ~2/3 throughout: below earlyLockRate, above lockRate.
	observeAll(ch, feed[:31])
	_, _, ok := ch.Locked()
	assert.False(t, ok, "a moderate rate must wait for the decision window")
	assert.True(t, ch.Snapshot().Identifying)

	ch.Observe(feed[31], time.Time{})
	assert.Equal(t, "k", lockedKey(t, ch), "rate 22/32 locks at the window")
	assert.InDelta(t, 22.0/32, ch.FinalRate("k"), 1e-9)

	key, _, ok := ch.MergeTarget()
	require.True(t, ok, "22/32 clears mergeRate")
	assert.Equal(t, "k", key)
}

func TestChooserLateLockAfterNovelPreamble(t *testing.T) {
	mLines := steps(45)
	ch := NewChooser([]Candidate{cand("k", "k", plainModel(t, mLines))})

	observeAll(ch, novelLines(40))
	_, _, ok := ch.Locked()
	assert.False(t, ok, "40 novel lines cannot lock anything")
	assert.True(t, ch.Snapshot().Identifying)

	// Cumulative rate k/(40+k) crosses lockRate at the 40th matching line;
	// the decision keeps re-running after the window, so the lock lands
	// exactly there.
	observeAll(ch, mLines[:39])
	_, _, ok = ch.Locked()
	assert.False(t, ok, "39/79 is still below lockRate")

	ch.Observe(mLines[39], time.Time{})
	assert.Equal(t, "k", lockedKey(t, ch), "40/80 reaches lockRate late")
	s := ch.Snapshot()
	assert.Equal(t, 40, s.UnitsDone, "the late lock's display is exact from line 1")
	assert.Equal(t, 40, s.NovelLines)
}

func TestChooserNeverLocksOnAllNovel(t *testing.T) {
	ch := NewChooser([]Candidate{cand("k", "k", plainModel(t, steps(15)))})
	observeAll(ch, novelLines(100))
	_, _, ok := ch.Locked()
	assert.False(t, ok)
	assert.True(t, ch.Snapshot().Identifying)
	_, rate, ok := ch.Best()
	require.True(t, ok)
	assert.Zero(t, rate)
	_, _, ok = ch.MergeTarget()
	assert.False(t, ok)
}

func TestChooserSwitchHysteresis(t *testing.T) {
	aLines := namedLines("alpha", 20)
	bLines := namedLines("beta", 30)
	ch := NewChooser([]Candidate{
		cand("a", "a", plainModel(t, aLines)),
		cand("b", "b", plainModel(t, bLines)),
	})

	observeAll(ch, aLines[:18])
	assert.Equal(t, "a", lockedKey(t, ch))

	// 22 rival lines: b = 22/40 = 0.55, a = 18/40 = 0.45 -- a lead of
	// exactly +0.10 stays below switchMargin and must NOT steal the lock.
	observeAll(ch, bLines[:22])
	assert.Equal(t, "a", lockedKey(t, ch), "+0.10 is inside the hysteresis margin")

	// 5 more: b = 27/45 = 0.60, a = 18/45 = 0.40 -- a lead of +0.20
	// clears the margin (and lockRate), so the rival takes the lock.
	observeAll(ch, bLines[22:27])
	assert.Equal(t, "b", lockedKey(t, ch), "+0.20 steals the lock")
	assert.Equal(t, "b", ch.Snapshot().Label, "the display follows the switch")
}

func TestChooserRivalBelowLockRateCannotSteal(t *testing.T) {
	aLines := namedLines("alpha", 15)
	bLines := namedLines("beta", 40)
	noise := novelLines(24)
	ch := NewChooser([]Candidate{
		cand("a", "a", plainModel(t, aLines)),
		cand("b", "b", plainModel(t, bLines)),
	})

	observeAll(ch, aLines[:12])
	assert.Equal(t, "a", lockedKey(t, ch))

	// Interleave noise and rival lines: at counted 60 the rival sits at
	// 24/60 = 0.40 vs the incumbent's 12/60 = 0.20. The margin is beaten,
	// but a rival below lockRate must never take the lock.
	for i := 0; i < 24; i++ {
		ch.Observe(noise[i], time.Time{})
		ch.Observe(bLines[i], time.Time{})
	}
	assert.Equal(t, "a", lockedKey(t, ch), "a sub-lockRate rival cannot steal")

	// Pure rival lines push it over lockRate: 36/72 = 0.5 -> steal.
	observeAll(ch, bLines[24:39])
	assert.Equal(t, "b", lockedKey(t, ch))
}

func TestChooserTieBreakDeterminism(t *testing.T) {
	lines := steps(12)

	t.Run("more runs wins over smaller key", func(t *testing.T) {
		ch := NewChooser([]Candidate{
			cand("aaa", "aaa", multiRunModel(t, "aaa", 1, lines)),
			cand("zzz", "zzz", multiRunModel(t, "zzz", 2, lines)),
		})
		ch.Observe("completely unmatched line", time.Time{})
		key, rate, ok := ch.Best()
		require.True(t, ok)
		assert.Zero(t, rate)
		assert.Equal(t, "zzz", key, "equal rate and weight: more runs wins")
	})

	t.Run("smaller key breaks the final tie", func(t *testing.T) {
		for _, order := range [][2]string{{"bbb", "aaa"}, {"aaa", "bbb"}} {
			ch := NewChooser([]Candidate{
				cand(order[0], order[0], multiRunModel(t, order[0], 1, lines)),
				cand(order[1], order[1], multiRunModel(t, order[1], 1, lines)),
			})
			observeAll(ch, lines)
			assert.Equal(t, "aaa", lockedKey(t, ch), "candidate order %v", order)
		}
	})

	t.Run("higher matched weight wins over smaller key", func(t *testing.T) {
		ch := NewChooser([]Candidate{
			cand("aaa", "aaa", plainModel(t, steps(24))),
			cand("wide", "wide", plainModel(t, lines)),
		})
		observeAll(ch, lines)
		// Both rates are 1.0; the 12-line model's matched weight is 1.0 vs
		// roughly half for the 24-line superset.
		assert.Equal(t, "wide", lockedKey(t, ch))
	})
}

func TestChooserZeroCandidates(t *testing.T) {
	ch := NewChooser(nil)
	base := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	ch.Observe("first line here", base)
	ch.Observe("second line here", base.Add(time.Second))
	ch.Tick(base.Add(5 * time.Second))

	s := ch.Snapshot()
	assert.False(t, s.Identifying, "no candidates renders as plain baseline recording")
	assert.Empty(t, s.Label)
	assert.Zero(t, s.UnitsTotal)
	assert.Equal(t, 2, s.CurrentLines)
	require.True(t, s.ElapsedKnown)
	assert.Equal(t, 5*time.Second, s.Elapsed)

	_, _, ok := ch.Locked()
	assert.False(t, ok)
	_, _, ok = ch.Best()
	assert.False(t, ok)
	_, _, ok = ch.MergeTarget()
	assert.False(t, ok)
	assert.Zero(t, ch.FinalRate("anything"))
}

func TestChooserBestNeedsCountedLines(t *testing.T) {
	ch := NewChooser([]Candidate{cand("k", "k", plainModel(t, steps(3)))})
	_, _, ok := ch.Best()
	assert.False(t, ok, "no counted lines yet")
	ch.Observe("   ", time.Time{})
	_, _, ok = ch.Best()
	assert.False(t, ok, "empty-normalized lines do not count")
}

func TestChooserMergeTargetRequiresMergeRate(t *testing.T) {
	mLines := steps(30)
	noise := novelLines(16)
	ch := NewChooser([]Candidate{cand("k", "lbl", plainModel(t, mLines))})

	// Alternating match/noise holds the rate at exactly 0.5: locks at the
	// window, but display-lock confidence is not merge confidence.
	for i := 0; i < 16; i++ {
		ch.Observe(mLines[i], time.Time{})
		ch.Observe(noise[i], time.Time{})
	}
	assert.Equal(t, "k", lockedKey(t, ch), "0.5 locks at the window")
	_, _, ok := ch.MergeTarget()
	assert.False(t, ok, "0.5 is below mergeRate")

	observeAll(ch, mLines[16:26])
	key, label, ok := ch.MergeTarget()
	require.True(t, ok, "26/42 clears mergeRate")
	assert.Equal(t, "k", key)
	assert.Equal(t, "lbl", label)
	assert.InDelta(t, 26.0/42, ch.FinalRate("k"), 1e-9)
}
