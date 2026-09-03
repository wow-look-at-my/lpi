package render

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forceTTY pins IsTTY to a fixed answer for the
func forceTTY(t *testing.T, tty bool) {
	t.Helper()
	old := IsTTY
	IsTTY = func(io.Writer) bool { return tty }
	t.Cleanup(func() { IsTTY = old })
}

func TestRendererTTYRepaintsInPlace(t *testing.T) {
	t.Serial()
	forceTTY(t, true)
	var buf bytes.Buffer
	r := New(&buf)
	s := fullSnap()
	r.Update(s)
	r.Update(s)
	r.Close(s)
	line := StatusLine(s)
	want := "\r\x1b[K" + line + "\r\x1b[K" + line + "\r\x1b[K" + line + "\n"
	assert.Equal(t, want, buf.String())
}

func TestRendererPlainThrottles(t *testing.T) {
	t.Serial()
	forceTTY(t, false)
	old := PlainInterval
	PlainInterval = time.Hour
	t.Cleanup(func() { PlainInterval = old })

	var buf bytes.Buffer
	r := New(&buf)
	s := fullSnap()
	r.Update(s) // update always prints
	r.Update(s) // suppressed: same percent, interval not elapsed
	s2 := s
	s2.Progress = 0.41
	r.Update(s2) // whole percent changed: prints
	r.Close(s2)  // always prints
	want := StatusLine(s) + "\n" + StatusLine(s2) + "\n" + StatusLine(s2) + "\n"
	assert.Equal(t, want, buf.String())
}

func TestRendererPlainIntervalElapsed(t *testing.T) {
	t.Serial()
	forceTTY(t, false)
	old := PlainInterval
	PlainInterval = 0 // every update qualifies
	t.Cleanup(func() { PlainInterval = old })

	var buf bytes.Buffer
	r := New(&buf)
	s := fullSnap()
	r.Update(s)
	r.Update(s)
	assert.Equal(t, StatusLine(s)+"\n"+StatusLine(s)+"\n", buf.String())
}

func TestPassthroughTTYEraseAndRepaint(t *testing.T) {
	t.Serial()
	forceTTY(t, true)
	var buf bytes.Buffer
	r := New(&buf)
	pt := r.Passthrough(&buf, &sync.Mutex{})
	s := fullSnap()
	line := StatusLine(s)

	// Child bytes before any status: forwarded
	_, err := pt.Write([]byte("early\n"))
	require.NoError(t, err)
	assert.Equal(t, "early\n", buf.String())
	buf.Reset()

	r.Update(s)
	assert.Equal(t, "\r\x1b[K"+line, buf.String())
	buf.Reset()

	// A complete child line: erase child bytes
	_, err = pt.Write([]byte("child\n"))
	require.NoError(t, err)
	assert.Equal(t, "\r\x1b[K"+"child\n"+"\r\x1b[K"+line, buf.String())
	buf.Reset()

	// A partial child line: the status stays down
	_, err = pt.Write([]byte("part"))
	require.NoError(t, err)
	assert.Equal(t, "\r\x1b[K"+"part", buf.String())
	buf.Reset()

	// The next paint starts on a fresh line instead of
	r.Update(s)
	assert.Equal(t, "\n"+"\r\x1b[K"+line, buf.String())
	buf.Reset()

	// The child line's remainder lands at column of an
	_, err = pt.Write([]byte("rest\n"))
	require.NoError(t, err)
	assert.Equal(t, "\r\x1b[K"+"rest\n"+"\r\x1b[K"+line, buf.String())
	buf.Reset()

	r.Close(s)
	assert.Equal(t, "\r\x1b[K"+line+"\n", buf.String())
}

func TestPassthroughTTYCloseAfterPartialLine(t *testing.T) {
	t.Serial()
	forceTTY(t, true)
	var buf bytes.Buffer
	r := New(&buf)
	pt := r.Passthrough(&buf, &sync.Mutex{})
	s := fullSnap()

	r.Update(s)
	_, err := pt.Write([]byte("no trailing newline"))
	require.NoError(t, err)
	buf.Reset()

	// Close must not paint the final status onto the
	r.Close(s)
	assert.Equal(t, "\n"+"\r\x1b[K"+StatusLine(s)+"\n", buf.String())
}

func TestPassthroughPlainStatusOwnsWholeLines(t *testing.T) {
	t.Serial()
	forceTTY(t, false)
	old := PlainInterval
	PlainInterval = 0 // every update qualifies
	t.Cleanup(func() { PlainInterval = old })

	var buf bytes.Buffer
	r := New(&buf)
	pt := r.Passthrough(&buf, &sync.Mutex{})
	s := fullSnap()
	line := StatusLine(s)

	r.Update(s)
	assert.Equal(t, line+"\n", buf.String(), "plain status prints end with a newline")
	buf.Reset()

	// Complete child lines pass through with no
	_, err := pt.Write([]byte("child\n"))
	require.NoError(t, err)
	assert.Equal(t, "child\n", buf.String())
	buf.Reset()

	// A partial child line is terminated before the
	_, err = pt.Write([]byte("part"))
	require.NoError(t, err)
	r.Update(s)
	assert.Equal(t, "part"+"\n"+line+"\n", buf.String())
	buf.Reset()

	r.Close(s)
	assert.Equal(t, line+"\n", buf.String(), "Close ends with a newline in plain mode")
}

func TestMessageTTY(t *testing.T) {
	t.Serial()
	forceTTY(t, true)
	var buf bytes.Buffer
	r := New(&buf)
	pt := r.Passthrough(&buf, &sync.Mutex{})
	s := fullSnap()
	line := StatusLine(s)

	// Nothing painted yet: the message is just its own
	r.Message("warning: early")
	assert.Equal(t, "warning: early\n", buf.String())
	buf.Reset()

	// A painted status is erased the message owns its
	r.Update(s)
	buf.Reset()
	r.Message("warning: capture file disabled: boom")
	assert.Equal(t, "\r\x1b[K"+"warning: capture file disabled: boom\n", buf.String())
	buf.Reset()

	// The next Update repaints on the fresh line left
	r.Update(s)
	assert.Equal(t, "\r\x1b[K"+line, buf.String())
	buf.Reset()

	// A complete passthrough line after a message also
	r.Message("notice")
	buf.Reset()
	_, err := pt.Write([]byte("child\n"))
	require.NoError(t, err)
	assert.Equal(t, "child\n"+"\r\x1b[K"+line, buf.String())
}

func TestMessageTTYAfterPartialChildLine(t *testing.T) {
	t.Serial()
	forceTTY(t, true)
	var buf bytes.Buffer
	r := New(&buf)
	pt := r.Passthrough(&buf, &sync.Mutex{})
	s := fullSnap()

	r.Update(s)
	_, err := pt.Write([]byte("partial"))
	require.NoError(t, err)
	buf.Reset()

	// The partial child line is terminated before the
	r.Message("interrupted -- run not learned")
	assert.Equal(t, "\n"+"interrupted -- run not learned\n", buf.String())
}

func TestMessagePlain(t *testing.T) {
	t.Serial()
	forceTTY(t, false)
	old := PlainInterval
	PlainInterval = 0 // every update qualifies
	t.Cleanup(func() { PlainInterval = old })

	var buf bytes.Buffer
	r := New(&buf)
	pt := r.Passthrough(&buf, &sync.Mutex{})
	s := fullSnap()
	line := StatusLine(s)

	// Messages are whole lines between whole status
	r.Update(s)
	r.Message("warning: capture file disabled: boom")
	r.Update(s)
	assert.Equal(t, line+"\n"+"warning: capture file disabled: boom\n"+line+"\n", buf.String())
	buf.Reset()

	// A partial child line is terminated before the
	_, err := pt.Write([]byte("part"))
	require.NoError(t, err)
	r.Message("notice")
	assert.Equal(t, "part"+"\n"+"notice\n", buf.String())
}

func TestBreakTTY(t *testing.T) {
	t.Serial()
	forceTTY(t, true)
	var buf bytes.Buffer
	r := New(&buf)
	pt := r.Passthrough(&buf, &sync.Mutex{})
	s := fullSnap()

	// Nothing in progress: Break emits nothing
	r.Break()
	assert.Empty(t, buf.String())

	// A painted status is committed by the newline, not
	r.Update(s)
	buf.Reset()
	r.Break()
	assert.Equal(t, "\n", buf.String())
	buf.Reset()

	// After Break a completed passthrough line does not
	_, err := pt.Write([]byte("child\n"))
	require.NoError(t, err)
	assert.Equal(t, "child\n", buf.String())
	buf.Reset()

	// A partial child line is terminated too
	r.Update(s)
	_, err = pt.Write([]byte("part"))
	require.NoError(t, err)
	buf.Reset()
	r.Break()
	assert.Equal(t, "\n", buf.String())
}

func TestBreakPlain(t *testing.T) {
	t.Serial()
	forceTTY(t, false)
	old := PlainInterval
	PlainInterval = 0
	t.Cleanup(func() { PlainInterval = old })

	var buf bytes.Buffer
	r := New(&buf)
	pt := r.Passthrough(&buf, &sync.Mutex{})
	s := fullSnap()

	// Plain statuses already own whole lines: Break
	r.Update(s)
	buf.Reset()
	r.Break()
	assert.Empty(t, buf.String())

	// Only a partial child line needs terminating
	_, err := pt.Write([]byte("part"))
	require.NoError(t, err)
	buf.Reset()
	r.Break()
	assert.Equal(t, "\n", buf.String())
}

func TestPassthroughSeparateTTYStreams(t *testing.T) {
	t.Serial()
	forceTTY(t, true) // both the status stream and dst count as TTYs
	var status, out bytes.Buffer
	r := New(&status)
	pt := r.Passthrough(&out, &sync.Mutex{})
	s := fullSnap()
	line := StatusLine(s)

	r.Update(s)
	_, err := pt.Write([]byte("stdout line\n"))
	require.NoError(t, err)
	_, err = pt.Write([]byte("partial"))
	require.NoError(t, err)
	r.Update(s)

	assert.Equal(t, "stdout line\npartial", out.String(),
		"child bytes reach their own stream byte-for-byte")
	assert.Equal(t, "\r\x1b[K"+line+"\r\x1b[K"+"\r\x1b[K"+line+"\r\x1b[K"+"\n"+"\r\x1b[K"+line,
		status.String(), "erase and repaint stay on the renderer's stream")
}

func TestPassthroughUncoordinatedIsUnwrapped(t *testing.T) {
	t.Serial()
	forceTTY(t, false)
	var status, out bytes.Buffer
	r := New(&status)
	assert.Same(t, &out, r.Passthrough(&out, &sync.Mutex{}),
		"a non-TTY foreign stream needs no coordination")
	assert.NotSame(t, &status, r.Passthrough(&status, &sync.Mutex{}),
		"the renderer's own stream is always coordinated")
}

// funcWriter is an uncomparable io.Writer used to
type funcWriter func([]byte) (int, error)

func (f funcWriter) Write(p []byte) (int, error) { return f(p) }

func TestPassthroughUncomparableWriter(t *testing.T) {
	t.Serial()
	forceTTY(t, false)
	w := funcWriter(func(p []byte) (int, error) { return len(p), nil })
	r := New(w)
	assert.NotPanics(t, func() { r.Passthrough(w, &sync.Mutex{}) })
}

func TestIsTTYDefault(t *testing.T) {
	t.Serial()
	assert.False(t, IsTTY(&bytes.Buffer{}))

	f, err := os.CreateTemp(t.TempDir(), "plain")
	require.NoError(t, err)
	defer f.Close()
	assert.False(t, IsTTY(f), "regular file is not a TTY")

	null, err := os.Open(os.DevNull)
	require.NoError(t, err)
	assert.True(t, IsTTY(null), "/dev/null is a character device")
	require.NoError(t, null.Close())
	assert.False(t, IsTTY(null), "Stat on a closed file fails")
}
