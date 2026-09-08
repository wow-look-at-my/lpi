package estimate

import "time"

// Observer is what a live run is fed to: Estimator and Matcher both satisfy it.
type Observer interface {
	Observe(tok Token, at time.Time)
	ObserveLine(line string, at time.Time)
	Tick(at time.Time)
	Estimate() Estimate
}

// Sink feeds a live run to all of the Observer scoring it, the Recorder
// learning it, and the Capture keeping it recoverable.
type Sink struct {
	Obs Observer
	Rec *Recorder
	Cap *Capture
}

// ObserveLine feeds raw log text to each. The error is the capture's alone: a
// capture that cannot be written costs recoverability, never the run.
func (s *Sink) ObserveLine(line string, at time.Time) error {
	s.Obs.ObserveLine(line, at)
	if s.Rec == nil {
		return nil
	}
	s.Rec.ObserveLine(line, at)
	return s.Cap.Add(line, at)
}
