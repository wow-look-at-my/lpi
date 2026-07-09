package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCaptureRoundTripMatchesLiveDigest is the core durability guarantee:
// digesting a capture file yields byte-for-byte the same Run as the live
// digester saw, including per-line times, empty lines, and tab-containing
// lines. It also pins the out-of-band-stamp design: the fingerprints equal
// those of the raw line text, which an in-band stamp prefix would break.
func TestCaptureRoundTripMatchesLiveDigest(t *testing.T) {
	db := t.TempDir()
	const label = "run 2026-07-09 12:00:00 make -j8"
	cw, err := NewCaptureWriter(db, "mybuild", label)
	require.NoError(t, err)

	lines := []string{
		"alpha start",
		"", // empty: skipped by the digester, recorded anyway
		"tab\there value=42",
		"beta 0x1f2e link",
		"gamma done",
	}
	offsets := []time.Duration{0, time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}
	live := NewDigester(label, nil)
	base := time.Unix(1700000000, 123456789)
	for i, ln := range lines {
		at := base.Add(offsets[i])
		live.LineAt(ln, at)
		require.NoError(t, cw.Add(ln, at))
	}
	require.NoError(t, cw.Close())
	want, err := live.Finish()
	require.NoError(t, err)

	got, err := DigestFile(cw.Path())
	require.NoError(t, err)
	assert.Equal(t, label, got.Source, "the header's source label round-trips")
	assert.True(t, got.HasTimes)
	assert.Equal(t, want.Duration, got.Duration)
	assert.Equal(t, want.Lines, got.Lines)
	assert.Equal(t, want.Occ, got.Occ, "replayed fingerprints and fractions must be identical")
}

func TestCaptureFileLocationAndNaming(t *testing.T) {
	db := t.TempDir()
	cw, err := NewCaptureWriter(db, "weird/key name", "src")
	require.NoError(t, err)
	defer cw.Discard()
	assert.Equal(t, PendingDir(db), filepath.Dir(cw.Path()))
	base := filepath.Base(cw.Path())
	assert.True(t, strings.HasPrefix(base, "weird_key_name-"), "key is sanitized like PathForKey: %q", base)
	assert.True(t, strings.HasSuffix(base, ".log"), "capture files end in .log: %q", base)
}

func TestCaptureLongKeyTruncatedInFileName(t *testing.T) {
	db := t.TempDir()
	cw, err := NewCaptureWriter(db, strings.Repeat("q", 300), "")
	require.NoError(t, err, "a huge key must not push the file name past OS limits")
	defer cw.Discard()
	assert.LessOrEqual(t, len(filepath.Base(cw.Path())), 80)
}

func TestCaptureWriterLifecycle(t *testing.T) {
	db := t.TempDir()
	cw, err := NewCaptureWriter(db, "k", "")
	require.NoError(t, err)
	require.NoError(t, cw.Add("x", time.Unix(1, 0)))
	require.NoError(t, cw.Close())
	require.NoError(t, cw.Add("after close", time.Unix(2, 0)), "Add after Close is a no-op")
	require.NoError(t, cw.Close(), "Close is idempotent")
	path := cw.Path()
	cw.Discard()
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "Discard removes the file")

	var nilCW *CaptureWriter
	assert.NoError(t, nilCW.Add("x", time.Unix(3, 0)))
	assert.NoError(t, nilCW.Close())
	assert.Equal(t, "", nilCW.Path())
	nilCW.Discard()
}

func TestCaptureWriterCreateError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "dbfile")
	require.NoError(t, os.WriteFile(blocker, []byte("not a dir"), 0o644))
	_, err := NewCaptureWriter(blocker, "k", "")
	require.Error(t, err, "pending/ under a file must fail cleanly")
}

func TestCaptureHeaderLabelSanitized(t *testing.T) {
	db := t.TempDir()
	cw, err := NewCaptureWriter(db, "k", "line\nbreak\rlabel")
	require.NoError(t, err)
	for i, ln := range []string{"alpha", "beta"} {
		require.NoError(t, cw.Add(ln, time.Unix(int64(i+1), 0)))
	}
	require.NoError(t, cw.Close())
	run, err := DigestFile(cw.Path())
	require.NoError(t, err)
	assert.Equal(t, "line break label", run.Source, "newlines cannot corrupt the one-line header")
	assert.Equal(t, 2, run.Lines)
}

func TestCaptureHeaderWithoutLabelUsesPath(t *testing.T) {
	path := writeFile(t, "cap.log", "#lpi-capture v1\n1000000000\talpha\n2000000000\tbeta\n")
	run, err := DigestFile(path)
	require.NoError(t, err)
	assert.Equal(t, path, run.Source)
	assert.True(t, run.HasTimes)
	assert.Equal(t, time.Second, run.Duration)
}

func TestCaptureFileGzipped(t *testing.T) {
	path := writeGzipFile(t, "cap.log.gz",
		"#lpi-capture v1\tzipped run\n0\talpha\n1000000000\tbeta\n")
	run, err := DigestFile(path)
	require.NoError(t, err)
	assert.Equal(t, "zipped run", run.Source, "the gzip sniff runs before the header sniff")
	assert.True(t, run.HasTimes)
	assert.Equal(t, time.Second, run.Duration)
}

func TestCaptureMalformedRecordsDegradeGracefully(t *testing.T) {
	path := writeFile(t, "cap.log", "#lpi-capture v1\tx\n"+
		"not-a-number\tline one\n"+
		"plain line without tab\n"+
		"1500000000\tline two\n"+
		"2500000000\tline three\n")
	run, err := DigestFile(path)
	require.NoError(t, err, "malformed records must not crash the digest")
	assert.Equal(t, 4, run.Lines)
	assert.True(t, run.HasTimes)
	assert.Equal(t, time.Second, run.Duration, "only well-formed stamps drive the clock")
}

func TestCaptureNonCaptureFilesUnaffected(t *testing.T) {
	// A log whose first line merely mentions the magic mid-line is a plain log.
	path := writeFile(t, "plain.log", "note: #lpi-capture v1 is the header\nalpha\nbeta\n")
	run, err := DigestFile(path)
	require.NoError(t, err)
	assert.Equal(t, 3, run.Lines)
	assert.False(t, run.HasTimes)
}
