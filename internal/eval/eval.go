// Package eval scores lpi against logs whose real answer is known: it replays
// a complete log line by line, compares every estimate with the truth that log
// records, and reports how wrong the estimates were.
package eval

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/wow-look-at-my/lpi/internal/fingerprint"
	"github.com/wow-look-at-my/lpi/internal/model"
	"github.com/wow-look-at-my/lpi/internal/progress"
	"github.com/wow-look-at-my/lpi/internal/timeparse"
)

// Target is a complete log: where to read it and its digest.
type Target struct {
	Path string
	Run  *model.Run
}

// Point is an estimate compared with the truth at that moment.
type Point struct {
	Line  int
	Truth float64
	Pred  float64
	// ETA and TrueLeft are set only for a timed run
	ETA      time.Duration
	TrueLeft time.Duration
	ETAKind  string
	Pace     float64
	Elapsed  time.Duration
}

// Err is positive when lpi claimed more progress than the run had made.
func (p Point) Err() float64 { return p.Pred - p.Truth }

// Result scores a held-out log.
type Result struct {
	Source string
	Lines  int
	// RefRuns is how many runs the model was built from
	Duration time.Duration
	HasTimes bool
	RefRuns  int
	SelfFit  bool

	MatchRate  float64
	Confidence string
	FinalPred  float64

	MeanAbsErr float64
	MedAbsErr  float64
	P90AbsErr  float64
	MaxAbsErr  float64
	WorstAt    float64
	Bias       float64

	// ETA errors cover the points where an ETA was
	ETAPoints     int
	ETAMeanAbsErr time.Duration
	ETAMeanRelErr float64

	Checkpoints []Point
}

// Grade names how good the progress estimates were.
func (r *Result) Grade() string { return Grade(r.MeanAbsErr) }

// Grade turns a mean absolute progress error into a word.
func Grade(mae float64) string {
	switch {
	case mae < 0.05:
		return "excellent"
	case mae < 0.10:
		return "good"
	case mae < 0.20:
		return "rough"
	default:
		return "poor"
	}
}

// checkpoints are the truth fractions the report samples the estimates at.
var checkpoints = []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1}

// LeaveOneOut scores every target against a model built from the OTHER
// targets, which is the only honest answer to "how good will it be on a run it
// has not seen". A single target is scored against itself and marked SelfFit.
func LeaveOneOut(targets []Target, format *timeparse.Format) ([]*Result, error) {
	if len(targets) == 0 {
		return nil, errors.New("eval: no logs given")
	}
	results := make([]*Result, 0, len(targets))
	for i, t := range targets {
		m := model.New("eval")
		for j, other := range targets {
			if j != i {
				m.AddRun(other.Run)
			}
		}
		selfFit := len(m.Runs) == 0
		if selfFit {
			m.AddRun(t.Run)
		}
		r, err := Score(m, t, format)
		if err != nil {
			return nil, err
		}
		r.SelfFit = selfFit
		results = append(results, r)
	}
	return results, nil
}

// Against scores every target against a stored model, which is the real
// holdout case: those logs were never merged into it.
func Against(m *model.Model, targets []Target, format *timeparse.Format) ([]*Result, error) {
	results := make([]*Result, 0, len(targets))
	for _, t := range targets {
		r, err := Score(m, t, format)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

// Score replays a complete log against m and measures every estimate it makes
// along the way.
func Score(m *model.Model, t Target, format *timeparse.Format) (*Result, error) {
	if t.Run == nil {
		return nil, fmt.Errorf("eval: %s: no digest", t.Path)
	}
	if t.Run.Lines < 2 {
		return nil, fmt.Errorf("eval: %s: too short to score", t.Path)
	}
	s := &scorer{
		res: &Result{
			Source:   t.Path,
			Lines:    t.Run.Lines,
			Duration: t.Run.Duration,
			HasTimes: t.Run.HasTimes,
			RefRuns:  len(m.Runs),
		},
		est:      progress.NewEstimator(m),
		run:      t.Run,
		lastLine: t.Run.Lines - 1,
	}
	if err := model.ReplayFile(t.Path, format.Clone(), s.line); err != nil {
		return nil, fmt.Errorf("eval: %s: %w", t.Path, err)
	}
	return s.finish(), nil
}

// scorer replays a log and accumulates the error at every line.
type scorer struct {
	res      *Result
	est      *progress.Estimator
	run      *model.Run
	lastLine int

	idx      int
	first    time.Time
	prev     time.Time
	haveT    bool
	errs     []float64
	sumErr   float64
	etaAbs   time.Duration
	etaRel   float64
	etaCount int
	next     int
	last     progress.Snapshot
}

func (s *scorer) line(text string, at time.Time) {
	// A line that normalizes to nothing never moved the digest clock, so the
	// replay must not let it move ours.
	if fingerprint.Normalize(text) == "" {
		return
	}
	s.est.Observe(text, s.clock(at))
	snap := s.est.Snapshot()
	p := Point{
		Line:    s.idx,
		Truth:   s.truth(),
		Pred:    snap.Progress,
		ETAKind: snap.ETAKind,
		Pace:    snap.Pace,
		Elapsed: snap.Elapsed,
	}
	s.idx++
	if s.run.HasTimes {
		p.TrueLeft = s.trueLeft()
		if snap.ETAKind != "none" {
			p.ETA = snap.ETA
			s.etaAbs += absDuration(p.ETA - p.TrueLeft)
			if p.TrueLeft > 0 {
				s.etaRel += math.Abs(float64(p.ETA-p.TrueLeft)) / float64(p.TrueLeft)
				s.etaCount++
			}
		}
	}
	s.record(p)
	s.last = snap
}

// clock mirrors the digester's effective clock, so the replay sees the same
// times the reference digest was built from.
func (s *scorer) clock(at time.Time) time.Time {
	if at.IsZero() {
		return s.prev // carry the previous line's time, unset before any stamp
	}
	if s.haveT && at.Before(s.prev) {
		at = s.prev
	}
	if !s.haveT {
		s.first = at
		s.haveT = true
	}
	s.prev = at
	return at
}

// truth is how much of the run is really done at this line: its share of the
// run's own clock when the log is timed, else its share of the line count.
func (s *scorer) truth() float64 {
	if s.run.HasTimes && s.run.Duration > 0 && s.haveT {
		return clamp01(float64(s.prev.Sub(s.first)) / float64(s.run.Duration))
	}
	if s.lastLine <= 0 {
		return 1
	}
	return clamp01(float64(s.idx) / float64(s.lastLine))
}

func (s *scorer) trueLeft() time.Duration {
	left := s.run.Duration - s.prev.Sub(s.first)
	if left < 0 {
		return 0
	}
	return left
}

// record keeps the error distribution and the checkpoint estimates.
func (s *scorer) record(p Point) {
	err := p.Err()
	s.errs = append(s.errs, math.Abs(err))
	s.sumErr += err
	if a := math.Abs(err); a > s.res.MaxAbsErr {
		s.res.MaxAbsErr = a
		s.res.WorstAt = p.Truth
	}
	for s.next < len(checkpoints) && p.Truth >= checkpoints[s.next] {
		s.res.Checkpoints = append(s.res.Checkpoints, p)
		s.next++
	}
}

func (s *scorer) finish() *Result {
	r := s.res
	n := len(s.errs)
	if n == 0 {
		return r
	}
	var sum float64
	for _, e := range s.errs {
		sum += e
	}
	r.MeanAbsErr = sum / float64(n)
	r.Bias = s.sumErr / float64(n)
	sorted := slices.Clone(s.errs)
	slices.Sort(sorted)
	r.MedAbsErr = sorted[n/2]
	r.P90AbsErr = sorted[min(n-1, (n*9)/10)]
	r.MatchRate = s.last.MatchRate
	r.Confidence = s.last.Confidence
	r.FinalPred = s.last.Progress
	if s.etaCount > 0 {
		r.ETAPoints = s.etaCount
		r.ETAMeanAbsErr = s.etaAbs / time.Duration(s.etaCount)
		r.ETAMeanRelErr = s.etaRel / float64(s.etaCount)
	}
	// The last line ends the run, so the report always carries a final row even
	// when the log's clock stops short of the last mark.
	if len(r.Checkpoints) < len(checkpoints) && n > 0 {
		r.Checkpoints = append(r.Checkpoints, Point{
			Line: s.idx - 1, Truth: 1, Pred: s.last.Progress,
			ETA: s.last.ETA, ETAKind: s.last.ETAKind, Pace: s.last.Pace,
			Elapsed: s.last.Elapsed,
		})
	}
	return r
}

// Overall aggregates a whole run of the suite.
type Overall struct {
	Runs          int
	SelfFit       bool
	MeanAbsErr    float64
	WorstAbsErr   float64
	MatchRate     float64
	ETAMeanRelErr float64
	ETARuns       int
}

// Grade names how good the suite was overall.
func (o Overall) Grade() string { return Grade(o.MeanAbsErr) }

// Aggregate averages the per-log scores.
func Aggregate(rs []*Result) Overall {
	var o Overall
	if len(rs) == 0 {
		return o
	}
	o.Runs = len(rs)
	for _, r := range rs {
		o.MeanAbsErr += r.MeanAbsErr
		o.MatchRate += r.MatchRate
		o.WorstAbsErr = math.Max(o.WorstAbsErr, r.MaxAbsErr)
		o.SelfFit = o.SelfFit || r.SelfFit
		if r.ETAPoints > 0 {
			o.ETAMeanRelErr += r.ETAMeanRelErr
			o.ETARuns++
		}
	}
	o.MeanAbsErr /= float64(len(rs))
	o.MatchRate /= float64(len(rs))
	if o.ETARuns > 0 {
		o.ETAMeanRelErr /= float64(o.ETARuns)
	}
	return o
}

func clamp01(f float64) float64 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	}
	return f
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
