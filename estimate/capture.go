package estimate

import "github.com/wow-look-at-my/lpi/internal/model"

// Capture streams a learning run to a file, so a dead run stays recoverable.
type Capture = model.CaptureWriter

// PendingDir is where a Store keeps capture files that were never learned.
func PendingDir(dir string) string { return model.PendingDir(dir) }

// NewCapture opens the capture file for a run learned under key. Its methods
// take a nil Capture: a file that fails to open costs recovery.
func NewCapture(dir, key, source string) (*Capture, error) {
	return model.NewCaptureWriter(dir, key, source)
}
