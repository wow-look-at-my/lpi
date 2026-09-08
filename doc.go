// Package lpi estimates completion, remaining work and ETA for anything that
// emits semi-unique tokens as it runs.
//
// A token is any repeatable marker a task emits while it works: a log line, a
// test name, a step id, a processed filename, a state transition, a message
// subject. The requirement is only that a run emits roughly the same multiset
// of tokens as the runs before it, and that the tokens are varied enough to
// place a run inside the whole. Nothing here is specific to logs.
//
// The shape of the API is the shape of the method. Record complete runs into a
// Model, then feed a live run's tokens to an Estimator built from that Model
// and read an Estimate whenever you want to draw something:
//
//	m := lpi.NewModel("nightly-import")
//	run, err := rec.Finish() // a Recorder fed with a completed run's tokens
//	m.Add(run)
//	est := lpi.NewEstimator(m)
//	est.Observe(lpi.TokenOf(event.Name), event.At)
//	e := est.Estimate() // e.Progress, e.ETA, e.Confidence
//
// Timestamps are optional. Pass an unset time.Time for a stream with no clock
// and each token weighs the same, which still gives progress and units but no
// ETA. A live stream with no timestamps of its own can call Tick with the wall
// clock to get an elapsed time and a paced ETA.
//
// Use TokenOf for tokens that are already identifiers. Use TokenOfLine for raw
// log text, which normalizes away timestamps, counters, hex ids, UUIDs and ANSI
// before hashing, so lines that differ only in noise share one token.
//
// A Model is a small gzipped file. Store gives it the same on-disk database the
// lpi command line uses, so a library caller and the CLI can share references.
// Matcher identifies a live run against many models at once when the caller
// does not know which reference applies.
//
// No type here is safe for concurrent use. One Estimator belongs to one live
// run, and a caller feeding it from several goroutines owns the lock.
package lpi
