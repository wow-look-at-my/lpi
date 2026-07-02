package main

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Equal(t, 1, *code, "signal death maps to exit code 1")
	assert.Contains(t, errOut, "Progress:")
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
