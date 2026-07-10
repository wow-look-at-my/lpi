package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/log-progress-indicator/internal/model"
	"github.com/wow-look-at-my/log-progress-indicator/internal/progress"
	"github.com/wow-look-at-my/log-progress-indicator/internal/render"
	"github.com/wow-look-at-my/log-progress-indicator/internal/timeparse"
)

// detectLines is how many leading lines the live modes sample for timestamp
// format detection, mirroring model.DigestFile.
const detectLines = 300

// tickInterval is how often the live modes advance the wall clock and
// repaint between lines. It is a var so tests can shorten it.
var tickInterval = 500 * time.Millisecond

// refFlags holds the flags every estimating command shares: where the
// reference model comes from.
type refFlags struct {
	refs []string
	key  string
	db   string
}

// addModelFlags registers --key and --db.
func addModelFlags(cmd *cobra.Command, rf *refFlags) {
	cmd.Flags().StringVar(&rf.key, "key", "", "name of a learned model in the model database")
	cmd.Flags().StringVar(&rf.db, "db", model.DefaultDir(), "model database directory")
}

// addRefFlags registers --ref on top of the model flags.
func addRefFlags(cmd *cobra.Command, rf *refFlags) {
	addModelFlags(cmd, rf)
	cmd.Flags().StringArrayVar(&rf.refs, "ref", nil,
		"reference log of a completed run (repeatable; gzip is handled transparently)")
}

// resolve builds the reference model: --key loads it from the database, and
// each --ref log is digested and added on top (in memory only). At least one
// of the two must be given.
func (rf *refFlags) resolve() (*model.Model, error) {
	if rf.key == "" && len(rf.refs) == 0 {
		return nil, errors.New("no reference given: use --key NAME and/or --ref FILE")
	}
	var m *model.Model
	if rf.key != "" {
		var err error
		if m, err = model.Load(model.PathForKey(rf.db, rf.key)); err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("no model for key %q in %s (%s)",
					rf.key, rf.db, availableKeys(rf.db))
			}
			return nil, err
		}
	} else {
		m = model.New("adhoc")
	}
	for _, ref := range rf.refs {
		run, err := model.DigestFile(ref)
		if err != nil {
			return nil, fmt.Errorf("digest %s: %w", ref, err)
		}
		m.AddRun(run)
	}
	return m, nil
}

// resolveOrBootstrap resolves the reference model for a live-learning mode,
// where learnKey is the key the run will be saved into. It behaves exactly
// like resolve except for the bootstrap case: when no --ref is given and
// the reference key -- --key when set, else learnKey -- is the learn target
// itself but has no stored model yet, it returns a fresh empty model and
// bootstrap=true instead of an error. The caller then records the run as
// the key's first baseline, so the next invocation has a real reference.
// An explicit --key naming a different key than learnKey must still exist,
// as must --key whenever a --ref is also given.
func (rf *refFlags) resolveOrBootstrap(learnKey string) (m *model.Model, bootstrap bool, err error) {
	if len(rf.refs) > 0 || (rf.key != "" && rf.key != learnKey) {
		m, err = rf.resolve()
		return m, false, err
	}
	m, err = model.Load(model.PathForKey(rf.db, learnKey))
	if os.IsNotExist(err) {
		return model.New(learnKey), true, nil
	}
	return m, false, err
}

// bootstrapNotice tells the user why no progress will be shown this run.
func bootstrapNotice(w io.Writer, key string) {
	fmt.Fprintf(w, "no model for key %q yet -- recording baseline run\n", key)
}

// notify delivers one of lpi's own out-of-band lines -- a capture warning,
// pipe's interrupt notice, the printed recovery command. Live paths back it
// with the renderer's Message (called under the same mutex that serializes
// rendering) so the line never lands glued onto a painted status or a
// partial child line; paths with no renderer print plainly.
type notify func(format string, args ...any)

// plainNotify prints out-of-band lines directly to w, one whole line each,
// for paths with no active renderer (e.g. a json-stream pipe).
func plainNotify(w io.Writer) notify {
	return func(format string, args ...any) {
		fmt.Fprintf(w, format+"\n", args...)
	}
}

// renderNotify routes out-of-band lines through r.Message so they respect
// the status line's never-share-a-line discipline. While other goroutines
// are live, callers must hold the mutex that serializes the renderer.
func renderNotify(r *render.Renderer) notify {
	return func(format string, args ...any) {
		r.Message(fmt.Sprintf(format, args...))
	}
}

// availableKeys names the models present in db, for error messages.
func availableKeys(db string) string {
	entries, err := os.ReadDir(db)
	var keys []string
	if err == nil {
		for _, e := range entries {
			if name, ok := strings.CutSuffix(e.Name(), ".lpi"); ok && !e.IsDir() {
				keys = append(keys, name)
			}
		}
	}
	if len(keys) == 0 {
		return "no models learned yet"
	}
	sort.Strings(keys)
	return "available: " + strings.Join(keys, ", ")
}

// loadOrCreate returns the model stored for key, or a fresh one when the
// database has no entry yet.
func loadOrCreate(db, key string) (*model.Model, error) {
	m, err := model.Load(model.PathForKey(db, key))
	if os.IsNotExist(err) {
		return model.New(key), nil
	}
	return m, err
}

// learnRun adds run to key's stored model, saves it, and prints the
// confirmation line used by pipe and run.
func learnRun(w io.Writer, db, key string, run *model.Run) error {
	m, err := loadOrCreate(db, key)
	if err != nil {
		return err
	}
	m.AddRun(run)
	path := model.PathForKey(db, key)
	if err := m.Save(path); err != nil {
		return err
	}
	fmt.Fprintf(w, "learned run (%d lines, %s) into key %q (%d runs)\n",
		run.Lines, render.Duration(run.Duration), key, len(m.Runs))
	return nil
}

// newCapture opens the durable capture file for a learning run, warning and
// returning nil (capture disabled) when it cannot be created -- the recovery
// feature must never break the primary flow. All *CaptureWriter methods are
// nil-safe, so callers need no branches.
func newCapture(msg notify, db, key, source string) *model.CaptureWriter {
	cw, err := model.NewCaptureWriter(db, key, source)
	if err != nil {
		msg("warning: capture file disabled: %v", err)
		return nil
	}
	return cw
}

// keepCapture closes the capture file, leaves it in place, and prints the
// recovery instructions used by run and pipe when a learning run fails.
// A nil capture (creation failed earlier) prints nothing.
func keepCapture(msg notify, cw *model.CaptureWriter, db, key string) {
	if cw == nil {
		return
	}
	_ = cw.Close()
	msg("captured log kept: %s", cw.Path())
	msg("learn it later with: lpi learn --key %s --db %s %s", key, db, cw.Path())
}

// keepOrDiscardCapture keeps the capture file with recovery instructions
// when dig holds anything recoverable, and removes it otherwise: with fewer
// than 2 nonempty lines the printed recovery command could never succeed,
// and a hint that cannot work is worse than none.
func keepOrDiscardCapture(msg notify, dig *model.Digester, cw *model.CaptureWriter, db, key string) {
	if _, err := dig.Finish(); err != nil {
		cw.Discard()
		return
	}
	keepCapture(msg, cw, db, key)
}

// jsonSnapshot is the stable JSON form of a progress snapshot, shared by
// --json and --json-stream. eta_seconds is only present when there is an
// ETA.
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
}

// writeJSONSnapshot writes s as one JSON object followed by a newline.
func writeJSONSnapshot(w io.Writer, s progress.Snapshot) error {
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

// lineFeeder stamps live lines with a time and feeds them to an estimator:
// wall-clock time in wall mode, else times parsed by format (carrying the
// previous stamp forward across unparsable lines), else no time at all.
type lineFeeder struct {
	est    *progress.Estimator
	format *timeparse.Format
	wall   bool
	last   time.Time
}

func (f *lineFeeder) feed(line string) {
	var at time.Time
	switch {
	case f.wall:
		at = time.Now()
	case f.format != nil:
		if t, ok := f.format.Parse(line); ok {
			f.last = t
		}
		at = f.last
	}
	f.est.Observe(line, at)
}

// sourceName labels a live-learned run, e.g. "run 2026-07-02 15:04:05 make".
func sourceName(mode string, args []string) string {
	name := mode + " " + time.Now().Format("2006-01-02 15:04:05")
	if len(args) > 0 {
		name += " " + strings.Join(args, " ")
	}
	return name
}
