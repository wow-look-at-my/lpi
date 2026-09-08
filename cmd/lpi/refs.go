package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/lpi/estimate"
	"github.com/wow-look-at-my/lpi/internal/render"
)

// tickInterval is how often the live modes advance
var tickInterval = 500 * time.Millisecond

// refFlags holds the flags every estimating command
type refFlags struct {
	refs   []string
	key    string
	db     string
	format string
	layout string
}

// addModelFlags registers --key and --db
func addModelFlags(cmd *cobra.Command, rf *refFlags) {
	cmd.Flags().StringVar(&rf.key, "key", "", "name of a learned model in the model database")
	cmd.Flags().StringVar(&rf.db, "db", estimate.DefaultDir(), "model database directory")
}

// addTimeFlags registers the timestamp-reading
func addTimeFlags(cmd *cobra.Command, rf *refFlags) {
	cmd.Flags().StringVar(&rf.format, "format", "",
		"how to read each line's timestamp: auto (default), a builtin ("+
			strings.Join(estimate.FormatNames(), ", ")+"), or a regex with named groups ("+
			strings.Join(estimate.FormatGroups(), ", ")+")")
	cmd.Flags().StringVar(&rf.layout, "time-layout", "",
		"Go reference layout for the regex 'time' group, or for the start of each line")
}

// timeFormat compiles --format/--time-layout
func (rf *refFlags) timeFormat() (*estimate.TimeFormat, error) {
	return estimate.CompileFormat(rf.format, rf.layout)
}

// addRefFlags registers --ref on top of the model
func addRefFlags(cmd *cobra.Command, rf *refFlags) {
	addModelFlags(cmd, rf)
	cmd.Flags().StringArrayVar(&rf.refs, "ref", nil,
		"reference log of a completed run (repeatable; gzip is handled transparently)")
}

// resolve builds the reference model: --key loads
func (rf *refFlags) resolve() (*estimate.Model, error) {
	if rf.key == "" && len(rf.refs) == 0 {
		return nil, errors.New("no reference given: use --key NAME and/or --ref FILE")
	}
	var m *estimate.Model
	if rf.key != "" {
		var err error
		if m, err = estimate.OpenStore(rf.db).Load(rf.key); err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("no model for key %q in %s (%s)",
					rf.key, rf.db, availableKeys(rf.db))
			}
			return nil, err
		}
	} else {
		m = estimate.NewModel("adhoc")
	}
	format, err := rf.timeFormat()
	if err != nil {
		return nil, err
	}
	for _, ref := range rf.refs {
		run, err := estimate.RecordFileWith(ref, format.Clone())
		if err != nil {
			return nil, fmt.Errorf("digest %s: %w", ref, err)
		}
		m.Add(run)
	}
	return m, nil
}

// resolveOrBootstrap resolves the reference model
func (rf *refFlags) resolveOrBootstrap(learnKey string) (m *estimate.Model, bootstrap bool, err error) {
	if len(rf.refs) > 0 || (rf.key != "" && rf.key != learnKey) {
		m, err = rf.resolve()
		return m, false, err
	}
	return estimate.OpenStore(rf.db).LoadOrNew(learnKey)
}

// bootstrapNotice tells the user why no progress
func bootstrapNotice(w io.Writer, key string) {
	fmt.Fprintf(w, "no model for key %q yet -- recording baseline run\n", key)
}

// notify delivers of lpi's own out-of-band lines
type notify func(format string, args ...any)

// plainNotify prints out-of-band lines directly to
func plainNotify(w io.Writer) notify {
	return func(format string, args ...any) {
		fmt.Fprintf(w, format+"\n", args...)
	}
}

// renderNotify routes out-of-band lines through
func renderNotify(r *render.Renderer) notify {
	return func(format string, args ...any) {
		r.Message(fmt.Sprintf(format, args...))
	}
}

// availableKeys names the models present in db, for
func availableKeys(db string) string {
	keys, err := estimate.OpenStore(db).Keys()
	if err != nil || len(keys) == 0 {
		return "no models learned yet"
	}
	return "available: " + strings.Join(keys, ", ")
}

// loadOrCreate returns the model stored for key, or
func loadOrCreate(db, key string) (*estimate.Model, error) {
	m, _, err := estimate.OpenStore(db).LoadOrNew(key)
	return m, err
}

// learnRun adds run to key's stored model, records
func learnRun(w io.Writer, db, key string, run *estimate.Run, invocation string) error {
	m, err := loadOrCreate(db, key)
	if err != nil {
		return err
	}
	m.Add(run)
	m.AddLabel(invocation)
	store := estimate.OpenStore(db)
	if err := store.Save(m); err != nil {
		return err
	}
	fmt.Fprintf(w, "learned run (%d lines, %s) into key %q (%d runs)\n",
		run.Lines, render.Duration(run.Duration), key, len(m.Runs()))
	return nil
}

// learnCapturedRun learns run under key and drops its capture. A save that
// fails keeps the capture instead, with the command that recovers it.
func learnCapturedRun(w io.Writer, msg notify, cw *estimate.Capture, db, key string, run *estimate.Run, invocation string) error {
	if err := learnRun(w, db, key, run, invocation); err != nil {
		keepCapture(msg, cw, db, key)
		return err
	}
	cw.Discard()
	return nil
}

// finishCapturedRun digests what was consumed. Too little to learn discards
// the capture: there is nothing in it to recover.
func finishCapturedRun(rec *estimate.Recorder[string], cw *estimate.Capture) (*estimate.Run, error) {
	run, err := rec.Finish()
	if err != nil {
		cw.Discard()
		return nil, fmt.Errorf("run not learned: %w", err)
	}
	return run, nil
}

// newCapture opens the durable capture file for a
func newCapture(msg notify, db, key, source string) *estimate.Capture {
	cw, err := estimate.NewCapture(db, key, source)
	if err != nil {
		msg("warning: capture file disabled: %v", err)
		return nil
	}
	return cw
}

// keepCapture closes the capture file, leaves it in
func keepCapture(msg notify, cw *estimate.Capture, db, key string) {
	if cw == nil {
		return
	}
	_ = cw.Close()
	msg("captured log kept: %s", cw.Path())
	msg("learn it later with: lpi learn --key %s --db %s %s", key, db, cw.Path())
}

// keepOrDiscardCapture keeps the capture file with
func keepOrDiscardCapture(msg notify, rec *estimate.Recorder[string], cw *estimate.Capture, db, key string) {
	if _, err := rec.Finish(); err != nil {
		cw.Discard()
		return
	}
	keepCapture(msg, cw, db, key)
}

// jsonSnapshot is the stable JSON form of a
type jsonSnapshot struct {
	Progress           float64  `json:"progress"`
	UnitsDone          int      `json:"units_done"`
	UnitsTotal         int      `json:"units_total"`
	UnitsPct           float64  `json:"units_pct"`
	HasTimes           bool     `json:"has_times"`
	ElapsedSeconds     float64  `json:"elapsed_seconds"`
	ElapsedKnown       bool     `json:"elapsed_known"`
	RefDurationSeconds float64  `json:"ref_duration_seconds"`
	ETASeconds         *float64 `json:"eta_seconds,omitempty"`
	ETAKind            string   `json:"eta_kind"`
	Pace               float64  `json:"pace"`
	MatchRate          float64  `json:"match_rate"`
	Confidence         string   `json:"confidence"`
	CurrentLines       int      `json:"current_lines"`
	MatchedLines       int      `json:"matched_lines"`
	NovelLines         int      `json:"novel_lines"`
	OverflowLines      int      `json:"overflow_lines"`
	// Auto-mode fields; omitted at their values so
	Identifying bool   `json:"identifying,omitempty"`
	Label       string `json:"pattern,omitempty"`
}

// writeJSONSnapshot writes s as JSON object
func writeJSONSnapshot(w io.Writer, s estimate.Estimate) error {
	js := jsonSnapshot{
		Progress:           s.Progress,
		UnitsDone:          s.UnitsDone,
		UnitsTotal:         s.UnitsTotal,
		UnitsPct:           s.UnitsPct * 100,
		HasTimes:           s.HasTimes,
		ElapsedSeconds:     s.Elapsed.Seconds(),
		ElapsedKnown:       s.ElapsedKnown,
		RefDurationSeconds: s.RefDuration.Seconds(),
		ETAKind:            s.ETAKind,
		Pace:               s.Pace,
		MatchRate:          s.MatchRate,
		Confidence:         s.Confidence,
		CurrentLines:       s.CurrentLines,
		MatchedLines:       s.MatchedLines,
		NovelLines:         s.NovelLines,
		OverflowLines:      s.OverflowLines,
		Identifying:        s.Identifying,
		Label:              s.Label,
	}
	if s.ETAKind != "none" {
		eta := s.ETA.Seconds()
		js.ETASeconds = &eta
	}
	buf, err := json.Marshal(js)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", buf)
	return err
}

// lineFeeder stamps live lines with a time and
type lineFeeder struct {
	est   *estimate.Estimator[string]
	stamp *estimate.Stamper
	wall  bool
}

// newLineFeeder reads the log's own clock through format. wall takes the time
// from the machine instead, which only a live stream may do: a file on disk
// was written when it was written.
func newLineFeeder(est *estimate.Estimator[string], format *estimate.TimeFormat, wall bool) *lineFeeder {
	return &lineFeeder{est: est, stamp: estimate.NewStamper(format), wall: wall}
}

func (f *lineFeeder) feed(line string) {
	at := time.Now()
	if !f.wall {
		at, _, _ = f.stamp.Stamp(line)
	}
	f.est.Observe(line, at)
}

// sourceName labels a live-learned run, e.g
func sourceName(mode string, args []string) string {
	name := mode + " " + time.Now().Format("2006-01-02 15:04:05")
	if len(args) > 0 {
		name += " " + strings.Join(args, " ")
	}
	return name
}
