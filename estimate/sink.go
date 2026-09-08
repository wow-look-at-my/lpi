package estimate

import "time"

// Observer is what a live run is fed to: Estimator and Matcher both satisfy it.
type Observer[T any] interface {
	Observe(v T, at time.Time)
	Tick(at time.Time)
	Estimate() Estimate
}

// Sink feeds a live run to the Observer scoring it, the Recorder learning it,
// and the Capture keeping it. Text, being what a Capture stores.
type Sink[T ~string] struct {
	Obs Observer[T]
	Rec *Recorder[T]
	Cap *Capture
}

// Observe feeds a value to each. The error is the capture's alone: a capture
// that cannot be written costs recoverability, never the run.
func (s *Sink[T]) Observe(v T, at time.Time) error {
	s.Obs.Observe(v, at)
	if s.Rec == nil {
		return nil
	}
	s.Rec.Observe(v, at)
	return s.Cap.Add(string(v), at)
}
