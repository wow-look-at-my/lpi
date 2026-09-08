package estimate

import (
	"time"

	"github.com/wow-look-at-my/lpi/internal/model"
)

// Capture streams a learning run to a file as it arrives, so a run that dies
// before Finish stays recoverable. RecordFile reads the file back.
type Capture struct {
	cw *model.CaptureWriter
}

// PendingDir is where a Store keeps capture files that were never learned.
func PendingDir(dir string) string { return model.PendingDir(dir) }

// NewCapture opens the capture file for a run being learned under key.
func NewCapture(dir, key, source string) (*Capture, error) {
	cw, err := model.NewCaptureWriter(dir, key, source)
	if err != nil {
		return nil, err
	}
	return &Capture{cw: cw}, nil
}

// Add records a line and its time. A nil Capture drops it, so a caller that
// could not open the file needs no branch of its own.
func (c *Capture) Add(line string, at time.Time) error {
	if c == nil {
		return nil
	}
	return c.cw.Add(line, at)
}

// Path is the capture file on disk.
func (c *Capture) Path() string {
	if c == nil {
		return ""
	}
	return c.cw.Path()
}

// Close flushes the capture and leaves the file in place, to learn later.
func (c *Capture) Close() error {
	if c == nil {
		return nil
	}
	return c.cw.Close()
}

// Discard closes the capture and removes the file: the run it held is learned or worthless.
func (c *Capture) Discard() {
	if c == nil {
		return
	}
	c.cw.Discard()
}
