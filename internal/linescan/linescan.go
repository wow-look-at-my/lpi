// Package linescan splits a stream into lines without choking on very long
// ones. Unlike bufio.Scanner, which fails with ErrTooLong, overlong lines are
// truncated at MaxLine bytes and the remainder is discarded silently.
package linescan

import (
	"bufio"
	"io"
)

// MaxLine is the maximum length of a returned line in bytes.
const MaxLine = 1 << 20 // 1 MiB

const bufSize = 64 * 1024

// Scanner yields lines from a reader: it splits on '\n', strips one trailing
// '\r', and caps lines at MaxLine bytes. A final unterminated line is still
// yielded. It is implemented as a bufio.Reader ReadSlice loop.
type Scanner struct {
	r       *bufio.Reader
	text    string
	err     error
	done    bool
	scratch []byte
}

// NewScanner returns a Scanner reading from r.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{r: bufio.NewReaderSize(r, bufSize)}
}

// Scan advances to the next line. It returns false at end of input or on a
// read error; check Err afterwards.
func (s *Scanner) Scan() bool {
	if s.done {
		return false
	}
	buf := s.scratch[:0]
	sawData := false
	truncated := false
	for {
		chunk, err := s.r.ReadSlice('\n')
		if len(chunk) > 0 {
			sawData = true
		}
		full := false
		if len(chunk) > 0 && chunk[len(chunk)-1] == '\n' {
			chunk = chunk[:len(chunk)-1]
			full = true
		}
		if !truncated && len(chunk) > 0 {
			if len(buf)+len(chunk) > MaxLine {
				chunk = chunk[:MaxLine-len(buf)]
				truncated = true
			}
			buf = append(buf, chunk...)
		}
		if full {
			break
		}
		if err == nil || err == bufio.ErrBufferFull {
			continue // more of the same line still buffered
		}
		// Terminal condition: EOF or a real read error.
		s.done = true
		if err != io.EOF {
			s.err = err
		}
		if !sawData {
			s.scratch = buf
			return false
		}
		break // yield the final unterminated line
	}
	if !truncated && len(buf) > 0 && buf[len(buf)-1] == '\r' {
		buf = buf[:len(buf)-1]
	}
	s.scratch = buf
	s.text = string(buf)
	return true
}

// Text returns the current line.
func (s *Scanner) Text() string { return s.text }

// Err returns the first non-EOF error encountered, if any.
func (s *Scanner) Err() error { return s.err }
