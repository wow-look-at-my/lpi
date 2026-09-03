package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/log-progress-indicator/internal/render"
)

// shortTicks shrinks the live-mode timing seams for a test.
func shortTicks(t *testing.T) {
	t.Helper()
	oldTick, oldPlain := tickInterval, render.PlainInterval
	tickInterval = 20 * time.Millisecond
	render.PlainInterval = 0
	t.Cleanup(func() { tickInterval, render.PlainInterval = oldTick, oldPlain })
}

// cancelWatchAfter replaces the signal context with cancelled after d.
func cancelWatchAfter(t *testing.T, d time.Duration) {
	t.Helper()
	old := newSignalContext
	newSignalContext = func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(d)
			cancel()
		}()
		return ctx, cancel
	}
	t.Cleanup(func() { newSignalContext = old })
}

func TestWatchTimestampedFile(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	cancelWatchAfter(t, 400*time.Millisecond)

	path := filepath.Join(t.TempDir(), "live.log")
	data, err := os.ReadFile(demoPartial)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
	go func() {
		time.Sleep(150 * time.Millisecond)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintln(f, "11:18:07 [ 58%] Building C object src/net/CMakeFiles/net.dir/zip.c.o")
	}()

	out, errOut, err := execLpi(t, nil, "watch", "--db", db, "--key", "demo",
		"--interval", "5ms", "--json-stream", path)
	require.NoError(t, err)

	// Live status lines and the final summary land on stderr.
	assert.Contains(t, errOut, "units ")
	assert.Contains(t, errOut, "Progress:")
	assert.Contains(t, errOut, "Confidence:  high")
	// The file's own timestamps are the time source: elapsed comes from the
	// log clock, minutes long, not from the milliseconds of wall time.
	assert.Contains(t, errOut, "Elapsed:     3m")

	// NDJSON snapshots on stdout, per repaint, monotonically increasing.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.NotEmpty(t, lines)
	first := parseJSONLine(t, lines[0])
	last := parseJSONLine(t, lines[len(lines)-1])
	assert.GreaterOrEqual(t, last["progress"].(float64), first["progress"].(float64))
	assert.Greater(t, last["progress"].(float64), 0.3)
	assert.Equal(t, "high", last["confidence"])
}

// TestWatchJSONStreamTTYKeepsStreamsClean covers watch's own out-of-band
// stream during active rendering: with both stdout and stderr on a TTY, the
// NDJSON snapshots are coordinated with the status line (erase before,
// repaint after), so the JSON stream stays pure and no terminal line mixes
// status text with a snapshot.
func TestWatchJSONStreamTTYKeepsStreamsClean(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	forceTTY(t, true)
	cancelWatchAfter(t, 300*time.Millisecond)

	path := filepath.Join(t.TempDir(), "live.log")
	data, err := os.ReadFile(demoPartial)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	out, errOut, err := execLpi(t, nil, "watch", "--db", db, "--key", "demo",
		"--interval", "5ms", "--json-stream", path)
	require.NoError(t, err)

	assert.NotContains(t, out, "\x1b", "rendering bytes must never leak into the NDJSON stream")
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parseJSONLine(t, line)
	}
	assertStatusOwnsLines(t, renderScrollback(errOut))
}

func TestWatchWallClockMode(t *testing.T) {
	shortTicks(t)
	cancelWatchAfter(t, 300*time.Millisecond)

	// A reference and a live file with no timestamps at all: watch commits
	// to wall-clock mode and drives Tick.
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.log")
	var b strings.Builder
	for i := 0; i < 320; i++ {
		fmt.Fprintf(&b, "processing item %d of 320\ncompacting shard %d\n", i, i)
	}
	require.NoError(t, os.WriteFile(ref, []byte(b.String()), 0o644))
	live := filepath.Join(dir, "live.log")
	require.NoError(t, os.WriteFile(live, []byte(b.String()), 0o644)) // >= lines pre-existing

	_, errOut, err := execLpi(t, nil, "watch", "--ref", ref, "--interval", "5ms", live)
	require.NoError(t, err)
	assert.Contains(t, errOut, "Progress:    100.0% (by line position)")
	assert.Contains(t, errOut, "Elapsed:")
	assert.Contains(t, errOut, "no timing data")
}

func TestWatchFromStartFalseSkipsHistory(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	cancelWatchAfter(t, 250*time.Millisecond)

	path := filepath.Join(t.TempDir(), "live.log")
	data, err := os.ReadFile(demoPartial)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	_, errOut, err := execLpi(t, nil, "watch", "--db", db, "--key", "demo",
		"--interval", "5ms", "--from-start=false", path)
	require.NoError(t, err)
	assert.Contains(t, errOut, "units 0/", "history must be skipped")
}

func TestWatchHardError(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	_, _, err := execLpi(t, nil, "watch", "--db", db, "--key", "demo", "--interval", "5ms", t.TempDir())
	require.Error(t, err)
}

func TestWatchNoReference(t *testing.T) {
	_, _, err := execLpi(t, nil, "watch", "somefile.log")
	require.ErrorContains(t, err, "no reference given")
}
