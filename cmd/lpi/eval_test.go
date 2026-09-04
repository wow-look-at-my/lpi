package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalScoresTheDemoBuilds(t *testing.T) {
	t.Serial()
	out, _, err := execLpi(t, nil, "eval", demoBuild1, demoBuild2)
	require.NoError(t, err)

	assert.Contains(t, out, "build1.log")
	assert.Contains(t, out, "build2.log")
	assert.Contains(t, out, "verdict: ")
	assert.NotContains(t, out, "scored against itself",
		"each build is scored against the other one")
	assert.NotContains(t, out, "learned", "eval writes nothing without --learn")
}

func TestEvalDetailPrintsCheckpoints(t *testing.T) {
	t.Serial()
	out, _, err := execLpi(t, nil, "eval", "--detail", demoBuild1, demoBuild2)
	require.NoError(t, err)
	assert.Contains(t, out, "what lpi said as the run went by")
	assert.Contains(t, out, "true left")
}

func TestEvalJSONReportsEveryRun(t *testing.T) {
	t.Serial()
	out, _, err := execLpi(t, nil, "eval", "--json", demoBuild1, demoBuild2)
	require.NoError(t, err)

	doc := parseJSONLine(t, strings.TrimSpace(out))
	runs, ok := doc["runs"].([]any)
	require.True(t, ok)
	assert.Len(t, runs, 2)
	assert.Contains(t, doc, "err_mean")
	assert.Contains(t, doc, "grade")
	assert.Contains(t, doc["verdict"], doc["grade"])

	first, ok := runs[0].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, first, "checkpoints")
	assert.Equal(t, float64(1), first["ref_runs"])
}

func TestEvalSingleLogIsMarkedSelfFit(t *testing.T) {
	t.Serial()
	out, _, err := execLpi(t, nil, "eval", demoBuild1)
	require.NoError(t, err)
	assert.Contains(t, out, "scored against itself")
}

func TestEvalAgainstStoredKeyAndLearn(t *testing.T) {
	t.Serial()
	db := seedDemoModel(t)
	out, _, err := execLpi(t, nil, "eval", "--db", db, "--key", "demo", "--learn", demoPartial)
	require.NoError(t, err)
	assert.Contains(t, out, "partial.log")
	assert.Contains(t, out, "learned 1 run(s)")

	m := loadModel(t, db, "demo")
	assert.Len(t, m.Runs, 3, "the scored log is added to the key")
}

func TestEvalLearnRequiresKey(t *testing.T) {
	t.Serial()
	_, _, err := execLpi(t, nil, "eval", "--learn", demoBuild1)
	require.Error(t, err)
	assert.ErrorContains(t, err, "--key")
}

func TestEvalMissingModelKeyFails(t *testing.T) {
	t.Serial()
	_, _, err := execLpi(t, nil, "eval", "--db", t.TempDir(), "--key", "nope", demoBuild1)
	require.Error(t, err)
}

func TestEvalUnreadableLogFails(t *testing.T) {
	t.Serial()
	_, _, err := execLpi(t, nil, "eval", filepath.Join(t.TempDir(), "missing.log"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "digest")
}

func TestEvalRejectsBadFormat(t *testing.T) {
	t.Serial()
	_, _, err := execLpi(t, nil, "eval", "--format", "nonsense", demoBuild1)
	require.Error(t, err)
	assert.ErrorContains(t, err, "unknown format")
}
