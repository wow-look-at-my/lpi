# lpi as a Go library

The command line wraps a general estimator. Give it a stream of semi-unique tokens and recordings of previous complete streams. It tells you how far along this stream is, and how long is left. Logs are the CLI's subject, not the estimator's. The root package `github.com/wow-look-at-my/lpi` is that estimator. The log-specific parts are optional.

```sh
go get github.com/wow-look-at-my/lpi
```

## What counts as a token

A token is any repeatable marker a task emits while it works. A test name, a migration id, a processed filename, a queue subject, a state transition, a log line. Two properties make a stream estimable:

- **Semi-unique.** A run emits a varied vocabulary, not the same token again and again. A stream of one repeated token places nothing, and `Confidence` says so.
- **Repeatable.** The next run emits approximately the same multiset. Order does not matter. Matching is order-free, so parallel and interleaved work is fine.

`TokenOf(s)` hashes an identifier the caller already holds. Anything that varies between runs must be out of it already. If it is not, no run matches any other run. `TokenOfLine(s)` is the log path. It strips ANSI, collapses timestamps, counters, hex hashes and UUIDs, then hashes. So `10:04:07 [ 62%] Building tls.c.o` and `09:31:02 [ 58%] Building tls.c.o` are the same token. `Normalize` gives you the template it hashed, for tests and for logging.

## The types

| type | what it is |
|---|---|
| `Recorder` | digests one COMPLETED run into a `*Run` |
| `Model` | the merged expectation, built from up to `MaxRuns` runs, saved as one small file |
| `Estimator` | scores a LIVE run against a `Model` and answers with an `Estimate` |
| `Matcher` | scores a live run against MANY models, and locks onto the one it fits |
| `Store` | a directory of models -- the same database the CLI reads and writes |

## Record a run

```go
rec := lpi.NewRecorder("nightly-import")
for ev := range events {
	rec.Observe(lpi.TokenOf(ev.Step), ev.At)
}
run, err := rec.Finish() // err under 2 tokens: a run of one token places nothing

m := lpi.NewModel("nightly-import")
m.Add(run)
lpi.OpenStore("").Save(m) // "" means the CLI's own database
```

`Model.Add` merges. Expected counts take the upper median across runs. Times and weights average in seconds, so a short or partial run cannot inflate its share. Beyond `MaxRuns` the oldest run is evicted. `AddLabel` records a human-facing name. `Label` reports it, and `lpi model list` shows it.

## Estimate a live run

```go
m, err := lpi.OpenStore("").Load("nightly-import")
if errors.Is(err, fs.ErrNotExist) {
	// Never recorded. Record this run as the baseline instead of estimating it.
}
est := lpi.NewEstimator(m)
for ev := range events {
	est.Observe(lpi.TokenOf(ev.Step), ev.At)
	e := est.Estimate()
	fmt.Printf("%.1f%% eta %s (%s)\n", e.Progress*100, e.ETA, e.Confidence)
}
```

`Estimate` returns a value. It is cheap enough to read on every token, or on a redraw timer. The fields that matter:

| field | meaning |
|---|---|
| `Progress` | 0..1, the share of the reference run's TIME covered. The primary number. |
| `UnitsDone` / `UnitsTotal` | token occurrences matched / expected |
| `ETA`, `ETAKind` | time left. Valid only when `ETAKind` is not `"none"`. |
| `Elapsed`, `ElapsedKnown`, `Pace` | the live clock, and how it compares to the reference |
| `MatchRate`, `Confidence` | share of live tokens the reference knew: `high`, `medium`, `low`, `none` |
| `NovelLines`, `OverflowLines` | tokens the reference never saw / saw fewer times than this run |
| `Identifying`, `Label` | `Matcher` state: still picking, and the pattern it picked |

Read `Confidence` before you draw anything. A run that emits mostly tokens the reference never saw is not 3% done. It is a different run, and `low` is the estimator saying so.

## Clocks are optional

- **The tokens carry their own time.** Pass it to `Observe`. Elapsed time, pace and a paced ETA then come from the stream itself. This is what makes a replayed recording score the same as a live run.
- **The caller holds the clock.** Pass an unset `time.Time` and call `Tick(time.Now())` on a timer. The estimate gets its elapsed time from the ticks.
- **There is no clock at all.** Record and estimate with unset times throughout. Tokens then weigh equally. `Progress` and units still work, and there is no ETA (`ETAKind` is `"none"`).

Never mix a recorded clock with wall-clock ticks in the same live run. The elapsed time is then measured against a reference the ticks say nothing about.

## Identify an unknown run

Hand every stored model to a `Matcher` when the caller does not know which reference applies:

```go
models, err := lpi.OpenStore("").Models()
mt := lpi.NewMatcher(models...)
for ev := range events {
	mt.Observe(lpi.TokenOf(ev.Step), ev.At)
}
if key, _, ok := mt.MergeTarget(); ok {
	// This run refines the pattern it was recognized as.
} else {
	// Nothing fit. File it under its own content, and the next run of this
	// shape gets live progress.
	key := lpi.ContentKey(run)
}
```

`Estimate` reports `Identifying` until a model fits well enough to be worth showing. `Locked` names the model once one does. `Best` names the closest model meanwhile. `MergeTarget` answers a different question: does this finished run belong back in the model it was recognized as? It holds out for a fit that was good throughout, not only at the end.

## Working with logs

`RecordFile(path)` digests a complete log file. It detects the timestamps (ISO-8601, `HH:MM:SS`, syslog, go log, epoch, dmesg) and unpacks gzip. `RecordReader` does the same for a stream, without a clock. On the live side, `Estimator.ObserveLine` and `Matcher.ObserveLine` take raw log text.

These are all the same tokens underneath. A model learned from a log file estimates a stream of `TokenOfLine` tokens. The CLI's database and a library caller's database are one database.

The CLI keeps what the library does not:

- following a growing file, and wrapping a child process
- passthrough that never collides with a status line, and terminal rendering
- capture files for a run that dies
- backtesting with `lpi eval`

## Concurrency

Nothing here is safe for concurrent use. An `Estimator` belongs to one live run. A caller that feeds it from several goroutines owns the lock. This is deliberate. The estimator sits on the hot path of a stream, and the caller already knows what it synchronizes.
