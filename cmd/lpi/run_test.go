package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/log-progress-indicator/internal/model"
)

// captureExit stubs the osExit seam and returns a pointer to the recorded
// code (-1 when never called).
func captureExit(t *testing.T) *int {
	t.Helper()
	code := -1
	old := osExit
	osExit = func(c int) { code = c }
	t.Cleanup(func() { osExit = old })
	return &code
}

func TestRunPassthroughAndExitZero(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	code := captureExit(t)

	out, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--",
		"/bin/sh", "-c", "echo out-one; echo out-two; echo err-line >&2")
	require.NoError(t, err)
	assert.Equal(t, -1, *code, "exit code 0 must not call osExit")
	assert.Contains(t, out, "out-one\nout-two\n")
	assert.Contains(t, errOut, "err-line")
	assert.Contains(t, errOut, "Progress:")
	assert.Contains(t, errOut, "Confidence:")
}

func TestRunPropagatesExitCode(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	code := captureExit(t)

	_, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--",
		"/bin/sh", "-c", "echo a; echo b; exit 3")
	require.NoError(t, err)
	assert.Equal(t, 3, *code)
	assert.Contains(t, errOut, "Progress:")
}

func TestRunLearnOnSuccess(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	captureExit(t)

	script := "sed -n 1,40p " + demoPartial
	_, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--learn", "--",
		"/bin/sh", "-c", script)
	require.NoError(t, err)
	assert.Contains(t, errOut, "learned run (40 lines,")
	assert.Contains(t, errOut, `into key "demo" (3 runs)`)
	assert.Len(t, loadModel(t, db, "demo").Runs, 3)
	assert.Empty(t, pendingFiles(t, db), "a learned run leaves no capture file behind")
}

func TestRunLearnSkippedOnFailure(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	code := captureExit(t)

	_, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--learn", "--",
		"/bin/sh", "-c", "echo x; echo y; exit 5")
	require.NoError(t, err)
	assert.Equal(t, 5, *code)
	assert.Contains(t, errOut, "exit status 5 -- run not learned")
	assert.Len(t, loadModel(t, db, "demo").Runs, 2, "failed run must not be learned")
}

func TestRunForwardsSignals(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	code := captureExit(t)

	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
	start := time.Now()
	// exec so the signal hits the process actually holding the pipes.
	_, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--",
		"/bin/sh", "-c", "echo waiting; exec sleep 30")
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 10*time.Second, "child must die from the forwarded signal")
	assert.Equal(t, 143, *code, "SIGTERM death maps to 128+15")
	assert.Contains(t, errOut, "Progress:")
}

func TestRunSignalKilledChildNotLearned(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	code := captureExit(t)

	_, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--learn", "--",
		"/bin/sh", "-c", "echo one; echo two; kill -TERM $$")
	require.NoError(t, err)
	assert.Equal(t, 143, *code, "SIGTERM death maps to 128+15")
	assert.Contains(t, errOut, "exit status 143 -- run not learned")
	assert.Len(t, loadModel(t, db, "demo").Runs, 2, "signal-killed run must not be learned")

	files := pendingFiles(t, db)
	require.Len(t, files, 1, "the captured log of a signal-killed run is kept")
	assert.Contains(t, errOut, "captured log kept: "+files[0])
	assert.Contains(t, errOut, "learn it later with: lpi learn --key demo --db "+db+" "+files[0])
}

func TestRunBootstrapsMissingKeyWithLearn(t *testing.T) {
	db := t.TempDir()
	shortTicks(t)
	captureExit(t)
	script := "sed -n 1,40p " + demoPartial

	// First invocation: no model yet -> baseline recording, learned on exit 0.
	_, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "fresh", "--learn", "--",
		"/bin/sh", "-c", script)
	require.NoError(t, err)
	assert.Contains(t, errOut, `no model for key "fresh" yet -- recording baseline run`)
	assert.Contains(t, errOut, "recording baseline  lines")
	assert.Contains(t, errOut, "Reference:   none yet (recording baseline)")
	assert.Contains(t, errOut, `into key "fresh" (1 runs)`)
	assert.Len(t, loadModel(t, db, "fresh").Runs, 1)

	// Second invocation: the recorded baseline is now the reference.
	_, errOut, err = execLpi(t, nil, "run", "--db", db, "--key", "fresh", "--learn", "--",
		"/bin/sh", "-c", script)
	require.NoError(t, err)
	assert.NotContains(t, errOut, "recording baseline")
	assert.Contains(t, errOut, "Progress:")
	assert.Contains(t, errOut, "Units:")
	assert.Contains(t, errOut, `into key "fresh" (2 runs)`)
	assert.Len(t, loadModel(t, db, "fresh").Runs, 2)
}

func TestRunMissingKeyStillErrors(t *testing.T) {
	db := t.TempDir()

	// Without --learn there is nothing to record: a missing key is fatal.
	_, _, err := execLpi(t, nil, "run", "--db", db, "--key", "fresh", "--", "/bin/true")
	require.ErrorContains(t, err, `no model for key "fresh"`)

	// With a --ref the reference is explicit, so a missing --key is fatal
	// even under --learn.
	_, _, err = execLpi(t, nil, "run", "--db", db, "--key", "fresh", "--learn",
		"--ref", demoBuild1, "--", "/bin/true")
	require.ErrorContains(t, err, `no model for key "fresh"`)
}

// TestRunFailureKeepsCaptureAndRescues is the end-to-end rescue: a failed
// learning run keeps its capture file and prints a recovery command; running
// that command verbatim learns the run, cleans up pending/, and the model
// then estimates a subsequent run.
func TestRunFailureKeepsCaptureAndRescues(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	code := captureExit(t)

	script := "sed -n 1,40p " + demoPartial + "; exit 5"
	_, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--learn", "--",
		"/bin/sh", "-c", script)
	require.NoError(t, err)
	assert.Equal(t, 5, *code)
	assert.Contains(t, errOut, "exit status 5 -- run not learned")
	assert.Len(t, loadModel(t, db, "demo").Runs, 2, "the failed run must not be learned")

	files := pendingFiles(t, db)
	require.Len(t, files, 1)
	assert.Contains(t, errOut, "captured log kept: "+files[0])
	assert.Contains(t, errOut, "learn it later with: lpi learn --key demo --db "+db+" "+files[0])

	data, err := os.ReadFile(files[0])
	require.NoError(t, err)
	recs := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Greater(t, len(recs), 40, "header plus one record per consumed line")
	assert.True(t, strings.HasPrefix(recs[0], "#lpi-capture v1"), "capture header: %q", recs[0])
	stamp, _, ok := strings.Cut(recs[1], "\t")
	require.True(t, ok, "records are stamp<TAB>text: %q", recs[1])
	_, err = strconv.ParseInt(stamp, 10, 64)
	require.NoError(t, err, "the stamp is unix nanoseconds: %q", stamp)

	// Run the printed recovery command verbatim (minus the binary name).
	var hinted []string
	for _, line := range strings.Split(errOut, "\n") {
		if rest, found := strings.CutPrefix(line, "learn it later with: lpi "); found {
			hinted = strings.Fields(rest)
			break
		}
	}
	require.NotEmpty(t, hinted, "the recovery command must be printed")
	out, _, err := execLpi(t, nil, hinted...)
	require.NoError(t, err)
	assert.Contains(t, out, "learned "+files[0])
	assert.Contains(t, out, `model "demo": 3 runs,`)
	assert.Contains(t, out, "removed pending capture: "+files[0])
	assert.Empty(t, pendingFiles(t, db), "learning a pending capture removes it")
	assert.Len(t, loadModel(t, db, "demo").Runs, 3)

	// The rescued model estimates a fresh run sanely.
	*code = -1
	_, errOut, err = execLpi(t, nil, "run", "--db", db, "--key", "demo", "--",
		"/bin/sh", "-c", "sed -n 1,40p "+demoPartial)
	require.NoError(t, err)
	assert.Equal(t, -1, *code)
	assert.Contains(t, errOut, "Progress:")
	assert.Contains(t, errOut, "Units:")
}

// TestRunBaselineFailureKeepsCaptureWithTimes is the reported bug: a
// baseline-recording run (no model yet) that fails after producing a long
// log must not throw the log away, and the rescued run must keep its
// per-line times rather than fall back to position mode.
func TestRunBaselineFailureKeepsCaptureWithTimes(t *testing.T) {
	db := t.TempDir()
	shortTicks(t)
	code := captureExit(t)

	script := "sed -n 1,60p " + demoBuild1 + "; exit 2"
	_, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "mybuild", "--learn", "--",
		"/bin/sh", "-c", script)
	require.NoError(t, err)
	assert.Equal(t, 2, *code)
	assert.Contains(t, errOut, `no model for key "mybuild" yet -- recording baseline run`)
	assert.Contains(t, errOut, "recording baseline")
	assert.Contains(t, errOut, "exit status 2 -- run not learned")
	assert.Contains(t, errOut, "captured log kept: ")
	_, err = os.Stat(model.PathForKey(db, "mybuild"))
	assert.True(t, os.IsNotExist(err), "no model may be created for the failed baseline")

	files := pendingFiles(t, db)
	require.Len(t, files, 1)
	_, _, err = execLpi(t, nil, "learn", "--key", "mybuild", "--db", db, files[0])
	require.NoError(t, err)

	m := loadModel(t, db, "mybuild")
	require.Len(t, m.Runs, 1)
	assert.Equal(t, 60, m.Runs[0].Lines)
	assert.True(t, m.Runs[0].HasTimes, "capture replay must preserve per-line times")
	assert.Positive(t, m.Runs[0].Duration)
	assert.True(t, m.HasTimes)
}

func TestRunLearnOnFailure(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	code := captureExit(t)

	script := "sed -n 1,40p " + demoPartial + "; exit 5"
	_, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--learn-on-failure", "--",
		"/bin/sh", "-c", script)
	require.NoError(t, err)
	assert.Equal(t, 5, *code, "the child's exit code still propagates")
	assert.Contains(t, errOut, "learned run (40 lines,")
	assert.Contains(t, errOut, `into key "demo" (3 runs)`)
	assert.NotContains(t, errOut, "run not learned")
	assert.Empty(t, pendingFiles(t, db), "a learned run leaves no capture file behind")
	assert.Len(t, loadModel(t, db, "demo").Runs, 3)
}

func TestRunLearnOnFailureRequiresKey(t *testing.T) {
	_, _, err := execLpi(t, nil, "run", "--db", t.TempDir(), "--learn-on-failure", "--", "/bin/true")
	require.ErrorContains(t, err, "--learn requires --key")
}

func TestRunFailureTooShortKeepsNothing(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	code := captureExit(t)

	_, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--learn", "--",
		"/bin/sh", "-c", "echo lonely; exit 7")
	require.NoError(t, err)
	assert.Equal(t, 7, *code)
	assert.Contains(t, errOut, "exit status 7 -- run not learned")
	assert.NotContains(t, errOut, "captured log kept")
	assert.Empty(t, pendingFiles(t, db), "fewer than 2 nonempty lines is nothing worth recovering")
}

func TestRunLearnTooShortDiscardsCapture(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	captureExit(t)

	_, _, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--learn", "--",
		"/bin/sh", "-c", "echo lonely")
	require.ErrorContains(t, err, "run not learned")
	assert.Empty(t, pendingFiles(t, db))
}

// TestRunLearnSaveFailureKeepsCapture drives the model save itself to fail
// (the key produces a model file name past the OS limit, while the capture
// file's truncated name stays valid): the digested run is lost but the
// capture file survives with recovery instructions.
func TestRunLearnSaveFailureKeepsCapture(t *testing.T) {
	db := t.TempDir()
	shortTicks(t)
	captureExit(t)

	longKey := strings.Repeat("k", 246)
	script := "sed -n 1,20p " + demoBuild1
	_, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", longKey, "--learn", "--",
		"/bin/sh", "-c", script)
	require.Error(t, err, "the model save must fail")
	assert.Contains(t, errOut, "captured log kept: ")
	assert.Contains(t, errOut, "learn it later with: lpi learn --key "+longKey+" --db "+db+" ")
	require.Len(t, pendingFiles(t, db), 1)
}

// TestRunCaptureDisabledWarning proves the recovery feature never breaks
// the primary flow: when the capture file cannot be created, the run warns
// once and proceeds normally.
func TestRunCaptureDisabledWarning(t *testing.T) {
	db := seedDemoModel(t)
	require.NoError(t, os.WriteFile(filepath.Join(db, "pending"), []byte("blocker"), 0o644))
	shortTicks(t)
	code := captureExit(t)

	_, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--learn", "--",
		"/bin/sh", "-c", "echo x; echo y; exit 5")
	require.NoError(t, err)
	assert.Equal(t, 5, *code)
	assert.Contains(t, errOut, "warning: capture file disabled:")
	assert.Contains(t, errOut, "exit status 5 -- run not learned")
	assert.NotContains(t, errOut, "captured log kept")
}

// TestCaptureFileAsRef proves capture files work anywhere a reference log
// does: --ref resolution goes through model.DigestFile, which sniffs the
// capture header.
func TestCaptureFileAsRef(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	captureExit(t)

	script := "sed -n 1,40p " + demoPartial + "; exit 3"
	_, _, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--learn", "--",
		"/bin/sh", "-c", script)
	require.NoError(t, err)
	files := pendingFiles(t, db)
	require.Len(t, files, 1)

	out, _, err := execLpi(t, nil, "analyze", "--ref", files[0], demoPartial)
	require.NoError(t, err)
	assert.Contains(t, out, "Progress:")
	assert.Contains(t, out, "Units:")
}

// glueScript emits interleaved stdout and stderr lines plus mid-line stdout
// pauses, so live status repaints land between and inside child lines.
const glueScript = `i=0
while [ $i -lt 8 ]; do
	i=$((i+1))
	echo "out line $i"
	echo "err line $i" >&2
	printf "hold%d" $i
	sleep 0.03
	echo "-done"
done`

// glueScriptStdout is the exact byte stream glueScript writes to stdout.
func glueScriptStdout() string {
	var b strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&b, "out line %d\nhold%d-done\n", i, i)
	}
	return b.String()
}

// TestRunTTYStatusNeverGluesToChildOutput is the reported bug: on a TTY the
// status line was painted with no trailing newline and passthrough bytes
// were appended straight onto it ("recording baseline  lines 35  elapsed
// 3sLocal build detected..."). The renderer must erase before child bytes,
// repaint after them, and never paint onto a partial child line.
func TestRunTTYStatusNeverGluesToChildOutput(t *testing.T) {
	db := t.TempDir()
	shortTicks(t)
	captureExit(t)
	forceTTY(t, true)

	out, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "fresh", "--learn", "--",
		"/bin/sh", "-c", glueScript)
	require.NoError(t, err)

	assert.Equal(t, glueScriptStdout(), out,
		"stdout passthrough must stay byte-faithful, with no rendering bytes mixed in")

	lines := renderScrollback(errOut)
	assertStatusOwnsLines(t, lines)
	for i := 1; i <= 8; i++ {
		assert.Contains(t, lines, fmt.Sprintf("err line %d", i),
			"child stderr lines must render as whole lines")
	}
}

// TestRunPlainStatusLinesAreWholeLines locks the plain-mode discipline:
// every status print is a complete line, and child output never shares one.
func TestRunPlainStatusLinesAreWholeLines(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t) // PlainInterval = 0: a status line per update
	captureExit(t)
	forceTTY(t, false)

	out, errOut, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "--",
		"/bin/sh", "-c", glueScript)
	require.NoError(t, err)

	assert.Equal(t, glueScriptStdout(), out)
	assert.NotContains(t, errOut, "\x1b", "plain mode must not emit escape sequences")
	assert.NotContains(t, errOut, "\r")
	lines := strings.Split(strings.TrimSuffix(errOut, "\n"), "\n")
	assertStatusOwnsLines(t, lines)
	for i := 1; i <= 8; i++ {
		assert.Contains(t, lines, fmt.Sprintf("err line %d", i),
			"child stderr lines must stay whole lines")
	}
}

func TestRunArgumentValidation(t *testing.T) {
	db := seedDemoModel(t)

	_, _, err := execLpi(t, nil, "run", "--db", db, "--key", "demo", "/bin/true")
	require.ErrorContains(t, err, "missing '--'")

	_, _, err = execLpi(t, nil, "run", "--db", db, "--key", "demo", "stray", "--", "/bin/true")
	require.ErrorContains(t, err, "unexpected argument before '--'")

	_, _, err = execLpi(t, nil, "run", "--db", db, "--key", "demo", "--")
	require.ErrorContains(t, err, "no command given")

	_, _, err = execLpi(t, nil, "run", "--db", db, "--learn", "--", "/bin/true")
	require.ErrorContains(t, err, "--learn requires --key")

	_, _, err = execLpi(t, nil, "run", "--db", db, "--key", "demo", "--",
		"/definitely/not/a/binary")
	require.Error(t, err)
}
