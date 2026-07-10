package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/log-progress-indicator/internal/model"
)

func TestPipePassthroughAndLearn(t *testing.T) {
	db := seedDemoModel(t)
	data, err := os.ReadFile(demoPartial)
	require.NoError(t, err)

	out, errOut, err := execLpi(t, bytes.NewReader(data), "pipe",
		"--db", db, "--key", "demo", "--learn-key", "captured")
	require.NoError(t, err)

	assert.Equal(t, string(data), out, "stdout must be a byte-faithful passthrough")
	assert.Contains(t, errOut, "Progress:")
	assert.Contains(t, errOut, `learned run (66 lines,`)
	assert.Contains(t, errOut, `into key "captured" (1 runs)`)

	m := loadModel(t, db, "captured")
	assert.Len(t, m.Runs, 1)
	assert.Equal(t, 66, m.Runs[0].Lines)
	assert.Empty(t, pendingFiles(t, db), "a learned stream leaves no capture file behind")
}

func TestPipeWeirdBytesStayIntact(t *testing.T) {
	db := seedDemoModel(t)
	weird := "plain line\n\x00\x01binary\xff\r\nover\rwrite\nno trailing newline"
	out, _, err := execLpi(t, strings.NewReader(weird), "pipe", "--db", db, "--key", "demo")
	require.NoError(t, err)
	assert.Equal(t, weird, out)
}

// TestPipeTTYStatusNeverGluesToPassthrough covers the TTY half of the
// glued-status bug for pipe: stdout must stay byte-faithful (no rendering
// escapes leaking in) and the stderr status line must never share a
// rendered terminal line with forwarded text, even when the input ends with
// a partial line.
func TestPipeTTYStatusNeverGluesToPassthrough(t *testing.T) {
	db := seedDemoModel(t)
	forceTTY(t, true)

	input := "alpha compile line\nbeta compile line\ngamma partial"
	out, errOut, err := execLpi(t, strings.NewReader(input), "pipe", "--db", db, "--key", "demo")
	require.NoError(t, err)

	assert.Equal(t, input, out, "stdout must stay byte-faithful under TTY rendering")
	assertStatusOwnsLines(t, renderScrollback(errOut))
}

// TestPipePlainStatusLinesAreWholeLines locks the plain-mode discipline for
// pipe: complete status lines only, no escape sequences.
func TestPipePlainStatusLinesAreWholeLines(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t) // PlainInterval = 0: a status line per update
	forceTTY(t, false)

	input := "alpha compile line\nbeta compile line\n"
	out, errOut, err := execLpi(t, strings.NewReader(input), "pipe", "--db", db, "--key", "demo")
	require.NoError(t, err)

	assert.Equal(t, input, out)
	assert.NotContains(t, errOut, "\x1b", "plain mode must not emit escape sequences")
	assert.NotContains(t, errOut, "\r")
	assertStatusOwnsLines(t, strings.Split(strings.TrimSuffix(errOut, "\n"), "\n"))
}

func TestPipeJSONStream(t *testing.T) {
	db := seedDemoModel(t)
	data, err := os.ReadFile(demoPartial)
	require.NoError(t, err)

	out, errOut, err := execLpi(t, bytes.NewReader(data), "pipe",
		"--db", db, "--key", "demo", "--json-stream")
	require.NoError(t, err)
	assert.Equal(t, string(data), out, "NDJSON must not leak into the passthrough")

	lines := strings.Split(errOut, "\n")
	require.NotEmpty(t, lines)
	snap := parseJSONLine(t, lines[0])
	assert.Contains(t, snap, "progress")
	assert.Contains(t, errOut, "Progress:", "final summary still printed")
}

func TestPipeBootstrapsLearnKey(t *testing.T) {
	db := t.TempDir()
	data, err := os.ReadFile(demoPartial)
	require.NoError(t, err)

	// First invocation: --learn-key alone, no model yet -> baseline recording.
	out, errOut, err := execLpi(t, bytes.NewReader(data), "pipe",
		"--db", db, "--learn-key", "fresh")
	require.NoError(t, err)
	assert.Equal(t, string(data), out, "passthrough must survive baseline recording")
	assert.Contains(t, errOut, `no model for key "fresh" yet -- recording baseline run`)
	assert.Contains(t, errOut, "recording baseline  lines")
	assert.Contains(t, errOut, "Reference:   none yet (recording baseline)")
	assert.Contains(t, errOut, `into key "fresh" (1 runs)`)

	// Second invocation: --learn-key doubles as the reference key.
	_, errOut, err = execLpi(t, bytes.NewReader(data), "pipe",
		"--db", db, "--learn-key", "fresh")
	require.NoError(t, err)
	assert.NotContains(t, errOut, "recording baseline")
	assert.Contains(t, errOut, "Progress:")
	assert.Contains(t, errOut, `into key "fresh" (2 runs)`)
	assert.Len(t, loadModel(t, db, "fresh").Runs, 2)
}

func TestPipeBootstrapExplicitKeyMatchingLearnKey(t *testing.T) {
	db := t.TempDir()
	_, errOut, err := execLpi(t, strings.NewReader("alpha line\nbeta line\n"), "pipe",
		"--db", db, "--key", "fresh", "--learn-key", "fresh")
	require.NoError(t, err)
	assert.Contains(t, errOut, `no model for key "fresh" yet -- recording baseline run`)
	assert.Contains(t, errOut, "recording baseline")
	assert.Len(t, loadModel(t, db, "fresh").Runs, 1)
}

func TestPipeBootstrapJSONStreamStaysFinite(t *testing.T) {
	db := t.TempDir()
	_, errOut, err := execLpi(t, strings.NewReader("alpha line\nbeta line\n"), "pipe",
		"--db", db, "--learn-key", "fresh", "--json-stream")
	require.NoError(t, err)

	var snaps int
	for _, line := range strings.Split(errOut, "\n") {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		snaps++
		snap := parseJSONLine(t, line)
		assert.Equal(t, 0.0, snap["progress"])
		assert.Equal(t, 0.0, snap["units_total"])
		assert.Equal(t, "none", snap["confidence"])
		assert.Equal(t, "none", snap["eta_kind"])
	}
	assert.Equal(t, 3, snaps, "one snapshot per line plus the final one")
}

func TestPipeMissingForeignKeyStillErrors(t *testing.T) {
	// A --key naming a different key than --learn-key gets no bootstrap.
	_, _, err := execLpi(t, strings.NewReader("x\n"), "pipe",
		"--db", t.TempDir(), "--key", "other", "--learn-key", "fresh")
	require.ErrorContains(t, err, `no model for key "other"`)
}

func TestPipeLearnTooShort(t *testing.T) {
	db := seedDemoModel(t)
	_, _, err := execLpi(t, strings.NewReader("only one line\n"), "pipe",
		"--db", db, "--key", "demo", "--learn-key", "captured")
	require.ErrorContains(t, err, "run not learned")
	assert.Empty(t, pendingFiles(t, db), "fewer than 2 nonempty lines is nothing worth recovering")
}

// errAfterReader yields its data, then fails with err instead of EOF.
type errAfterReader struct {
	data []byte
	err  error
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestPipeScannerErrorKeepsCapture(t *testing.T) {
	db := seedDemoModel(t)
	boom := errors.New("stdin exploded")
	stdin := &errAfterReader{data: []byte("alpha line\nbeta line\n"), err: boom}

	_, errOut, err := execLpi(t, stdin, "pipe", "--db", db, "--key", "demo", "--learn-key", "captured")
	require.ErrorIs(t, err, boom)
	assert.NotContains(t, errOut, "learned run")

	files := pendingFiles(t, db)
	require.Len(t, files, 1, "the capture must survive a read error")
	assert.Contains(t, errOut, "captured log kept: "+files[0])
	assert.Contains(t, errOut, "learn it later with: lpi learn --key captured --db "+db+" "+files[0])
	_, err = model.Load(model.PathForKey(db, "captured"))
	assert.True(t, os.IsNotExist(err), "the interrupted stream must not be learned")
}

// TestPipeInterruptKeepsCaptureAndSkipsLearn covers the Ctrl-C race: the
// same signal that kills the upstream would EOF stdin and trigger the
// unconditional EOF-learn of a truncated stream. The handler must win: keep
// the capture, report, and exit 130 without learning. It runs on a forced
// TTY, so it also proves the interrupt notice and recovery lines fire
// through the renderer mid-render: the painted status is erased and each
// message owns a whole terminal line instead of gluing onto the status.
func TestPipeInterruptKeepsCaptureAndSkipsLearn(t *testing.T) {
	db := seedDemoModel(t)
	code := captureExit(t)
	forceTTY(t, true)

	// Keep the process alive if the signal beats pipe's handler
	// registration (the runtime disables the default death while any
	// channel is notified).
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGINT)
	defer signal.Stop(guard)

	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("alpha line\nbeta line\n"))
		time.Sleep(400 * time.Millisecond) // let pipe arm its handler
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		time.Sleep(300 * time.Millisecond) // let the handler run before EOF
		_ = pw.Close()
	}()

	_, errOut, err := execLpi(t, pr, "pipe", "--db", db, "--key", "demo", "--learn-key", "captured")
	require.NoError(t, err)
	assert.Equal(t, 130, *code, "SIGINT maps to 128+2")
	assert.Contains(t, errOut, "interrupted -- run not learned")

	files := pendingFiles(t, db)
	require.Len(t, files, 1, "the capture must survive the interrupt")
	assert.Contains(t, errOut, "captured log kept: "+files[0])
	assert.Contains(t, errOut, "learn it later with: lpi learn --key captured --db "+db+" "+files[0])
	_, err = model.Load(model.PathForKey(db, "captured"))
	assert.True(t, os.IsNotExist(err), "a truncated stream must never be learned")

	// The terminal scrollback is exactly the three messages: the transient
	// status was erased before the notice, and no line mixes the two.
	assert.Equal(t, []string{
		"interrupted -- run not learned",
		"captured log kept: " + files[0],
		"learn it later with: lpi learn --key captured --db " + db + " " + files[0],
	}, renderScrollback(errOut))
}

// TestPipeScannerErrorRecoveryOwnsLinesOnTTY drives a mid-stream read error
// while a TTY status is painted: rendering is abandoned with the status
// committed on its own line, and the recovery instructions each own a whole
// line instead of gluing onto the painted status.
func TestPipeScannerErrorRecoveryOwnsLinesOnTTY(t *testing.T) {
	db := seedDemoModel(t)
	forceTTY(t, true)

	boom := errors.New("stdin exploded")
	stdin := &errAfterReader{data: []byte("alpha line\nbeta line\n"), err: boom}
	_, errOut, err := execLpi(t, stdin, "pipe", "--db", db, "--key", "demo", "--learn-key", "captured")
	require.ErrorIs(t, err, boom)

	files := pendingFiles(t, db)
	require.Len(t, files, 1, "the capture must survive a read error")
	lines := renderScrollback(errOut)
	assertStatusOwnsLines(t, lines)
	assert.Contains(t, lines, "captured log kept: "+files[0])
	assert.Contains(t, lines, "learn it later with: lpi learn --key captured --db "+db+" "+files[0])
}

// TestPipeLearnSaveFailureKeepsCapture drives the save to fail (model file
// name past the OS limit) and checks the capture survives with hints.
func TestPipeLearnSaveFailureKeepsCapture(t *testing.T) {
	db := t.TempDir()
	longKey := strings.Repeat("p", 246)
	_, errOut, err := execLpi(t, strings.NewReader("alpha line\nbeta line\n"), "pipe",
		"--db", db, "--learn-key", longKey)
	require.Error(t, err, "the model save must fail")
	files := pendingFiles(t, db)
	require.Len(t, files, 1)
	assert.Contains(t, errOut, "captured log kept: "+files[0])
}

func TestPipeRequiresReference(t *testing.T) {
	_, _, err := execLpi(t, strings.NewReader("x\n"), "pipe")
	require.ErrorContains(t, err, "no reference given")
}
