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
