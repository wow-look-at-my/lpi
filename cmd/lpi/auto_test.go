package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// autoLines returns n distinct all-letter log lines (digits would collapse
// under normalization and merge the fingerprints).
func autoLines(tag string, n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("%s unit %c%c of project", tag, 'a'+rune(i/26), 'a'+rune(i%26))
	}
	return lines
}

// printfScript builds a shell script printing the given lines.
func printfScript(lines []string) string {
	return "printf '" + strings.Join(lines, "\\n") + "\\n'"
}

// autoModelKeys lists the auto.* model keys stored in db, sorted by ReadDir.
func autoModelKeys(t *testing.T, db string) []string {
	t.Helper()
	entries, err := os.ReadDir(db)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var keys []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".lpi"); ok && !e.IsDir() &&
			strings.HasPrefix(name, "auto.") {
			keys = append(keys, name)
		}
	}
	return keys
}

func TestAutoRecordsNewPattern(t *testing.T) {
	db := t.TempDir()
	shortTicks(t)
	captureExit(t)

	script := printfScript(autoLines("compile", 15))
	_, errOut, err := execLpi(t, nil, "auto", "--db", db, "--", "/bin/sh", "-c", script)
	require.NoError(t, err)
	assert.Contains(t, errOut, "recorded new pattern")
	assert.Contains(t, errOut, "future runs with this output shape will show live progress")
	assert.Contains(t, errOut, "recording baseline", "an empty db has nothing to identify against")

	keys := autoModelKeys(t, db)
	require.Len(t, keys, 1)
	assert.Regexp(t, `^auto\.[0-9a-f]{16}$`, keys[0])
	m := loadModel(t, db, keys[0])
	assert.Len(t, m.Runs, 1)
	assert.Equal(t, []string{"/bin/sh -c " + script}, m.Invocations)
	assert.Empty(t, pendingFiles(t, db), "a learned run leaves no capture file behind")
}

func TestAutoSecondRunRecognizesAndRefines(t *testing.T) {
	db := t.TempDir()
	shortTicks(t)
	captureExit(t)

	script := printfScript(autoLines("compile", 15))
	_, _, err := execLpi(t, nil, "auto", "--db", db, "--", "/bin/sh", "-c", script)
	require.NoError(t, err)
	keys := autoModelKeys(t, db)
	require.Len(t, keys, 1)

	_, errOut, err := execLpi(t, nil, "auto", "--db", db, "--", "/bin/sh", "-c", script)
	require.NoError(t, err)
	assert.Contains(t, errOut, "identifying pattern",
		"the first status print lands before the lock")
	assert.Contains(t, errOut, "learned run")
	assert.Contains(t, errOut, `into key "`+keys[0]+`" (2 runs)`)
	assert.Contains(t, errOut, "Pattern:", "the final summary names the pattern")
	assert.Contains(t, errOut, "ref /bin/sh -c printf",
		"the post-lock status line carries the pattern label")

	m := loadModel(t, db, keys[0])
	assert.Len(t, m.Runs, 2)
	assert.Len(t, m.Invocations, 1, "the identical command line is deduped")
	assert.Len(t, autoModelKeys(t, db), 1, "no second pattern may appear")
	assert.Empty(t, pendingFiles(t, db))
}

func TestAutoCrossCommandFit(t *testing.T) {
	db := t.TempDir()
	shortTicks(t)
	captureExit(t)

	script := printfScript(autoLines("compile", 15))
	_, _, err := execLpi(t, nil, "auto", "--db", db, "--", "/bin/sh", "-c", script)
	require.NoError(t, err)
	keys := autoModelKeys(t, db)
	require.Len(t, keys, 1)

	// A different command line producing the same output is the same
	// pattern: identity is the output, the command is only a label.
	other := "true; " + script
	_, errOut, err := execLpi(t, nil, "auto", "--db", db, "--", "/bin/sh", "-c", other)
	require.NoError(t, err)
	assert.Contains(t, errOut, `into key "`+keys[0]+`" (2 runs)`)
	assert.Len(t, autoModelKeys(t, db), 1)

	m := loadModel(t, db, keys[0])
	assert.Equal(t, []string{"/bin/sh -c " + other, "/bin/sh -c " + script}, m.Invocations,
		"both command lines are recorded, most recent first")
}

func TestAutoNovelOutputRecordsSeparatePattern(t *testing.T) {
	db := t.TempDir()
	shortTicks(t)
	captureExit(t)

	_, _, err := execLpi(t, nil, "auto", "--db", db, "--",
		"/bin/sh", "-c", printfScript(autoLines("compile", 15)))
	require.NoError(t, err)
	first := autoModelKeys(t, db)
	require.Len(t, first, 1)

	_, errOut, err := execLpi(t, nil, "auto", "--db", db, "--",
		"/bin/sh", "-c", printfScript(autoLines("deploy", 15)))
	require.NoError(t, err)
	assert.Contains(t, errOut, "recorded new pattern")

	keys := autoModelKeys(t, db)
	assert.Len(t, keys, 2, "novel output records a second pattern")
	assert.Len(t, loadModel(t, db, first[0]).Runs, 1, "the existing pattern is untouched")
}

func TestAutoShortRunMergesByContentHash(t *testing.T) {
	db := t.TempDir()
	shortTicks(t)
	captureExit(t)

	// 5 lines can never reach lockMinLines, so the second run cannot lock;
	// the content-derived id still lands it on the recorded pattern.
	script := printfScript(autoLines("tiny", 5))
	_, _, err := execLpi(t, nil, "auto", "--db", db, "--", "/bin/sh", "-c", script)
	require.NoError(t, err)
	keys := autoModelKeys(t, db)
	require.Len(t, keys, 1)

	_, errOut, err := execLpi(t, nil, "auto", "--db", db, "--", "/bin/sh", "-c", script)
	require.NoError(t, err)
	assert.Contains(t, errOut, `into key "`+keys[0]+`" (2 runs)`)
	assert.Len(t, autoModelKeys(t, db), 1)
}

func TestAutoFailureKeepsCapture(t *testing.T) {
	db := t.TempDir()
	shortTicks(t)
	code := captureExit(t)

	_, errOut, err := execLpi(t, nil, "auto", "--db", db, "--",
		"/bin/sh", "-c", "echo x; echo y; echo z; exit 3")
	require.NoError(t, err)
	assert.Equal(t, 3, *code, "the child's exit code propagates")
	assert.Contains(t, errOut, "exit status 3 -- run not learned")

	files := pendingFiles(t, db)
	require.Len(t, files, 1, "the captured log of a failed run is kept")
	assert.Contains(t, errOut, "captured log kept: "+files[0])
	assert.Contains(t, errOut, "learn it later with: lpi learn --key auto.",
		"the recovery key is the run's content id")
	assert.Empty(t, autoModelKeys(t, db), "a failed run must not create a model")
}

func TestAutoLocksAndMergesUserKey(t *testing.T) {
	db := seedDemoModel(t)
	shortTicks(t)
	captureExit(t)
	abs, err := filepath.Abs(demoBuild2)
	require.NoError(t, err)

	_, errOut, err := execLpi(t, nil, "auto", "--db", db, "--", "cat", abs)
	require.NoError(t, err)
	assert.Contains(t, errOut, "ref demo", "the fit locks the stored user key")
	assert.Contains(t, errOut, `into key "demo" (3 runs)`)
	assert.Contains(t, errOut, "Pattern:     demo")

	m := loadModel(t, db, "demo")
	assert.Len(t, m.Runs, 3, "the run merges into the fitted pattern")
	assert.Equal(t, []string{"cat " + abs}, m.Invocations,
		"the merged pattern gains the command line as its label")
	assert.Empty(t, autoModelKeys(t, db), "no auto pattern may be recorded on a merge")
}

func TestAutoCleanShortRunIsNotAnError(t *testing.T) {
	// A clean child exit with <2 nonempty lines has nothing to learn, but
	// wrapping a quick command must never turn its success into a failure:
	// a one-line notice, no error, no usage dump, exit code stays 0.
	tests := []struct {
		name string
		args []string
	}{
		{"no output", []string{"true"}},
		{"one line", []string{"/bin/sh", "-c", "echo hi"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := t.TempDir()
			shortTicks(t)
			code := captureExit(t)

			args := append([]string{"auto", "--db", db, "--"}, tt.args...)
			_, errOut, err := execLpi(t, nil, args...)
			require.NoError(t, err)
			assert.Equal(t, -1, *code, "a clean short run must not call osExit")
			assert.Contains(t, errOut, "nothing to learn -- fewer than 2 nonempty output lines")
			assert.NotContains(t, errOut, "Usage:")
			assert.NotContains(t, errOut, "Error:")
			assert.Empty(t, autoModelKeys(t, db), "nothing to learn records no pattern")
			assert.Empty(t, pendingFiles(t, db), "fewer than 2 nonempty lines is nothing worth recovering")
		})
	}
}

func TestAutoSkipsCorruptModels(t *testing.T) {
	db := t.TempDir()
	shortTicks(t)
	captureExit(t)
	require.NoError(t, os.WriteFile(filepath.Join(db, "corrupt.lpi"), []byte("junk"), 0o644))

	_, errOut, err := execLpi(t, nil, "auto", "--db", db, "--",
		"/bin/sh", "-c", printfScript(autoLines("compile", 15)))
	require.NoError(t, err)
	assert.Contains(t, errOut, "warning: skipping model corrupt.lpi")
	assert.Contains(t, errOut, "recorded new pattern",
		"a corrupt model must not break the magic path")
}

func TestAutoTransportErrorKeepsNothingWhenEmpty(t *testing.T) {
	db := t.TempDir()
	shortTicks(t)
	captureExit(t)

	_, _, err := execLpi(t, nil, "auto", "--db", db, "--", "/definitely/not/a/binary")
	require.Error(t, err)
	assert.Empty(t, pendingFiles(t, db), "nothing was captured, nothing is kept")
}

func TestAutoArgumentValidation(t *testing.T) {
	db := t.TempDir()

	_, _, err := execLpi(t, nil, "auto", "--db", db, "/bin/true")
	require.ErrorContains(t, err, "missing '--'")

	_, _, err = execLpi(t, nil, "auto", "--db", db, "stray", "--", "/bin/true")
	require.ErrorContains(t, err, "unexpected argument before '--'")

	_, _, err = execLpi(t, nil, "auto", "--db", db, "--")
	require.ErrorContains(t, err, "no command given")

	// pflag's '--' position is sticky across executions; the validation
	// must hold on a repeat run (resetCommand re-inits it).
	_, _, err = execLpi(t, nil, "auto", "--db", db, "/bin/true")
	require.ErrorContains(t, err, "missing '--'")
}
