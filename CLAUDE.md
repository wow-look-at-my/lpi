# log-progress-indicator -- developer notes

`lpi` estimates completion %, units done, and ETA of a long-running task by
fuzzy-matching its partial log against reference logs of previous completed
runs. `README.md` is the user story; `docs/DESIGN.md` is the full design.

## Build and test

Always use `go-toolchain` (never bare `go` commands) from the repo root. It
runs mod tidy, vet (with auto-fixes), tests with coverage enforcement, lint,
and builds the binary to `build/lpi`.

```sh
go-toolchain                 # everything, before pushing
go-toolchain --no-benchmark  # faster inner loop
```

Accept whatever it auto-rewrites (formatting, imports, go.mod) and commit
those changes; note it refuses to auto-fix files with uncommitted changes,
so commit first. CI (`.github/workflows/ci.yml`) runs the same via
`wow-look-at-my/go-toolchain@v1`. Tests use `github.com/stretchr/testify`.

## Layout

```
cmd/lpi/               cobra CLI: one command per file, self-registering via
                       init(); refs.go holds the shared --ref/--key/--db
                       resolution, the pinned JSON snapshot type, and the
                       line feeder
internal/
  fingerprint/  line normalization (hand-rolled, no regexp) + FNV-1a hashing
  linescan/     long-line-safe line splitting (1 MiB cap, \r strip)
  timeparse/    timestamp format detection and stateful parsing
  model/        run digestion (time-gap weights), run merging, persistence
                (atomic save), capture.go: durable capture files for
                learning runs (written under <db>/pending/, sniffed by
                DigestFile via the "#lpi-capture v1" header)
  progress/     the live estimator: occurrence matching -> Snapshot
  tailer/       polling file follower (truncation, rotation, appears-late)
  render/       Bar/StatusLine/Summary strings + TTY-aware Renderer
testdata/demo/         two complete fake cmake builds + a ~55% partial run,
                       used by cmd tests and README examples
```

## The algorithm in five bullets

- Every line is normalized to a stable template (timestamps, counters, hex
  hashes, UUIDs -> `#`; ANSI stripped; text after the last `\r` wins) and
  hashed; variable noise vanishes, identifying text stays.
- Matching is order-free occurrence matching: the k-th live occurrence of a
  fingerprint matches the k-th reference occurrence. Robust to parallel and
  reordered logs.
- Each reference occurrence owns the time gap since the previous reference
  line (fraction of run duration). Progress = sum of matched weights, so
  silent stretches are owned by the line that ends them.
- Models merge up to 8 runs: expected count per fingerprint is the upper
  median across runs; occurrence fractions are averaged; weights
  renormalized to 1.
- ETA scales the remaining reference time by the observed pace (elapsed vs
  matched reference time); with no elapsed clock it assumes reference pace;
  otherwise no ETA. Confidence = live match rate (0.9/0.6 thresholds).

## Key CLI behaviors

- `progress.Estimator` is NOT concurrency-safe: watch/run wrap it (and the
  renderer) in a mutex; run's child-stderr passthrough shares that mutex via
  lockedWriter.
- Live modes never mix time sources: a detected log-timestamp format wins
  (carry-forward for unparsable lines); otherwise wall clock plus a periodic
  Tick. watch buffers up to 300 lines (or until the first tick) to decide.
- pipe/run passthrough tees at the reader, so stdout stays byte-faithful
  even for overlong or binary lines.
- run learns on exit code 0 (or always, with --learn-on-failure, which
  implies --learn) and propagates the child's exit code (128+N when signal
  N killed it, e.g. SIGTERM -> 143); pipe learns on clean EOF (documented
  caveat) -- except when interrupted: a SIGINT/SIGTERM to a learning pipe
  prints "interrupted -- run not learned", keeps the capture file, and
  osExits 128+N so the truncated stream is never merged at EOF.
- Durable capture: every learning run/pipe streams consumed lines to
  <db>/pending/<key>-<stamp>-<pid>.log (format in docs/DESIGN.md). Learned
  -> file removed; failed (non-zero exit, signal, read error, save error)
  -> file kept and the exact "lpi learn --key K --db D <path>" recovery
  command printed; <2 nonempty lines -> nothing recoverable, removed.
  Capture-file problems only warn ("capture file disabled") -- they never
  fail the run. `lpi learn` auto-detects capture files (full per-line
  timing preserved) and removes ingested ones that live in the current
  db's pending/ dir after a successful save.
- Live-learning bootstrap: a learn-target key with no model yet is NOT an
  error for `run --learn`/`pipe --learn-key` (when no `--ref` and any
  `--key` equals the learn key) -- the run records the baseline against an
  empty model: progress 0, confidence "none", "recording baseline" status
  line, and a short baseline summary. pipe's `--learn-key` doubles as the
  reference key when `--key`/`--ref` are absent.

## Testing seams (package vars)

- `render.PlainInterval` -- non-TTY reprint throttle
- `render.IsTTY` -- force TTY/plain mode
- `tailer.Tailer.Interval` -- poll interval (also the `--interval` flag)
- `cmd/lpi`: `osExit` (exit-code capture), `tickInterval` (live tick),
  `newSignalContext` (watch cancellation)
- cmd tests execute `rootCmd` in-process; `resetCommand` in helpers_test.go
  restores flag defaults AND re-inits pflag's sticky `--` position between
  executions (pflag never resets `argsLenAtDash` on Parse)

## Conventions

- cobra; one top-level command per file in `cmd/lpi/`; each file registers
  its command in its own `init()`. Never centralize registration.
- All files plain ASCII (`--`, `...`, straight quotes; no em dashes).
- Keep `README.md`, this file, and `docs/DESIGN.md` in sync with behavior
  changes.
