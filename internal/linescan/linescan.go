// Package linescan splits a stream into lines
package linescan

import (
	"bufio"
	"io"
)

// MaxLine is the maximum length of a returned line
const MaxLine = 1 << 20 // MiB

const bufSize = 64 * 1024

// Scanner yields lines from a reader: it splits on
type Scanner struct {
	r       *bufio.Reader
	text    string
	err     error
	done    bool
	scratch []byte
}

// NewScanner returns a Scanner reading from r
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{r: bufio.NewReaderSize(r, bufSize)}
}

// Scan advances to the next line
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
		// Terminal condition: EOF or a real read error
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

// Text returns the current line
func (s *Scanner) Text() string { return s.text }

// Err returns the non-EOF error encountered, if any
func (s *Scanner) Err() error { return s.err }
