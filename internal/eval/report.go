package eval

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/lpi/internal/render"
)

// Report writes the human-facing scorecard for a scored suite.
func Report(w io.Writer, rs []*Result, detail bool) {
	if len(rs) == 0 {
		fmt.Fprintln(w, "nothing to score")
		return
	}
	fmt.Fprintf(w, "%-24s %8s %9s %8s %8s %8s %9s %7s\n",
		"log", "lines", "duration", "err avg", "err p90", "err max", "eta err", "match")
	for _, r := range rs {
		fmt.Fprintf(w, "%-24s %8d %9s %7.1f%% %7.1f%% %7.1f%% %9s %6.0f%%\n",
			trimName(r.Source, 24), r.Lines, durationOf(r), pct(r.MeanAbsErr), pct(r.P90AbsErr),
			pct(r.MaxAbsErr), etaCell(r), r.MatchRate*100)
	}
	o := Aggregate(rs)
	if len(rs) > 1 {
		fmt.Fprintf(w, "%-24s %8s %9s %7.1f%% %8s %7.1f%% %9s %6.0f%%\n",
			"all", "", "", pct(o.MeanAbsErr), "", pct(o.WorstAbsErr), etaOverall(o), o.MatchRate*100)
	}
	fmt.Fprintln(w)
	if detail {
		for _, r := range rs {
			writeCheckpoints(w, r)
		}
	}
	writeVerdict(w, rs, o)
}

// writeCheckpoints prints what lpi would have told you as the run went by.
func writeCheckpoints(w io.Writer, r *Result) {
	fmt.Fprintf(w, "%s -- what lpi said as the run went by\n", r.Source)
	fmt.Fprintf(w, "  %8s %8s %8s %9s %9s %7s\n", "true", "said", "error", "eta", "true left", "pace")
	for _, p := range r.Checkpoints {
		eta, left := "-", "-"
		if p.ETAKind != "none" && r.HasTimes {
			eta = render.Duration(p.ETA)
			left = render.Duration(p.TrueLeft)
		}
		fmt.Fprintf(w, "  %7.0f%% %7.1f%% %+7.1f%% %9s %9s %6.2fx\n",
			p.Truth*100, p.Pred*100, p.Err()*100, eta, left, p.Pace)
	}
	fmt.Fprintln(w)
}

// writeVerdict says in words how much to trust the estimates.
func writeVerdict(w io.Writer, rs []*Result, o Overall) {
	fmt.Fprintf(w, "verdict: %s -- progress is off by %.1f%% on average", o.Grade(), pct(o.MeanAbsErr))
	if o.ETARuns > 0 {
		fmt.Fprintf(w, ", and the ETA by %.0f%% of the time actually left", o.ETAMeanRelErr*100)
	}
	fmt.Fprintln(w, ".")
	if o.SelfFit {
		fmt.Fprintln(w, "note: one log scored against itself -- the score is optimistic. "+
			"Pass two or more logs of the same task to score each against the others.")
	}
	for _, r := range rs {
		if !r.HasTimes {
			fmt.Fprintf(w, "note: %s carries no timestamps, so its truth is the line count and it has no ETA.\n",
				trimName(r.Source, 40))
		}
	}
}

func durationOf(r *Result) string {
	if !r.HasTimes {
		return "untimed"
	}
	return render.Duration(r.Duration)
}

func etaCell(r *Result) string {
	if r.ETAPoints == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", r.ETAMeanRelErr*100)
}

func etaOverall(o Overall) string {
	if o.ETARuns == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", o.ETAMeanRelErr*100)
}

func pct(f float64) float64 { return f * 100 }

// trimName keeps the tail of a path, which is the part that identifies the log.
func trimName(s string, max int) string {
	if len(s) <= max {
		return s
	}
	base := filepath.Base(s)
	if len(base) <= max {
		return base
	}
	return "..." + base[len(base)-(max-3):]
}

// JSONResult is the stable JSON form of a scored log.
type JSONResult struct {
	Source        string           `json:"source"`
	Lines         int              `json:"lines"`
	DurationSecs  float64          `json:"duration_seconds"`
	HasTimes      bool             `json:"has_times"`
	RefRuns       int              `json:"ref_runs"`
	SelfFit       bool             `json:"self_fit"`
	MatchRate     float64          `json:"match_rate"`
	Confidence    string           `json:"confidence"`
	FinalProgress float64          `json:"final_progress"`
	MeanAbsErr    float64          `json:"err_mean"`
	MedAbsErr     float64          `json:"err_median"`
	P90AbsErr     float64          `json:"err_p90"`
	MaxAbsErr     float64          `json:"err_max"`
	WorstAt       float64          `json:"err_max_at"`
	Bias          float64          `json:"err_bias"`
	ETAMeanRelErr float64          `json:"eta_rel_err,omitempty"`
	ETAMeanAbsSec float64          `json:"eta_abs_err_seconds,omitempty"`
	Grade         string           `json:"grade"`
	Checkpoints   []JSONCheckpoint `json:"checkpoints"`
}

// JSONCheckpoint is an estimate taken at a mark along the run.
type JSONCheckpoint struct {
	Truth        float64 `json:"true"`
	Pred         float64 `json:"said"`
	Err          float64 `json:"error"`
	ETASecs      float64 `json:"eta_seconds,omitempty"`
	TrueLeftSecs float64 `json:"true_left_seconds,omitempty"`
	Pace         float64 `json:"pace,omitempty"`
}

// JSON converts a result into its stable JSON form.
func JSON(r *Result) JSONResult {
	j := JSONResult{
		Source: r.Source, Lines: r.Lines, DurationSecs: r.Duration.Seconds(),
		HasTimes: r.HasTimes, RefRuns: r.RefRuns, SelfFit: r.SelfFit,
		MatchRate: r.MatchRate, Confidence: r.Confidence, FinalProgress: r.FinalPred,
		MeanAbsErr: r.MeanAbsErr, MedAbsErr: r.MedAbsErr, P90AbsErr: r.P90AbsErr,
		MaxAbsErr: r.MaxAbsErr, WorstAt: r.WorstAt, Bias: r.Bias,
		ETAMeanRelErr: r.ETAMeanRelErr, ETAMeanAbsSec: r.ETAMeanAbsErr.Seconds(),
		Grade: r.Grade(),
	}
	for _, p := range r.Checkpoints {
		c := JSONCheckpoint{Truth: p.Truth, Pred: p.Pred, Err: p.Err(), Pace: p.Pace}
		if p.ETAKind != "none" && r.HasTimes {
			c.ETASecs = p.ETA.Seconds()
			c.TrueLeftSecs = p.TrueLeft.Seconds()
		}
		j.Checkpoints = append(j.Checkpoints, c)
	}
	return j
}

// Verdict is the summary line the JSON output carries alongside the runs.
func Verdict(o Overall) string {
	parts := []string{fmt.Sprintf("progress off by %.1f%% on average", o.MeanAbsErr*100)}
	if o.ETARuns > 0 {
		parts = append(parts, fmt.Sprintf("eta off by %.0f%% of the time left", o.ETAMeanRelErr*100))
	}
	if o.SelfFit {
		parts = append(parts, "scored against itself, so optimistic")
	}
	return o.Grade() + ": " + strings.Join(parts, ", ")
}
