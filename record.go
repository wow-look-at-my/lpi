package lpi

import (
	"io"
	"time"

	"github.com/wow-look-at-my/lpi/internal/model"
)

// Run is one completed reference run: every token it emitted, and when. A Run
// is what a Model is built from. Its Duration and HasTimes fields report the
// clock the recording carried, and Lines counts the tokens digested.
type Run = model.Run

// Recorder digests one completed run into a Run. Feed it the tokens of a run
// that finished, in the order they were emitted, then call Finish.
type Recorder struct {
	dig *model.Digester
}

// NewRecorder returns a Recorder for a run. source labels the recording in
// model listings; a path, a command line or a job name all read well.
func NewRecorder(source string) *Recorder {
	return &Recorder{dig: model.NewDigester(source, nil)}
}

// Observe records one token, emitted at at. An unset at means the token carries
// no time of its own: it inherits the previous token's clock, or, when the run
// carries no times at all, the run is weighted by position instead.
func (r *Recorder) Observe(tok Token, at time.Time) { r.dig.Token(uint64(tok), at) }

// ObserveLine records one raw line of log text, emitted at at. A line with
// nothing identifying in it is dropped, exactly as TokenOfLine reports.
func (r *Recorder) ObserveLine(line string, at time.Time) { r.dig.LineAt(line, at) }

// Finish converts what was recorded into a Run. It fails when fewer than two
// tokens were recorded, because a run of one token places nothing.
func (r *Recorder) Finish() (*Run, error) { return r.dig.Finish() }

// RecordFile digests a complete log file into a Run. Timestamps are detected
// from the file itself (ISO-8601, HH:MM:SS, syslog, go log, epoch, dmesg) and
// gzip is unpacked transparently. It is the library form of "lpi learn FILE".
func RecordFile(path string) (*Run, error) { return model.DigestFile(path) }

// RecordReader digests a complete log stream into a Run, one token per line.
// The lines carry no clock of their own, so the Run is weighted by position:
// use a Recorder with real times when the caller has them.
func RecordReader(r io.Reader, source string) (*Run, error) {
	return model.DigestReader(r, source, nil)
}
