package estimate_test

import (
	"fmt"
	"time"

	"github.com/wow-look-at-my/lpi/estimate"
)

// Estimating a task from the tokens it emits: record a completed run, then
// score a live run against it. The tokens here are step names, but anything
// repeatable works -- a test id, a filename, an event type, a log line.
func Example() {
	// The caller's own type: estimate.Identifiers reads any string-shaped type.
	type Stage string
	stages := []Stage{"fetch", "resolve", "unpack", "generate", "compile",
		"link", "test", "lint", "package", "publish"}
	start := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	at := func(i int) time.Time { return start.Add(time.Duration(i*10) * time.Second) }

	// A run that already finished: its stages, evenly spaced.
	rec := estimate.NewRecorder("release build", estimate.Identifiers[Stage])
	for i, stage := range stages {
		rec.Observe(stage, at(i))
	}
	run, err := rec.Finish()
	if err != nil {
		panic(err)
	}
	m := estimate.NewModel("release")
	m.Add(run)

	// A live run, part way through the same stages.
	est := estimate.NewEstimator(m, estimate.Identifiers[Stage])
	for i, stage := range stages[:6] {
		est.Observe(stage, at(i))
	}

	// e.Progress, e.UnitsDone, e.UnitsTotal and e.ETA carry the numbers.
	e := est.Estimate()
	fmt.Println(e.Confidence, e.ETAKind)
	// Output: high pace
}
