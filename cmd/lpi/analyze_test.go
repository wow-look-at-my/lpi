package main

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeHumanSummary(t *testing.T) {
	db := seedDemoModel(t)
	out, _, err := execLpi(t, nil, "analyze", "--db", db, "--key", "demo", demoPartial)
	require.NoError(t, err)
	assert.Contains(t, out, "Progress:")
	assert.Contains(t, out, "(time-weighted)")
	assert.Contains(t, out, "ETA:")
	assert.Contains(t, out, "Confidence:  high")
	assert.Contains(t, out, "Reference:")
}

// jsonSnapshotKeys is the pinned public schema of
var jsonSnapshotKeys = []string{
	"progress", "units_done", "units_total", "units_pct", "has_times",
	"elapsed_seconds", "elapsed_known", "ref_duration_seconds", "eta_seconds",
	"eta_kind", "pace", "match_rate", "confidence", "current_lines",
	"matched_lines", "novel_lines", "overflow_lines",
}

func TestAnalyzeJSON(t *testing.T) {
	db := seedDemoModel(t)
	out, _, err := execLpi(t, nil, "analyze", "--db", db, "--key", "demo", "--json", demoPartial)
	require.NoError(t, err)
	snap := parseJSONLine(t, strings.TrimSpace(out))

	var keys []string
	for k := range snap {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, jsonSnapshotKeys, keys)

	progress := snap["progress"].(float64)
	assert.Greater(t, progress, 0.35)
	assert.Less(t, progress, 0.75)
	assert.Equal(t, "high", snap["confidence"])
	assert.Equal(t, "pace", snap["eta_kind"])
	assert.True(t, snap["has_times"].(bool))
	assert.True(t, snap["elapsed_known"].(bool))
	assert.Greater(t, snap["units_pct"].(float64), 1.0, "units_pct is a percentage, not a fraction")
	assert.Greater(t, snap["eta_seconds"].(float64), 0.0)
	assert.Greater(t, snap["pace"].(float64), 0.0)
}

func TestAnalyzeJSONOmitsETAWhenNone(t *testing.T) {
	db := seedDemoModel(t)
	out, _, err := execLpi(t, strings.NewReader("\n \n"), "analyze", "--db", db, "--key", "demo", "--json", "-")
	require.NoError(t, err)
	snap := parseJSONLine(t, strings.TrimSpace(out))
	assert.Equal(t, "none", snap["eta_kind"])
	assert.NotContains(t, snap, "eta_seconds")
	assert.Equal(t, "none", snap["confidence"])
}

func TestAnalyzeStdin(t *testing.T) {
	db := seedDemoModel(t)
	data, err := os.ReadFile(demoPartial)
	require.NoError(t, err)
	out, _, err := execLpi(t, bytes.NewReader(data), "analyze", "--db", db, "--key", "demo", "-")
	require.NoError(t, err)
	assert.Contains(t, out, "Confidence:  high")
}

func TestAnalyzeAdhocRefsAndGzip(t *testing.T) {
	// Gzip reference on the fly; DigestFile must sniff
	data, err := os.ReadFile(demoBuild1)
	require.NoError(t, err)
	gzPath := filepath.Join(t.TempDir(), "build1.log.gz")
	f, err := os.Create(gzPath)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	_, err = gz.Write(data)
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	require.NoError(t, f.Close())

	out, _, err := execLpi(t, nil, "analyze", "--ref", gzPath, "--ref", demoBuild2, demoPartial)
	require.NoError(t, err)
	assert.Contains(t, out, "Confidence:  high")
}

func TestAnalyzeKeyPlusExtraRef(t *testing.T) {
	db := seedDemoModel(t)
	out, _, err := execLpi(t, nil, "analyze", "--db", db, "--key", "demo", "--ref", demoBuild1, demoPartial)
	require.NoError(t, err)
	assert.Contains(t, out, "Progress:")
}

func TestAnalyzeErrors(t *testing.T) {
	db := seedDemoModel(t)

	_, _, err := execLpi(t, nil, "analyze", demoPartial)
	require.ErrorContains(t, err, "no reference given")

	_, _, err = execLpi(t, nil, "analyze", "--db", db, "--key", "nope", demoPartial)
	require.ErrorContains(t, err, `no model for key "nope"`)
	require.ErrorContains(t, err, "available: demo")

	_, _, err = execLpi(t, nil, "analyze", "--db", t.TempDir(), "--key", "nope", demoPartial)
	require.ErrorContains(t, err, "no models learned yet")

	_, _, err = execLpi(t, nil, "analyze", "--db", db, "--key", "demo", "does-not-exist.log")
	require.Error(t, err)

	_, _, err = execLpi(t, nil, "analyze", "--ref", "does-not-exist.log", demoPartial)
	require.ErrorContains(t, err, "digest")
}
