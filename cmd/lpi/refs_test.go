package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/lpi/internal/model"
	"github.com/wow-look-at-my/lpi/internal/progress"
)

// TestJSONSnapshotEmptyModel proves the
func TestJSONSnapshotEmptyModel(t *testing.T) {
	t.Serial()
	est := progress.NewEstimator(model.New("empty"))
	base := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	est.Observe("first baseline line", base)
	est.Observe("second baseline line", base.Add(2*time.Second))

	var buf bytes.Buffer
	require.NoError(t, writeJSONSnapshot(&buf, est.Snapshot()),
		"a NaN or Inf anywhere in the snapshot would fail json.Marshal")

	snap := parseJSONLine(t, strings.TrimSpace(buf.String()))
	assert.Equal(t, 0.0, snap["progress"])
	assert.Equal(t, 0.0, snap["units_done"])
	assert.Equal(t, 0.0, snap["units_total"])
	assert.Equal(t, 0.0, snap["units_pct"])
	assert.Equal(t, 0.0, snap["match_rate"])
	assert.Equal(t, 0.0, snap["pace"])
	assert.Equal(t, "none", snap["confidence"])
	assert.Equal(t, "none", snap["eta_kind"])
	assert.NotContains(t, snap, "eta_seconds")
	assert.Equal(t, 2.0, snap["elapsed_seconds"])
	assert.Equal(t, true, snap["elapsed_known"])
	assert.Equal(t, 2.0, snap["current_lines"])
	assert.Equal(t, 2.0, snap["novel_lines"])
}
