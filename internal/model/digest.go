// Package model digests completed reference logs into runs and merges runs
// into the expectation model consumed by the progress estimator.
package model

import (
	"bufio"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"time"

	"github.com/wow-look-at-my/log-progress-indicator/internal/fingerprint"
	"github.com/wow-look-at-my/log-progress-indicator/internal/linescan"
	"github.com/wow-look-at-my/log-progress-indicator/internal/timeparse"
)

// Occurrence is one expected appearance of a fingerprint within a run.
type Occurrence struct {
	// TimeFrac is the completion time of this occurrence as a fraction of
	// the run duration (line-index fraction when the run has no times).
	TimeFrac float32
	// WeightFrac is the share of the total run duration this occurrence
	// owns: the gap between it and the previous line of the run.
	WeightFrac float32
}

// Run is the digest of one completed reference run.
type Run struct {
	Source   string
	Lines    int           // nonempty lines digested
	Duration time.Duration // 0 if unknown
	HasTimes bool
	Occ      map[uint64][]Occurrence
}

// rawOcc is one line observation prior to fraction conversion.
type rawOcc struct {
	idx   int           // 0-based nonempty line index
	at    time.Time     // effective (monotonic) time; valid when timed
	gap   time.Duration // time since the previous line
	timed bool          // false only before the first timestamp of the run
}

// Digester incrementally consumes one run's log lines. Feed lines with Line
// (timestamps parsed via the format, if any) or LineAt (explicit times, for
// live-recorded runs), then call Finish.
type Digester struct {
	source string
	format *timeparse.Format
	occ    map[uint64][]rawOcc
	count  int
	first  time.Time
	prev   time.Time
	haveT  bool
}

// NewDigester returns a Digester for one run. A nil format means position
// mode: line indices stand in for times unless LineAt supplies them.
func NewDigester(source string, format *timeparse.Format) *Digester {
	return &Digester{source: source, format: format, occ: make(map[uint64][]rawOcc)}
}

// Line feeds one raw log line, parsing its timestamp with the configured
// format when one was given.
func (d *Digester) Line(text string) {
	var at time.Time
	if d.format != nil {
		if t, ok := d.format.Parse(text); ok {
			at = t
		}
	}
	d.add(text, at)
}

// LineAt feeds one raw log line stamped with an explicit time (zero if
// unknown).
func (d *Digester) LineAt(text string, at time.Time) {
	d.add(text, at)
}

func (d *Digester) add(text string, at time.Time) {
	norm := fingerprint.Normalize(text)
	if norm == "" {
		return
	}
	fp := fingerprint.Sum64(norm)
	ro := rawOcc{idx: d.count}
	d.count++
	switch {
	case !at.IsZero():
		eff := at
		if d.haveT {
			if eff.Before(d.prev) {
				eff = d.prev // clamp: the effective clock never moves backwards
			}
			ro.gap = eff.Sub(d.prev)
		} else {
			d.first = eff
			d.haveT = true
		}
		d.prev = eff
		ro.at = eff
		ro.timed = true
	case d.haveT:
		// No timestamp on this line: carry the previous time forward.
		ro.at = d.prev
		ro.timed = true
	default:
		// Before the first timestamp: pinned to the run start at Finish.
	}
	d.occ[fp] = append(d.occ[fp], ro)
}

// Finish converts the accumulated observations into a Run. It fails when
// fewer than 2 nonempty lines were digested.
func (d *Digester) Finish() (*Run, error) {
	if d.count < 2 {
		return nil, errors.New("model: need at least 2 nonempty log lines")
	}
	run := &Run{Source: d.source, Lines: d.count, Occ: make(map[uint64][]Occurrence, len(d.occ))}
	var dur time.Duration
	if d.haveT {
		dur = d.prev.Sub(d.first)
	}
	if dur > 0 {
		d.finishTimed(run, dur)
	} else {
		// No usable times (none given, none parsed, or zero span): position
		// mode, where line indices stand in for times.
		d.finishPositional(run)
	}
	return run, nil
}

func (d *Digester) finishTimed(run *Run, dur time.Duration) {
	run.Duration = dur
	run.HasTimes = true
	fdur := float64(dur)
	for fp, list := range d.occ {
		occs := make([]Occurrence, len(list))
		for i, ro := range list {
			if ro.timed {
				occs[i] = Occurrence{
					TimeFrac:   float32(float64(ro.at.Sub(d.first)) / fdur),
					WeightFrac: float32(float64(ro.gap) / fdur),
				}
			}
			// !ro.timed: before the first timestamp -> TimeFrac 0, weight 0.
		}
		run.Occ[fp] = occs
	}
}

func (d *Digester) finishPositional(run *Run) {
	den := float64(d.count - 1)
	for fp, list := range d.occ {
		occs := make([]Occurrence, len(list))
		for i, ro := range list {
			occs[i] = Occurrence{TimeFrac: float32(float64(ro.idx) / den)}
			if ro.idx > 0 {
				occs[i].WeightFrac = float32(1 / den)
			}
		}
		run.Occ[fp] = occs
	}
}

// DigestReader digests all lines from r into a Run.
func DigestReader(r io.Reader, source string, format *timeparse.Format) (*Run, error) {
	d := NewDigester(source, format)
	sc := linescan.NewScanner(r)
	for sc.Scan() {
		d.Line(sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return d.Finish()
}

// detectLines is how many leading lines DigestFile samples for timestamp
// format detection.
const detectLines = 300

// DigestFile digests a log file into a Run. Gzip files are handled
// transparently (sniffed via magic bytes), as are lpi capture files (sniffed
// via their header line), whose records carry the exact per-line times of
// the recorded run. For plain logs the first 300 lines are sampled for
// timeparse.Detect, then the whole file is digested.
func DigestFile(path string) (*Run, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 64*1024)
	var r io.Reader = br
	if magic, perr := br.Peek(2); perr == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, gerr := gzip.NewReader(br)
		if gerr != nil {
			return nil, gerr
		}
		defer gz.Close()
		r = gz
	}
	sc := linescan.NewScanner(r)
	var sample []string
	if sc.Scan() {
		if label, ok := parseCaptureHeader(sc.Text()); ok {
			return digestCapture(sc, path, label)
		}
		sample = append(sample, sc.Text())
		for len(sample) < detectLines && sc.Scan() {
			sample = append(sample, sc.Text())
		}
	}
	d := NewDigester(path, timeparse.Detect(sample))
	for _, ln := range sample {
		d.Line(ln)
	}
	for sc.Scan() {
		d.Line(sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return d.Finish()
}

// digestCapture digests the records following a capture-file header via
// per-record explicit times, reconstructing the run exactly as it was
// digested live. The header's source label becomes Run.Source when present;
// otherwise the file path does, matching plain-log behavior.
func digestCapture(sc *linescan.Scanner, path, label string) (*Run, error) {
	source := path
	if label != "" {
		source = label
	}
	d := NewDigester(source, nil)
	for sc.Scan() {
		text, at := parseCaptureRecord(sc.Text())
		d.LineAt(text, at)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return d.Finish()
}
