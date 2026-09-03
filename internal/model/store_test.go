package model

import (
	"compress/gzip"
	"encoding/gob"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	lines := []string{"alpha", "beta", "gamma"}
	m := New("cmake-build")
	m.AddRun(digestAt(t, "r1", lines, []time.Duration{0, 40 * time.Second, 100 * time.Second}))
	m.AddRun(digestAt(t, "r2", lines, []time.Duration{0, 60 * time.Second, 100 * time.Second}))

	path := filepath.Join(t.TempDir(), "nested", "dir", "cmake-build.lpi")
	require.NoError(t, m.Save(path))

	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, m.Key, got.Key)
	require.Len(t, got.Runs, 2)
	assert.Equal(t, m.Runs[0].Source, got.Runs[0].Source)
	assert.Equal(t, m.TotalUnits, got.TotalUnits)
	assert.Equal(t, m.RefDuration, got.RefDuration)
	assert.Equal(t, m.HasTimes, got.HasTimes)
	require.Len(t, got.Expect, len(m.Expect))
	for fp, want := range m.Expect {
		gotOccs, ok := got.Expect[fp]
		require.True(t, ok)
		require.Len(t, gotOccs, len(want))
		for i := range want {
			assert.InDelta(t, float64(want[i].TimeFrac), float64(gotOccs[i].TimeFrac), 1e-6)
			assert.InDelta(t, float64(want[i].WeightFrac), float64(gotOccs[i].WeightFrac), 1e-6)
		}
	}
}

func TestSaveLoadKeepsInvocations(t *testing.T) {
	m := New("k")
	m.AddRun(digestAt(t, "r1", []string{"alpha", "beta"}, []time.Duration{0, time.Second}))
	m.AddInvocation("make -j8")
	m.AddInvocation("make")

	path := filepath.Join(t.TempDir(), "k.lpi")
	require.NoError(t, m.Save(path))
	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"make", "make -j8"}, got.Invocations)
	assert.Equal(t, "make", got.DisplayLabel())
}

// TestLoadOldFormatWithoutInvocations pins
func TestLoadOldFormatWithoutInvocations(t *testing.T) {
	type legacyEnvelope struct {
		Version int
		Key     string
		Runs    []*Run
	}
	run := digestAt(t, "r1", []string{"alpha", "beta"}, []time.Duration{0, time.Second})

	path := filepath.Join(t.TempDir(), "old.lpi")
	f, err := os.Create(path)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	require.NoError(t, gob.NewEncoder(gz).Encode(legacyEnvelope{
		Version: 1, Key: "old", Runs: []*Run{run},
	}))
	require.NoError(t, gz.Close())
	require.NoError(t, f.Close())

	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "old", got.Key)
	require.Len(t, got.Runs, 1)
	assert.Empty(t, got.Invocations)
	assert.Equal(t, "old", got.DisplayLabel(), "no invocations falls back to the key")
}

func TestLoadRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.lpi")
	f, err := os.Create(path)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	require.NoError(t, gob.NewEncoder(gz).Encode(envelope{Version: 99, Key: "x"}))
	require.NoError(t, gz.Close())
	require.NoError(t, f.Close())

	_, err = Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestLoadErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := Load(filepath.Join(t.TempDir(), "nope.lpi"))
		assert.Error(t, err)
	})
	t.Run("not gzip", func(t *testing.T) {
		path := writeFile(t, "junk.lpi", "this is not a model")
		_, err := Load(path)
		assert.Error(t, err)
	})
	t.Run("gzip of garbage", func(t *testing.T) {
		path := writeGzipFile(t, "garbage.lpi", "not gob at all")
		_, err := Load(path)
		assert.Error(t, err)
	})
}

func TestSaveErrors(t *testing.T) {
	t.Run("parent is a file", func(t *testing.T) {
		blocker := writeFile(t, "blocker", "x")
		err := New("k").Save(filepath.Join(blocker, "sub", "m.lpi"))
		assert.Error(t, err)
	})
	t.Run("path is a directory", func(t *testing.T) {
		dir := t.TempDir()
		err := New("k").Save(dir)
		assert.Error(t, err)
	})
}

// TestSaveFailureLeavesExistingModelIntact pins the
func TestSaveFailureLeavesExistingModelIntact(t *testing.T) {
	dir := t.TempDir()
	base := strings.Repeat("k", 246) + ".lpi" // bytes: valid target, oversize temp
	path := filepath.Join(dir, base)

	m := New("k")
	m.AddRun(digestAt(t, "r1", []string{"alpha", "beta"}, []time.Duration{0, time.Second}))
	good := filepath.Join(dir, "good.lpi")
	require.NoError(t, m.Save(good))
	before, err := os.ReadFile(good)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, before, 0o644), "seed the existing model directly")

	m2 := New("k")
	m2.AddRun(digestAt(t, "r2", []string{"gamma", "delta", "epsilon"},
		[]time.Duration{0, time.Second, 2 * time.Second}))
	require.Error(t, m2.Save(path), "the temp file name exceeds NAME_MAX, so the save must fail")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a failed save must leave the existing model byte-identical")
	got, err := Load(path)
	require.NoError(t, err, "the existing model must still load")
	assert.Len(t, got.Runs, 1)
}

// TestSaveFailedRenameKeepsTargetAndLeavesNoLitter
func TestSaveFailedRenameKeepsTargetAndLeavesNoLitter(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "k.lpi")
	require.NoError(t, os.MkdirAll(target, 0o755))
	inner := filepath.Join(target, "keep")
	require.NoError(t, os.WriteFile(inner, []byte("precious"), 0o644))

	m := New("k")
	m.AddRun(digestAt(t, "r1", []string{"alpha", "beta"}, []time.Duration{0, time.Second}))
	require.Error(t, m.Save(target), "renaming a file over a directory must fail")

	data, err := os.ReadFile(inner)
	require.NoError(t, err)
	assert.Equal(t, "precious", string(data), "the failed save must not clobber the target")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temp litter after a failed save")
}

func TestDefaultDirPrecedence(t *testing.T) {
	t.Run("LPI_DB wins", func(t *testing.T) {
		t.Setenv("LPI_DB", "/custom/db")
		t.Setenv("XDG_CACHE_HOME", "/xdg")
		assert.Equal(t, "/custom/db", DefaultDir())
	})
	t.Run("XDG_CACHE_HOME second", func(t *testing.T) {
		t.Setenv("LPI_DB", "")
		t.Setenv("XDG_CACHE_HOME", "/xdg")
		assert.Equal(t, filepath.Join("/xdg", "log-progress-indicator"), DefaultDir())
	})
	t.Run("home cache third", func(t *testing.T) {
		t.Setenv("LPI_DB", "")
		t.Setenv("XDG_CACHE_HOME", "")
		t.Setenv("HOME", "/home/someone")
		assert.Equal(t, filepath.Join("/home/someone", ".cache", "log-progress-indicator"), DefaultDir())
	})
	t.Run("temp fallback without home", func(t *testing.T) {
		t.Setenv("LPI_DB", "")
		t.Setenv("XDG_CACHE_HOME", "")
		t.Setenv("HOME", "")
		assert.Equal(t, filepath.Join(os.TempDir(), "log-progress-indicator"), DefaultDir())
	})
}

func TestPathForKey(t *testing.T) {
	cases := []struct {
		key, want string
	}{
		{"make all", "make_all.lpi"},
		{"cmake -S . -B build", "cmake_-S_._-B_build.lpi"},
		{"a/b:c", "a_b_c.lpi"},
		{"Safe.Key_09-x", "Safe.Key_09-x.lpi"},
		{"", "default.lpi"},
		{"caff\xc3\xa8", "caff__.lpi"}, // multibyte becomes underscores
	}
	for _, tc := range cases {
		assert.Equal(t, filepath.Join("/db", tc.want), PathForKey("/db", tc.key), "key %q", tc.key)
	}
}
