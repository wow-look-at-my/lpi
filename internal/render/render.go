// Package render turns progress snapshots into
package render

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/lpi/internal/progress"
)

// PlainInterval is the minimum time between non-TTY
var PlainInterval = 2 * time.Second

// IsTTY reports whether w is an interactive terminal
var IsTTY = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// barWidth is the interior width of the status-line
const barWidth = 22

// Renderer writes live status lines to writer
type Renderer struct {
	w         io.Writer
	tty       bool
	started   bool
	lastPlain time.Time
	lastPct   int
	last      string // last painted TTY status, repainted after
	painted   bool   // TTY: a status line is currently on the terminal
	midline   bool   // passthrough bytes left the display cursor inside
}

// New returns a Renderer for w, choosing TTY or
func New(w io.Writer) *Renderer {
	return &Renderer{w: w, tty: IsTTY(w), lastPct: -1}
}

// Update renders live snapshot
func (r *Renderer) Update(s progress.Snapshot) {
	if r.tty {
		r.paint(StatusLine(s))
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
	r.printPlain(StatusLine(s))
}

// Close prints the final status line, ending the
func (r *Renderer) Close(final progress.Snapshot) {
	if r.tty {
		r.paint(StatusLine(final))
		fmt.Fprint(r.w, "\n")
		r.painted = false
		r.last = ""
		return
	}
	r.printPlain(StatusLine(final))
}

// Message writes of the caller's own out-of-band
func (r *Renderer) Message(msg string) {
	if r.painted {
		fmt.Fprint(r.w, "\r\x1b[K")
		r.painted = false
	}
	if r.midline {
		fmt.Fprint(r.w, "\n")
		r.midline = false
	}
	fmt.Fprintf(r.w, "%s\n", msg)
}

// Break ends any in-progress terminal line -- a
func (r *Renderer) Break() {
	if r.painted || r.midline {
		fmt.Fprint(r.w, "\n")
	}
	r.painted = false
	r.midline = false
	r.last = ""
}

// paint draws line as the current TTY status
func (r *Renderer) paint(line string) {
	if r.midline {
		fmt.Fprint(r.w, "\n")
		r.midline = false
	}
	fmt.Fprintf(r.w, "\r\x1b[K%s", line)
	r.last = line
	r.painted = true
}

// printPlain writes complete status line in plain
func (r *Renderer) printPlain(line string) {
	if r.midline {
		fmt.Fprint(r.w, "\n")
		r.midline = false
	}
	fmt.Fprintf(r.w, "%s\n", line)
}

// Passthrough wraps dst so that child output
func (r *Renderer) Passthrough(dst io.Writer, mu *sync.Mutex) io.Writer {
	if !sameWriter(dst, r.w) && !(r.tty && IsTTY(dst)) {
		return dst
	}
	return &passthroughWriter{r: r, dst: dst, mu: mu}
}

// sameWriter reports whether a and b are the same
func sameWriter(a, b io.Writer) bool {
	return reflect.TypeOf(a).Comparable() && a == b
}

// passthroughWriter is the coordinated writer built
type passthroughWriter struct {
	r   *Renderer
	dst io.Writer
	mu  *sync.Mutex
}

func (pw *passthroughWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	pw.mu.Lock()
	defer pw.mu.Unlock()
	r := pw.r
	if r.painted {
		fmt.Fprint(r.w, "\r\x1b[K")
		r.painted = false
	}
	n, err := pw.dst.Write(p)
	if n > 0 {
		r.midline = p[n-1] != '\n'
	}
	// Repainting onto a partial child line would glue
	if err == nil && r.tty && !r.midline && r.last != "" {
		r.paint(r.last)
	}
	return n, err
}

// Bar renders a "[=====> ]" progress bar with the
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

// statusLabelMax caps the ref-label segment of the
const statusLabelMax = 28

// StatusLine renders a single-line live status, e.g
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

// truncLabel caps a pattern label at max bytes
func truncLabel(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// Summary renders the multi-line, aligned summary
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

// Duration formats d rounded to seconds: sub-minute
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
