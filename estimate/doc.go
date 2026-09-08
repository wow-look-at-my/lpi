// Package estimate reports completion, remaining work and ETA for anything that
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
//	type Step string // the caller's own type
//	m := estimate.NewModel("nightly-import")
//	run, err := rec.Finish() // a Recorder fed with a completed run's steps
//	m.Add(run)
//	est := estimate.NewEstimator(m, estimate.Identifiers[Step])
//	est.Observe(event.Step, event.At)
//	e := est.Estimate() // e.Progress, e.ETA, e.Confidence
//
// Timestamps are optional. Pass an unset time.Time for a stream with no clock
// and each token weighs the same, which still gives progress and units but no
// ETA. A live stream with no timestamps of its own can call Tick with the wall
// clock to get an elapsed time and a paced ETA.
//
// Recorder, Estimator and Matcher are generic over the caller's own value
// type, and take a Tokenizer that reduces it to a Token. Identifiers is the
// Tokenizer for a value that already identifies its work, over any
// string-shaped type. TokenOfLine is the Tokenizer for raw log text: it
// normalizes away timestamps, counters, hex ids, UUIDs and ANSI before hashing,
// so lines that differ only in noise share a token.
//
// A Model is a small gzipped file. Store gives it the same on-disk database the
// lpi command line uses, so a library caller and the CLI can share references.
// Matcher identifies a live run against many models in parallel, for a caller
// that does not know which reference applies.
//
// No type here is safe for concurrent use. An Estimator belongs to the live run
// it scores, and a caller feeding it from several goroutines owns the lock.
//
// The guide, with worked examples, is docs/LIBRARY.md in this repository.
package estimate
