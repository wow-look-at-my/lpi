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

// detectLines is how many leading lines the live
const detectLines = 300

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
	cmd.Flags().StringVar(&rf.db, "db", model.DefaultDir(), "model database directory")
}

// addTimeFlags registers the timestamp-reading flags for commands that digest
// or follow a log on disk.
func addTimeFlags(cmd *cobra.Command, rf *refFlags) {
	cmd.Flags().StringVar(&rf.format, "format", "",
		"how to read each line's timestamp: auto (default), a builtin ("+
			strings.Join(timeparse.Names(), ", ")+"), or a regex with named groups ("+
			strings.Join(timeparse.Groups(), ", ")+")")
	cmd.Flags().StringVar(&rf.layout, "time-layout", "",
		"Go reference layout for the regex 'time' group, or for the start of each line")
}

// timeFormat compiles --format/--time-layout. A nil format asks for detection.
func (rf *refFlags) timeFormat() (*timeparse.Format, error) {
	return timeparse.Compile(rf.format, rf.layout)
}

// addRefFlags registers --ref on top of the model
func addRefFlags(cmd *cobra.Command, rf *refFlags) {
	addModelFlags(cmd, rf)
	cmd.Flags().StringArrayVar(&rf.refs, "ref", nil,
		"reference log of a completed run (repeatable; gzip is handled transparently)")
}

// resolve builds the reference model: --key loads
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
	format, err := rf.timeFormat()
	if err != nil {
		return nil, err
	}
	for _, ref := range rf.refs {
		run, err := model.DigestFileWith(ref, format.Clone())
		if err != nil {
			return nil, fmt.Errorf("digest %s: %w", ref, err)
		}
		m.AddRun(run)
	}
	return m, nil
}

// resolveOrBootstrap resolves the reference model
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

// loadOrCreate returns the model stored for key, or
func loadOrCreate(db, key string) (*model.Model, error) {
	m, err := model.Load(model.PathForKey(db, key))
	if os.IsNotExist(err) {
		return model.New(key), nil
	}
	return m, err
}

// learnRun adds run to key's stored model, records
func learnRun(w io.Writer, db, key string, run *model.Run, invocation string) error {
	m, err := loadOrCreate(db, key)
	if err != nil {
		return err
	}
	m.AddRun(run)
	m.AddInvocation(invocation)
	path := model.PathForKey(db, key)
	if err := m.Save(path); err != nil {
		return err
	}
	fmt.Fprintf(w, "learned run (%d lines, %s) into key %q (%d runs)\n",
		run.Lines, render.Duration(run.Duration), key, len(m.Runs))
	return nil
}

// newCapture opens the durable capture file for a
func newCapture(msg notify, db, key, source string) *model.CaptureWriter {
	cw, err := model.NewCaptureWriter(db, key, source)
	if err != nil {
		msg("warning: capture file disabled: %v", err)
		return nil
	}
	return cw
}

// keepCapture closes the capture file, leaves it in
func keepCapture(msg notify, cw *model.CaptureWriter, db, key string) {
	if cw == nil {
		return
	}
	_ = cw.Close()
	msg("captured log kept: %s", cw.Path())
	msg("learn it later with: lpi learn --key %s --db %s %s", key, db, cw.Path())
}

// keepOrDiscardCapture keeps the capture file with
func keepOrDiscardCapture(msg notify, dig *model.Digester, cw *model.CaptureWriter, db, key string) {
	if _, err := dig.Finish(); err != nil {
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

// sourceName labels a live-learned run, e.g
func sourceName(mode string, args []string) string {
	name := mode + " " + time.Now().Format("2006-01-02 15:04:05")
	if len(args) > 0 {
		name += " " + strings.Join(args, " ")
	}
	return name
}
