package estimate

import (
	"io"
	"time"

	"github.com/wow-look-at-my/lpi/internal/model"
)

// Run is a completed reference run: the tokens it emitted, and when.
type Run = model.Run

// Recorder digests a completed run into a Run. Feed it that run's tokens in
// the order they were emitted, then call Finish.
type Recorder struct {
	dig *model.Digester
}

// NewRecorder returns a Recorder. source labels the recording in model listings.
func NewRecorder(source string) *Recorder {
	return &Recorder{dig: model.NewDigester(source, nil)}
}

// Observe records tok, emitted at at. An unset at inherits the previous token's clock.
func (r *Recorder) Observe(tok Token, at time.Time) { r.dig.Token(uint64(tok), at) }

// ObserveLine records raw log text emitted at at, dropping a line with nothing identifying.
func (r *Recorder) ObserveLine(line string, at time.Time) { r.dig.LineAt(line, at) }

// Finish converts the recording into a Run, and fails on a recording too short to place anything.
func (r *Recorder) Finish() (*Run, error) { return r.dig.Finish() }

// RecordFile digests a complete log file into a Run, detecting its stamps and unpacking gzip.
func RecordFile(path string) (*Run, error) { return model.DigestFile(path) }

// RecordFileWith digests a log file whose stamps are read with format, nil to detect.
func RecordFileWith(path string, format *TimeFormat) (*Run, error) {
	return model.DigestFileWith(path, format)
}

// ReplayFile hands every line of a log file to fn, stamped, so a finished run
// scores the way it would have live.
func ReplayFile(path string, format *TimeFormat, fn func(line string, at time.Time)) error {
	return model.ReplayFile(path, format, fn)
}

// RecordReader digests a log stream into a Run, hashing each line. It carries
// no clock, so the weights are positional.
func RecordReader(r io.Reader, source string) (*Run, error) {
	return model.DigestReader(r, source, nil)
}
