package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/log-progress-indicator/internal/model"
)

func TestLearnThenModelLifecycle(t *testing.T) {
	db := t.TempDir()

	out, _, err := execLpi(t, nil, "learn", "--db", db, "--key", "demo", demoBuild1, demoBuild2)
	require.NoError(t, err)
	assert.Contains(t, out, "learned "+demoBuild1+": 106 lines,")
	assert.Contains(t, out, "learned "+demoBuild2+": 107 lines,")
	assert.Contains(t, out, "unique fingerprints")
	assert.Contains(t, out, `model "demo": 2 runs,`)
	assert.Contains(t, out, "units over")

	m := loadModel(t, db, "demo")
	assert.Len(t, m.Runs, 2)
	assert.True(t, m.HasTimes)

	// Learning again appends to the same key.
	out, _, err = execLpi(t, nil, "learn", "--db", db, "--key", "demo", demoPartial)
	require.NoError(t, err)
	assert.Contains(t, out, `model "demo": 3 runs,`)

	// --replace starts over.
	out, _, err = execLpi(t, nil, "learn", "--db", db, "--key", "demo", "--replace", demoBuild1)
	require.NoError(t, err)
	assert.Contains(t, out, `model "demo": 1 runs,`)

	// list shows the key.
	out, _, err = execLpi(t, nil, "model", "list", "--db", db)
	require.NoError(t, err)
	assert.Contains(t, out, "KEY")
	assert.Contains(t, out, "demo")
	assert.Contains(t, out, "1")

	// show prints per-run rows and merged totals.
	out, _, err = execLpi(t, nil, "model", "show", "--db", db, "demo")
	require.NoError(t, err)
	assert.Contains(t, out, "key:  demo")
	assert.Contains(t, out, "SOURCE")
	assert.Contains(t, out, "build1.log")
	assert.Contains(t, out, "yes")
	assert.Contains(t, out, "merged: 1 runs,")

	// rm deletes the model file.
	out, _, err = execLpi(t, nil, "model", "rm", "--db", db, "demo")
	require.NoError(t, err)
	assert.Contains(t, out, `removed model "demo"`)
	_, err = os.Stat(model.PathForKey(db, "demo"))
	assert.True(t, os.IsNotExist(err))

	// list on an empty database.
	out, _, err = execLpi(t, nil, "model", "list", "--db", db)
	require.NoError(t, err)
	assert.Contains(t, out, "no models in "+db)
}

func TestLearnErrors(t *testing.T) {
	db := t.TempDir()

	_, _, err := execLpi(t, nil, "learn", "--db", db, demoBuild1)
	require.ErrorContains(t, err, "--key is required")

	_, _, err = execLpi(t, nil, "learn", "--db", db, "--key", "k", "missing.log")
	require.ErrorContains(t, err, "digest missing.log")

	_, _, err = execLpi(t, nil, "learn", "--db", db, "--key", "k")
	require.Error(t, err) // no LOG args
}

func TestModelShowNoTimestamps(t *testing.T) {
	db := t.TempDir()
	log := t.TempDir() + "/plain.log"
	require.NoError(t, os.WriteFile(log, []byte("alpha step\nbeta step\ngamma step\n"), 0o644))

	out, _, err := execLpi(t, nil, "learn", "--db", db, "--key", "plain", log)
	require.NoError(t, err)
	assert.Contains(t, out, "no timestamps")
	assert.Contains(t, out, "no timing data")

	out, _, err = execLpi(t, nil, "model", "show", "--db", db, "plain")
	require.NoError(t, err)
	assert.Contains(t, out, "no")

	out, _, err = execLpi(t, nil, "model", "list", "--db", db)
	require.NoError(t, err)
	assert.Contains(t, out, "-") // duration column placeholder
}

func TestModelErrors(t *testing.T) {
	db := t.TempDir()

	_, _, err := execLpi(t, nil, "model", "show", "--db", db, "ghost")
	require.ErrorContains(t, err, `no model for key "ghost"`)

	_, _, err = execLpi(t, nil, "model", "rm", "--db", db, "ghost")
	require.ErrorContains(t, err, `no model for key "ghost"`)
}
