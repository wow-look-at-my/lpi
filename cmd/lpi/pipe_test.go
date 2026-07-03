package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
}

func TestPipeWeirdBytesStayIntact(t *testing.T) {
	db := seedDemoModel(t)
	weird := "plain line\n\x00\x01binary\xff\r\nover\rwrite\nno trailing newline"
	out, _, err := execLpi(t, strings.NewReader(weird), "pipe", "--db", db, "--key", "demo")
	require.NoError(t, err)
	assert.Equal(t, weird, out)
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
}

func TestPipeRequiresReference(t *testing.T) {
	_, _, err := execLpi(t, strings.NewReader("x\n"), "pipe")
	require.ErrorContains(t, err, "no reference given")
}
