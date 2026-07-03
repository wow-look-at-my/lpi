package model

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const isoLog = `2026-07-02T10:00:00 alpha start
2026-07-02T10:01:00 beta configure
2026-07-02T10:02:00 gamma compile
2026-07-02T10:03:00 delta compile more
2026-07-02T10:04:00 epsilon link
2026-07-02T10:05:00 zeta done
`

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func writeGzipFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	_, err = gz.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	require.NoError(t, f.Close())
	return path
}

func TestDigestFilePlainWithDetection(t *testing.T) {
	path := writeFile(t, "build.log", isoLog)
	run, err := DigestFile(path)
	require.NoError(t, err)
	assert.Equal(t, path, run.Source)
	assert.Equal(t, 6, run.Lines)
	assert.True(t, run.HasTimes, "ISO timestamps must be detected")
	assert.Equal(t, 5*time.Minute, run.Duration)
}

func TestDigestFileGzip(t *testing.T) {
	plain, err := DigestFile(writeFile(t, "build.log", isoLog))
	require.NoError(t, err)
	gzRun, err := DigestFile(writeGzipFile(t, "build.log.gz", isoLog))
	require.NoError(t, err)

	assert.Equal(t, plain.Lines, gzRun.Lines)
	assert.Equal(t, plain.Duration, gzRun.Duration)
	assert.Equal(t, plain.HasTimes, gzRun.HasTimes)
	assert.Equal(t, plain.Occ, gzRun.Occ)
}

func TestDigestFileNoTimestamps(t *testing.T) {
	path := writeFile(t, "plain.log", "alpha one\nbeta two\ngamma three\n")
	run, err := DigestFile(path)
	require.NoError(t, err)
	assert.False(t, run.HasTimes)
	assert.Equal(t, 3, run.Lines)
}

func TestDigestFileLongerThanSample(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < detectLines+50; i++ {
		sb.WriteString("2026-07-02T10:00:00 step ")
		sb.WriteString(strings.Repeat("x", i%7+1)) // vary the text a little
		sb.WriteString("\n")
	}
	path := writeFile(t, "big.log", sb.String())
	run, err := DigestFile(path)
	require.NoError(t, err)
	assert.Equal(t, detectLines+50, run.Lines, "lines beyond the sample are digested too")
}

func TestDigestFileErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := DigestFile(filepath.Join(t.TempDir(), "nope.log"))
		assert.Error(t, err)
	})
	t.Run("empty file", func(t *testing.T) {
		_, err := DigestFile(writeFile(t, "empty.log", ""))
		assert.Error(t, err)
	})
	t.Run("corrupt gzip header", func(t *testing.T) {
		_, err := DigestFile(writeFile(t, "bad.gz", "\x1f\x8bnot really gzip data"))
		assert.Error(t, err)
	})
	t.Run("truncated gzip body", func(t *testing.T) {
		full := writeGzipFile(t, "trunc.gz", strings.Repeat(isoLog, 200))
		data, err := os.ReadFile(full)
		require.NoError(t, err)
		path := writeFile(t, "half.gz", string(data[:len(data)/2]))
		_, err = DigestFile(path)
		assert.Error(t, err)
	})
}
