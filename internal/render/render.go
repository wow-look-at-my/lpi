// Package render turns progress snapshots into terminal output: an in-place
// status line on TTYs, throttled plain lines otherwise, and a multi-line
// summary block.
package render

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/wow-look-at-my/log-progress-indicator/internal/progress"
)

// PlainInterval is the minimum time between non-TTY status lines (a whole
// percent of progress also forces one). It is a var so tests can shorten it.
var PlainInterval = 2 * time.Second

// IsTTY reports whether w is an interactive terminal. It is a var so tests
// can force either mode.
var IsTTY = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// barWidth is the interior width of the status-line progress bar.
const barWidth = 22

// Renderer writes live status lines to one writer. It is not safe for
// concurrent use; callers serialize Update/Close.
type Renderer struct {
	w         io.Writer
	tty       bool
	started   bool
	lastPlain time.Time
	lastPct   int
}

// New returns a Renderer for w, choosing TTY or plain mode via IsTTY.
func New(w io.Writer) *Renderer {
	return &Renderer{w: w, tty: IsTTY(w), lastPct: -1}
}

// Update renders one live snapshot. On a TTY the status line is repainted in
// place; otherwise a line is printed for the first update, then at most every
// PlainInterval or when the whole progress percent changes.
func (r *Renderer) Update(s progress.Snapshot) {
	if r.tty {
		fmt.Fprintf(r.w, "\r\x1b[K%s", StatusLine(s))
		return
	}
	pct := int(s.Progress * 100)
	now := time.Now()
	if r.started && pct == r.lastPct && now.Sub(r.lastPlain) < PlainInterval {
		return
	}
	r.started = true
	r.lastPct = pct
	r.lastPlain = now
	fmt.Fprintf(r.w, "%s\n", StatusLine(s))
}

// Close prints the final status line, ending the in-place repaint on a TTY.
func (r *Renderer) Close(final progress.Snapshot) {
	if r.tty {
		fmt.Fprintf(r.w, "\r\x1b[K%s\n", StatusLine(final))
		return
	}
	fmt.Fprintf(r.w, "%s\n", StatusLine(final))
}

// Bar renders a "[=====>    ]" progress bar with the given interior width.
// frac is clamped to 0..1.
func Bar(frac float64, width int) string {
	if width <= 0 {
		return "[]"
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac * float64(width))
	if filled >= width {
		return "[" + strings.Repeat("=", width) + "]"
	}
	return "[" + strings.Repeat("=", filled) + ">" + strings.Repeat(" ", width-filled-1) + "]"
}

// statusLabelMax caps the ref-label segment of the status line; pattern
// labels are command lines and can be arbitrarily long.
const statusLabelMax = 28

// StatusLine renders a single-line live status, e.g.
//
//	[========>             ] 38.4%  units 2451/5948 (41.2%)  elapsed 2m14s  eta ~3m35s  pace 1.07x  match 97%
//
// The elapsed segment is omitted when elapsed time is unknown, the eta
// segment when there is no ETA, and the pace segment when pace is 0. A
// snapshot from a locked pattern chooser (Label set) gains a trailing
// "ref <label>" segment, the label truncated to statusLabelMax bytes.
// While the chooser is still deciding (Identifying set) there is nothing
// to draw a bar against yet, so an identifying status is shown, e.g.
//
//	identifying pattern  lines 42  elapsed 3s
//
// Against an empty reference model (units total 0: a live-learning run
// recording the first baseline) the bar would be meaningless, so a
// recording status is shown instead, e.g.
//
//	recording baseline  lines 1234  elapsed 2m14s
//
// in both cases with the elapsed segment omitted when unknown.
func StatusLine(s progress.Snapshot) string {
	if s.Identifying {
		parts := []string{"identifying pattern", fmt.Sprintf("lines %d", s.CurrentLines)}
		if s.ElapsedKnown {
			parts = append(parts, "elapsed "+Duration(s.Elapsed))
		}
		return strings.Join(parts, "  ")
	}
	if s.UnitsTotal == 0 {
		parts := []string{"recording baseline", fmt.Sprintf("lines %d", s.CurrentLines)}
		if s.ElapsedKnown {
			parts = append(parts, "elapsed "+Duration(s.Elapsed))
		}
		return strings.Join(parts, "  ")
	}
	parts := []string{
		Bar(s.Progress, barWidth) + fmt.Sprintf(" %.1f%%", s.Progress*100),
		fmt.Sprintf("units %d/%d (%.1f%%)", s.UnitsDone, s.UnitsTotal, s.UnitsPct*100),
	}
	if s.ElapsedKnown {
		parts = append(parts, "elapsed "+Duration(s.Elapsed))
	}
	if s.ETAKind != "none" {
		parts = append(parts, "eta ~"+Duration(s.ETA))
	}
	if s.Pace != 0 {
		parts = append(parts, fmt.Sprintf("pace %.2fx", s.Pace))
	}
	parts = append(parts, fmt.Sprintf("match %.0f%%", s.MatchRate*100))
	if s.Label != "" {
		parts = append(parts, "ref "+truncLabel(s.Label, statusLabelMax))
	}
	return strings.Join(parts, "  ")
}

// truncLabel caps a pattern label at max bytes, appending "..." when cut
// (labels are command lines: ASCII in practice, so byte truncation is fine).
func truncLabel(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// Summary renders the multi-line, aligned summary block. It ends with a
// newline. Against an empty reference model there is nothing to estimate,
// so a short block notes that the run was recorded as the baseline (line
// count and elapsed time) instead. A snapshot carrying a pattern label
// (auto mode, locked) gains a final Pattern row naming it.
func Summary(s progress.Snapshot) string {
	var b strings.Builder
	row := func(key, val string) {
		fmt.Fprintf(&b, "%-12s %s\n", key+":", val)
	}
	if s.UnitsTotal == 0 {
		row("Reference", "none yet (recording baseline)")
		row("Lines", fmt.Sprintf("%d", s.CurrentLines))
		if s.ElapsedKnown {
			row("Elapsed", Duration(s.Elapsed))
		} else {
			row("Elapsed", "unknown")
		}
		return b.String()
	}
	row("Progress", fmt.Sprintf("%.1f%% (%s)", s.Progress*100, progressBasis(s)))
	row("Units", fmt.Sprintf("%d / %d reference lines matched (%.1f%%)",
		s.UnitsDone, s.UnitsTotal, s.UnitsPct*100))
	if s.ElapsedKnown {
		row("Elapsed", Duration(s.Elapsed))
	} else {
		row("Elapsed", "unknown")
	}
	switch s.ETAKind {
	case "pace":
		row("ETA", fmt.Sprintf("~%s (pace %.2fx vs reference)", Duration(s.ETA), s.Pace))
	case "ref-pace":
		row("ETA", fmt.Sprintf("~%s (assuming reference pace)", Duration(s.ETA)))
	}
	row("Confidence", fmt.Sprintf("%s (%.1f%% of lines matched; %d novel, %d overflow)",
		s.Confidence, s.MatchRate*100, s.NovelLines, s.OverflowLines))
	if s.HasTimes {
		row("Reference", fmt.Sprintf("%d units over %s", s.UnitsTotal, Duration(s.RefDuration)))
	} else {
		row("Reference", fmt.Sprintf("%d units, no timing data", s.UnitsTotal))
	}
	if s.Label != "" {
		row("Pattern", s.Label)
	}
	return b.String()
}

func progressBasis(s progress.Snapshot) string {
	if s.HasTimes {
		return "time-weighted"
	}
	return "by line position"
}

// Duration formats d rounded to seconds: sub-minute as "47s", sub-hour as
// "12m34s", and longer as "1h02m".
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	s := (d % time.Minute) / time.Second
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
