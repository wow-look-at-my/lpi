// Package model digests completed reference logs
package model

import (
	"bufio"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"time"

	"github.com/wow-look-at-my/lpi/internal/fingerprint"
	"github.com/wow-look-at-my/lpi/internal/linescan"
	"github.com/wow-look-at-my/lpi/internal/timeparse"
)

// Occurrence is expected appearance of a
type Occurrence struct {
	// TimeFrac is the completion time of this
	TimeFrac float32
	// WeightFrac is the share of the total run duration
	WeightFrac float32
}

// Run is the digest of completed reference run
type Run struct {
	Source   string
	Lines    int           // nonempty lines digested
	Duration time.Duration // if unknown
	HasTimes bool
	// TimeFormat names the stamp reader the digest
	TimeFormat string `json:",omitempty"`
	Occ        map[uint64][]Occurrence
}

// rawOcc is line observation prior to fraction
type rawOcc struct {
	idx   int           // nonempty line index
	at    time.Time     // effective (monotonic) time; valid when timed
	gap   time.Duration // time since the previous line
	timed bool          // false only before the timestamp of the run
}

// Digester incrementally consumes run's log lines
type Digester struct {
	source string
	format *timeparse.Format
	stamp  *timeparse.Stamper
	occ    map[uint64][]rawOcc
	count  int
	first  time.Time
	haveT  bool
}

// NewDigester returns a Digester for run
func NewDigester(source string, format *timeparse.Format) *Digester {
	return &Digester{
		source: source,
		format: format,
		stamp:  timeparse.NewStamper(format),
		occ:    make(map[uint64][]rawOcc),
	}
}

// Line feeds raw log line, parsing its timestamp
func (d *Digester) Line(text string) {
	norm := fingerprint.Normalize(text)
	if norm == "" {
		return
	}
	eff, gap, timed := d.stamp.Stamp(text)
	d.record(fingerprint.Sum64(norm), eff, gap, timed)
}

// LineAt feeds raw log line stamped with an
func (d *Digester) LineAt(text string, at time.Time) {
	d.add(text, at)
}

func (d *Digester) add(text string, at time.Time) {
	norm := fingerprint.Normalize(text)
	if norm == "" {
		return
	}
	d.Token(fingerprint.Sum64(norm), at)
}

// Token feeds an already-hashed token stamped with at: the seam the token API
// digests through, skipping line normalization.
func (d *Digester) Token(fp uint64, at time.Time) {
	eff, gap, timed := d.stamp.At(at)
	d.record(fp, eff, gap, timed)
}

// record files the occurrence. A token that arrives before any stamp stays
// untimed, which pins it to the run start.
func (d *Digester) record(fp uint64, eff time.Time, gap time.Duration, timed bool) {
	ro := rawOcc{idx: d.count, at: eff, gap: gap, timed: timed}
	d.count++
	if timed && !d.haveT {
		d.first, d.haveT = eff, true
	}
	d.occ[fp] = append(d.occ[fp], ro)
}

// Finish converts the accumulated observations into
func (d *Digester) Finish() (*Run, error) {
	if d.count < 2 {
		return nil, errors.New("model: need at least 2 nonempty log lines")
	}
	run := &Run{Source: d.source, Lines: d.count, Occ: make(map[uint64][]Occurrence, len(d.occ))}
	if d.format != nil && d.haveT {
		run.TimeFormat = d.format.Name()
	}
	var dur time.Duration
	if d.haveT {
		dur = d.stamp.Last().Sub(d.first)
	}
	if dur > 0 {
		d.finishTimed(run, dur)
	} else {
		// No usable times (none given, none parsed, or
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
			// !ro.timed: before the timestamp -> TimeFrac weight
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

// DigestReader digests all lines from r into a Run
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

// DigestFile digests a log file into a Run
func DigestFile(path string) (*Run, error) { return DigestFileWith(path, nil) }

// DigestFileWith digests a log file with a
func DigestFileWith(path string, format *timeparse.Format) (*Run, error) {
	var d *Digester
	label, err := readFile(path, format, func(used *timeparse.Format, text string, at time.Time) {
		if d == nil {
			d = NewDigester(path, used)
		}
		d.LineAt(text, at)
	})
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, errors.New("model: need at least 2 nonempty log lines")
	}
	if label != "" {
		d.source = label
	}
	return d.Finish()
}

// ReplayFile hands every line of path to fn, with
func ReplayFile(path string, format *timeparse.Format, fn func(text string, at time.Time)) error {
	_, err := readFile(path, format, func(_ *timeparse.Format, text string, at time.Time) {
		fn(text, at)
	})
	return err
}

// readFile is the reader behind digesting and replay, so a log is opened,
func readFile(path string, format *timeparse.Format, fn func(*timeparse.Format, string, time.Time)) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 64*1024)
	var r io.Reader = br
	if magic, perr := br.Peek(2); perr == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, gerr := gzip.NewReader(br)
		if gerr != nil {
			return "", gerr
		}
		defer gz.Close()
		r = gz
	}
	sc := linescan.NewScanner(r)
	det := timeparse.NewDetector(format)
	if sc.Scan() {
		if label, ok := parseCaptureHeader(sc.Text()); ok {
			for sc.Scan() {
				text, at := parseCaptureRecord(sc.Text())
				fn(nil, text, at)
			}
			return label, sc.Err()
		}
		for ready := det.Add(sc.Text()); !ready && sc.Scan(); ready = det.Add(sc.Text()) {
		}
	}
	format, sample := det.Decide()
	stamp := func(text string) time.Time {
		if format == nil {
			return time.Time{}
		}
		t, ok := format.Parse(text)
		if !ok {
			return time.Time{}
		}
		return t
	}
	for _, ln := range sample {
		fn(format, ln, stamp(ln))
	}
	for sc.Scan() {
		text := sc.Text()
		fn(format, text, stamp(text))
	}
	return "", sc.Err()
}
