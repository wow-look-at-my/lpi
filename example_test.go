package lpi_test

import (
	"fmt"
	"time"

	"github.com/wow-look-at-my/lpi"
)

// Estimating a task from the tokens it emits: record one completed run, then
// score a live one against it. The tokens here are step names, but anything
// repeatable works -- a test id, a filename, an event type, a log line.
func Example() {
	steps := []string{"fetch", "resolve", "unpack", "generate", "compile",
		"link", "test", "lint", "package", "publish"}
	start := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	at := func(i int) time.Time { return start.Add(time.Duration(i*10) * time.Second) }

	// A run that already finished: 10 steps, 10 seconds apart.
	rec := lpi.NewRecorder("release build")
	for i, step := range steps {
		rec.Observe(lpi.TokenOf(step), at(i))
	}
	run, err := rec.Finish()
	if err != nil {
		panic(err)
	}
	m := lpi.NewModel("release")
	m.Add(run)

	// A live run, six steps in.
	est := lpi.NewEstimator(m)
	for i, step := range steps[:6] {
		est.Observe(lpi.TokenOf(step), at(i))
	}

	e := est.Estimate()
	fmt.Printf("%.0f%% done, %d/%d units, eta %s, %s confidence\n",
		e.Progress*100, e.UnitsDone, e.UnitsTotal, e.ETA.Round(time.Second), e.Confidence)
	// Output: 56% done, 6/10 units, eta 40s, high confidence
}
