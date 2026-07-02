package tailer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testInterval = 5 * time.Millisecond

// start runs a Tailer on its own goroutine and returns its output channel,
// a cancel func, and the channel Run's error will arrive on.
func start(t *testing.T, tl *Tailer) (chan string, context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	lines := make(chan string, 128)
	errc := make(chan error, 1)
	go func() { errc <- tl.Run(ctx, lines) }()
	return lines, cancel, errc
}

func wantLine(t *testing.T, lines chan string, want string) {
	t.Helper()
	select {
	case got, ok := <-lines:
		require.True(t, ok, "lines channel closed while waiting for %q", want)
		assert.Equal(t, want, got)
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for line %q", want)
	}
}

func wantQuiet(t *testing.T, lines chan string, d time.Duration) {
	t.Helper()
	select {
	case got, ok := <-lines:
		if ok {
			t.Fatalf("unexpected line %q", got)
		}
		t.Fatal("lines channel closed unexpectedly")
	case <-time.After(d):
	}
}

func wantDone(t *testing.T, errc chan error) error {
	t.Helper()
	select {
	case err := <-errc:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return")
		return nil
	}
}

func append_(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(s)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestFromStartAndAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	append_(t, path, "one\ntwo\r\n")
	lines, cancel, errc := start(t, &Tailer{Path: path, FromStart: true, Interval: testInterval})
	wantLine(t, lines, "one")
	wantLine(t, lines, "two") // trailing \r stripped
	append_(t, path, "three\n")
	wantLine(t, lines, "three")
	cancel()
	assert.NoError(t, wantDone(t, errc))
	_, ok := <-lines
	assert.False(t, ok, "channel should be closed after Run returns")
}

func TestPartialLineHeldUntilNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	append_(t, path, "start\npart")
	lines, _, _ := start(t, &Tailer{Path: path, FromStart: true, Interval: testInterval})
	wantLine(t, lines, "start")
	wantQuiet(t, lines, 20*testInterval)
	append_(t, path, "ial\n")
	wantLine(t, lines, "partial")
}

func TestSkipExistingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	append_(t, path, "old\n")
	lines, _, _ := start(t, &Tailer{Path: path, FromStart: false, Interval: testInterval})
	time.Sleep(20 * testInterval) // let the tailer open and seek to the end
	append_(t, path, "new\n")
	wantLine(t, lines, "new")
}

func TestTruncateReopensFromZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	append_(t, path, "aa\nbb\n")
	lines, _, _ := start(t, &Tailer{Path: path, FromStart: true, Interval: testInterval})
	wantLine(t, lines, "aa")
	wantLine(t, lines, "bb")
	require.NoError(t, os.Truncate(path, 0))
	append_(t, path, "cc\n")
	wantLine(t, lines, "cc")
}

func TestRotationRenameAndRecreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	append_(t, path, "old\n")
	lines, _, _ := start(t, &Tailer{Path: path, FromStart: true, Interval: testInterval})
	wantLine(t, lines, "old")
	require.NoError(t, os.Rename(path, path+".1"))
	time.Sleep(5 * testInterval) // let the tailer notice the file is gone
	append_(t, path, "fresh\n")
	wantLine(t, lines, "fresh")
}

func TestRotationImmediateRecreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	append_(t, path, "before\n")
	lines, _, _ := start(t, &Tailer{Path: path, FromStart: true, Interval: testInterval})
	wantLine(t, lines, "before")
	require.NoError(t, os.Rename(path, path+".1"))
	append_(t, path, "after\n") // new inode at the old path
	wantLine(t, lines, "after")
}

func TestFileAppearsLate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "late.log")
	lines, _, _ := start(t, &Tailer{Path: path, FromStart: false, Interval: testInterval})
	time.Sleep(5 * testInterval)
	// The file did not exist at start, so all of its content is new even
	// with FromStart=false.
	append_(t, path, "hello\nworld\n")
	wantLine(t, lines, "hello")
	wantLine(t, lines, "world")
}

func TestCancelBeforeFileExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never.log")
	lines, cancel, errc := start(t, &Tailer{Path: path, Interval: testInterval})
	time.Sleep(3 * testInterval)
	cancel()
	assert.NoError(t, wantDone(t, errc))
	_, ok := <-lines
	assert.False(t, ok)
}

func TestHardErrorOnDirectory(t *testing.T) {
	dir := t.TempDir()
	_, _, errc := start(t, &Tailer{Path: dir, Interval: testInterval})
	assert.Error(t, wantDone(t, errc))
}

func TestDefaultInterval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	append_(t, path, "hi\n")
	lines, _, _ := start(t, &Tailer{Path: path, FromStart: true}) // Interval zero
	wantLine(t, lines, "hi")
}
