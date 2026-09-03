package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// captureMagic is the line of a capture file
const captureMagic = "#lpi-capture v1"

// PendingDir returns the directory under db where
func PendingDir(db string) string { return filepath.Join(db, "pending") }

// captureKeyLen caps the sanitized-key prefix of a
const captureKeyLen = 40

// CaptureWriter streams every line a learning run
type CaptureWriter struct {
	f    *os.File
	path string
}

// NewCaptureWriter creates
func NewCaptureWriter(db, key, source string) (*CaptureWriter, error) {
	dir := PendingDir(db)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	san := sanitizeKey(key)
	if len(san) > captureKeyLen {
		san = san[:captureKeyLen]
	}
	name := fmt.Sprintf("%s-%s-%d.log", san, time.Now().Format("20060102-150405"), os.Getpid())
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	header := captureMagic
	if source != "" {
		// The header is line; a newline in the label would
		header += "\t" + strings.NewReplacer("\n", " ", "\r", " ").Replace(source)
	}
	if _, err := f.WriteString(header + "\n"); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	return &CaptureWriter{f: f, path: path}, nil
}

// Add appends consumed line stamped with its time
func (cw *CaptureWriter) Add(text string, at time.Time) error {
	if cw == nil || cw.f == nil {
		return nil
	}
	if _, err := fmt.Fprintf(cw.f, "%d\t%s\n", at.UnixNano(), text); err != nil {
		cw.f.Close()
		cw.f = nil
		return err
	}
	return nil
}

// Path returns the capture file's location
func (cw *CaptureWriter) Path() string {
	if cw == nil {
		return ""
	}
	return cw.path
}

// Close closes the file, leaving it in place
func (cw *CaptureWriter) Close() error {
	if cw == nil || cw.f == nil {
		return nil
	}
	err := cw.f.Close()
	cw.f = nil
	return err
}

// Discard closes and removes the capture file: its
func (cw *CaptureWriter) Discard() {
	if cw == nil {
		return
	}
	_ = cw.Close()
	_ = os.Remove(cw.path)
}

// parseCaptureHeader reports whether line is a
func parseCaptureHeader(line string) (label string, ok bool) {
	if line == captureMagic {
		return "", true
	}
	if rest, found := strings.CutPrefix(line, captureMagic+"\t"); found {
		return rest, true
	}
	return "", false
}

// parseCaptureRecord splits capture record into its
func parseCaptureRecord(line string) (string, time.Time) {
	stamp, text, ok := strings.Cut(line, "\t")
	if !ok {
		return line, time.Time{}
	}
	nanos, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return line, time.Time{}
	}
	return text, time.Unix(0, nanos)
}
