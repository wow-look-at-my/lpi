package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// captureMagic is the first line of a capture file, optionally followed by
// tab and a source label. Each subsequent record is
// unix nanoseconds>\t<line text>\n", split on the FIRST tab only, so
// the line text may itself contain tabs. Times are out-of-band because
// fingerprints hash the full line text: prepending stamps in-band would
// produce templates that never match live lines.
const captureMagic = "#lpi-capture v1"

// PendingDir returns the directory under db where capture files of learning
// runs live until the run is learned (then they are removed) or fails (then
// they are kept for 'lpi learn' recovery).
func PendingDir(db string) string { return filepath.Join(db, "pending") }

// captureKeyLen caps the sanitized-key prefix of a capture file name. The
// prefix is only a human hint; uniqueness comes from the timestamp and pid,
// and an uncapped key could push the file name past the OS limit.
const captureKeyLen = 40

// CaptureWriter streams every line a learning run consumes to a recovery
// file as it goes, so even a crash or SIGKILL of lpi loses nothing: the
// captured log can be learned later with 'lpi learn'. Writes are direct and
// unbuffered -- durability first; per-line log rates are trivial.
//
// A nil *CaptureWriter is valid: all methods are no-ops, so callers that
// disabled capture (creation failed) need no branches.
type CaptureWriter struct {
	f    *os.File
	path string
}

// NewCaptureWriter creates <db>/pending/<key>-<YYYYMMDD-HHMMSS>-<pid>.log
// and writes the header line. source labels the run in the header (it
// becomes Run.Source when the file is digested); empty means no label.
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
		// The header is line; a newline in the label would corrupt it.
		header += "\t" + strings.NewReplacer("\n", " ", "\r", " ").Replace(source)
	}
	if _, err := f.WriteString(header + "\n"); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	return &CaptureWriter{f: f, path: path}, nil
}

// Add appends consumed line stamped with its time. After the first
// write failure the file is closed and further calls are silent no-ops; the
// failure is returned exactly so the caller can print warning --
// capture must never break the run it is protecting.
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

// Path returns the capture file's location.
func (cw *CaptureWriter) Path() string {
	if cw == nil {
		return ""
	}
	return cw.path
}

// Close closes the file, leaving it in place. It is idempotent.
func (cw *CaptureWriter) Close() error {
	if cw == nil || cw.f == nil {
		return nil
	}
	err := cw.f.Close()
	cw.f = nil
	return err
}

// Discard closes and removes the capture file: its data is no longer needed
// (the run was learned, or nothing recoverable was captured).
func (cw *CaptureWriter) Discard() {
	if cw == nil {
		return
	}
	_ = cw.Close()
	_ = os.Remove(cw.path)
}

// parseCaptureHeader reports whether line is a capture-file header and
// returns its optional source label.
func parseCaptureHeader(line string) (label string, ok bool) {
	if line == captureMagic {
		return "", true
	}
	if rest, found := strings.CutPrefix(line, captureMagic+"\t"); found {
		return rest, true
	}
	return "", false
}

// parseCaptureRecord splits capture record into its line text and time.
// A malformed record (no tab, or a non-numeric stamp) degrades to the whole
// line with an unknown time rather than an error.
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
