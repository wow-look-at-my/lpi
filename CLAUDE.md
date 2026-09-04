# lpi -- developer notes

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
`wow-look-at-my/go-toolchain@master`. Tests use `github.com/stretchr/testify`.

Autorelease publishes to the buildhost project `lpi` (`brew install
pazer/build/lpi`, `https://dl.pazer.build/lpi`). The project name is the repo
name, and the binary matching it is what keeps the release flat instead of a
nested `<repo>/lpi`. A buildhost description is settable only at project
create, so CI creates the project before the build.

## Layout

```
cmd/lpi/               cobra CLI: one command per file, self-registering via
                       init(); refs.go holds the shared --ref/--key/--db
                       resolution, the pinned JSON snapshot type, and the
                       line feeder; route.go routes bare "lpi CMD" to the
                       auto command (auto.go), the magic default mode
internal/
  fingerprint/  line normalization (hand-rolled, no regexp) + FNV-1a hashing
  linescan/     long-line-safe line splitting (1 MiB cap, \r strip)
  timeparse/    timestamp format detection and stateful parsing;
                custom.go: Compile builds the reader behind
                --format/--time-layout (builtin name, regex with named
                groups, or Go layout)
  model/        run digestion (time-gap weights), run merging, persistence
                (atomic save), invocation labels + AutoKey content ids,
                capture.go: durable capture files for
                learning runs (written under <db>/pending/, sniffed by
                DigestFile via the "#lpi-capture v1" header)
  progress/     the live estimator: occurrence matching -> Snapshot;
                autofit.go: the Chooser that fits live output against every
                stored model (auto mode's lock/switch/merge decisions)
  tailer/       polling file follower (truncation, rotation, appears-late)
  render/       Bar/StatusLine/Summary strings (render.go, render_test.go)
                + TTY-aware Renderer (renderer_test.go);
                Passthrough coordinates child output with the status line
                (the two never share a terminal line); Message gives lpi's
                own out-of-band lines the same discipline and Break ends an
                abandoned render on a clean line
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
  renderer) in a mutex; run/pipe passthrough writes share that mutex via
  render's Passthrough writers.
- Stamp reading is pinnable: learn/analyze/watch (and any `--ref` log) take
  `--format` (auto, a builtin name, or a regex with named groups) plus
  `--time-layout`; nil format means detect. A missed line carries the
  previous time, and `Run.TimeFormat` records which reader ran, which is
  what `learn` prints. Depth: docs/DESIGN.md, "User-specified formats".
- Live modes never mix time sources: a detected log-timestamp format wins
  (carry-forward for unparsable lines); otherwise wall clock plus a periodic
  Tick. watch buffers up to 300 lines (or until the first tick) to decide.
- pipe/run passthrough tees at the reader, so stdout stays byte-faithful
  even for overlong or binary lines. The renderer erases a pending TTY
  status before passthrough bytes and repaints it after them (moving to a
  fresh line when the child left a partial one); plain-mode status prints
  always end with a newline. A status line and child output never share a
  terminal line. lpi's own out-of-band prints get the same discipline:
  while a renderer is live, capture warnings, pipe's interrupt notice, and
  the recovery-command lines go through render.Message via the notify seam
  in refs.go (renderNotify under the state mutex; plainNotify when no
  renderer exists, e.g. a json-stream pipe); error paths that abandon
  rendering call render.Break first, and watch's --json-stream snapshots
  ride Passthrough so NDJSON never mixes with a painted status.
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
- Magic default mode: `Execute` routes os.Args through `routeArgs` -- a
  first arg that is no flag, no `--`, and no registered subcommand/alias
  (nor help/completion/__complete*) becomes `auto -- <args>`; `lpi -- CMD`
  is the escape for shadowed names. `auto` (only flag: --db) feeds every
  stored model to progress.Chooser, which locks by cumulative match rate
  (lockMinLines=12, earlyLockRate=0.8, lockWindowLines=32, lockRate=0.5,
  switchMargin=0.15 hysteresis) and is always learning: exit 0 merges into
  the locked pattern at final rate >= mergeRate=0.6 (adding the command
  line to Model.Invocations, shown by `model list`'s LABEL column), else
  records a new pattern under model.AutoKey(run) -- `auto.<hash16>`, the
  reserved auto namespace, hashed from the fingerprint multiset (an
  existing file under that id means same content, so it merges). A clean
  run with <2 nonempty lines learns nothing and is NOT an error: capture
  discarded, one "nothing to learn" notice line, exit code stays the
  child's 0. Failure semantics are unchanged from run --learn: never
  merged, capture kept (recovery key = fitted pattern when solid, else
  the content id), <2 nonempty lines discards, exit code propagates.

## Testing seams (package vars)

- `render.PlainInterval` -- non-TTY reprint throttle
- `render.IsTTY` -- force TTY/plain mode
- `tailer.Tailer.Interval` -- poll interval (also the `--interval` flag)
- `cmd/lpi`: `osExit` (exit-code capture), `tickInterval` (live tick),
  `newSignalContext` (watch cancellation)
- The toolchain's Go runs tests parallel by default; every cmd/lpi and
  render test calls `t.Serial()` because they share process-wide state
  (`rootCmd`, `render.IsTTY`, `render.PlainInterval`, `os.Args`). A new
  test in either package needs that call too.
- cmd tests execute `rootCmd` in-process; `resetCommand` in helpers_test.go
  restores flag defaults AND re-inits pflag's sticky `--` position between
  executions (pflag never resets `argsLenAtDash` on Parse). `execLpi`
  passes its args through `routeArgs`, so cmd tests exercise the
  production magic-mode routing; tests that call `Execute`/`main` directly
  must pin os.Args first (routing reads the real process arguments)

## Conventions

- cobra; one top-level command per file in `cmd/lpi/`; each file registers
  its command in its own `init()`. Never centralize registration.
- All files plain ASCII (`--`, `...`, straight quotes; no em dashes).
- Keep `README.md`, this file, and `docs/DESIGN.md` in sync with behavior
  changes.
