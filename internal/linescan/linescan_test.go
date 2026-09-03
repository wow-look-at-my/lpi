package linescan

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func collect(t *testing.T, input string) []string {
	t.Helper()
	s := NewScanner(strings.NewReader(input))
	var lines []string
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	require.NoError(t, s.Err())
	return lines
}

func TestScanNormalLines(t *testing.T) {
	assert.Equal(t, []string{"one", "two", "three"}, collect(t, "one\ntwo\nthree\n"))
}

func TestScanCRLF(t *testing.T) {
	assert.Equal(t, []string{"one", "two"}, collect(t, "one\r\ntwo\r\n"))
}

func TestScanInteriorCRPreserved(t *testing.T) {
	// Only trailing '\r' is stripped; interior ones
	assert.Equal(t, []string{"a\rb", "x\r"}, collect(t, "a\rb\nx\r\r\n"))
}

func TestScanNoTrailingNewline(t *testing.T) {
	assert.Equal(t, []string{"one", "last"}, collect(t, "one\nlast"))
}

func TestScanEmptyInput(t *testing.T) {
	assert.Empty(t, collect(t, ""))
}

func TestScanEmptyLines(t *testing.T) {
	assert.Equal(t, []string{"", "mid", ""}, collect(t, "\nmid\n\n"))
}

func TestScanOverlongLineTruncated(t *testing.T) {
	long := strings.Repeat("a", MaxLine+5000)
	lines := collect(t, long+"\nnext\n")
	require.Len(t, lines, 2)
	assert.Len(t, lines[0], MaxLine)
	assert.Equal(t, strings.Repeat("a", MaxLine), lines[0])
	assert.Equal(t, "next", lines[1])
}

func TestScanExactlyMaxLineKept(t *testing.T) {
	exact := strings.Repeat("b", MaxLine)
	lines := collect(t, exact+"\nz\n")
	require.Len(t, lines, 2)
	assert.Equal(t, exact, lines[0])
	assert.Equal(t, "z", lines[1])
}

func TestScanOverlongFinalUnterminatedLine(t *testing.T) {
	long := strings.Repeat("c", MaxLine+123)
	lines := collect(t, long)
	require.Len(t, lines, 1)
	assert.Len(t, lines[0], MaxLine)
}

// readerWithErr returns its data on the read, then
type readerWithErr struct {
	data string
	err  error
}

func (r *readerWithErr) Read(p []byte) (int, error) {
	if r.data == "" {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestScanReadError(t *testing.T) {
	boom := errors.New("boom")
	s := NewScanner(&readerWithErr{data: "partial", err: boom})
	require.True(t, s.Scan()) // partial data before the error is yielded
	assert.Equal(t, "partial", s.Text())
	assert.False(t, s.Scan())
	assert.ErrorIs(t, s.Err(), boom)
}

func TestScanErrorWithoutData(t *testing.T) {
	boom := errors.New("boom")
	s := NewScanner(&readerWithErr{err: boom})
	assert.False(t, s.Scan())
	assert.ErrorIs(t, s.Err(), boom)
	assert.False(t, s.Scan(), "scanner stays done")
}
