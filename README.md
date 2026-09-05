# lpi -- log progress indicator

**`lpi` puts a progress bar and an ETA on any long-running task by comparing its log output against logs of previous completed runs.**

A silent `make -j8`, a `docker build`, a test suite, a batch job -- they all print thousands of lines but never say "62% done, about 3 minutes left". If you kept the log of a previous successful run, `lpi` can. It fuzzy-matches the live output against that reference and works out how much of the reference run the live one covers. Every matched line weighs the share of the reference run's *time* it accounted for. Long silent steps (that final link, that one slow test) count for what they cost, instead of nothing.

## Install

```sh
brew tap pazer/build https://brew.pazer.build/tap.git
brew trust pazer/build
brew install pazer/build/lpi
```

Or take the binary straight from buildhost. `os` is `linux`, `darwin` or `windows`, and `arch` is `amd64` or `arm64`:

```sh
curl -fLo lpi "https://dl.pazer.build/lpi?os=linux&arch=amd64" && chmod +x lpi
```

## Quickstart

```sh
# First run: lpi has never seen this output, so it records it as a pattern.
lpi make -j8

# Every run after that: live bar, %, ETA -- and keeps learning.
lpi make -j8
```

That is the whole interface: no key to invent, no flag to remember. The first run ends with `recorded new pattern "make -j8"`. Every later run shows a live status line on stderr:

```
[==========>           ] 46.3%  units 65/108 (60.2%)  elapsed 3m05s  eta ~3m35s  pace 1.27x  match 98%  ref make -j8
```

lpi identifies the run by its OUTPUT, not by the command line. `make`, `make -j8`, and `nice make` all land on the same pattern while they produce the same build. The same `make` in two different projects is two different patterns. Failed runs never merge into a pattern: the captured log is kept and the recovery command printed.

Power users can manage named models explicitly -- seed a key from an old log and get real progress on the very first wrapped run:

```sh
lpi learn --key mybuild old-build.log
lpi run --key mybuild --learn -- make -j8
```

## Modes

### `lpi auto` -- automatic pattern detection (the default)

```sh
lpi make -j8            # plain "lpi CMD [ARGS...]" routes to auto
lpi -- run ./job.sh     # explicit form for commands shadowed by a subcommand
lpi auto --db /tmp/db -- make -j8
```

Runs the command exactly like `lpi run`, with passthrough, Ctrl-C forwarding and exit-code propagation. It picks the reference itself: every stored model is matched against the live output. The status line shows `identifying pattern` until one fits, then the normal bar tagged `ref <label>`. A clean exit merges the run into the recognized pattern. When nothing fits it records a new one (`recorded new pattern "make -j8"`), so the next run with the same output shape gets live progress. Auto-recorded patterns get content-derived keys (`auto.<hash>`). `lpi model list` shows the command lines they were seen from in the LABEL column. A wrapped command whose name collides with an lpi subcommand (`run`, `model`, ...) needs the `lpi -- CMD` form.

### `lpi run` -- wrap a command under an explicit key

```sh
lpi run --key mybuild --learn -- make -j8
lpi run --ref last-night.log -- cmake --build build -j
```

This is the explicit-key form of the default mode. You name the reference (`--key`/`--ref`) and opt into learning, instead of letting the output pick its own pattern.

Spawns the command, passes its stdout/stderr through byte-faithfully, and renders progress on stderr. It forwards Ctrl-C and propagates the exit code (a child killed by signal N exits 128+N, e.g. SIGTERM -> 143). With `--learn` (requires `--key`) the run is saved into the model, only when the command exits 0, so failed runs never pollute the reference. With `--learn` and no `--ref`, a `--key` that has no model yet is not an error. The first run is recorded as the baseline (no progress bar yet), and the next invocation shows real progress.

A learning run is never lost to a failure: every consumed line is also streamed to a capture file under `<db>/pending/` as it goes. When the run is learned, the file is silently removed. When the command fails (non-zero exit, killed by a signal) or the model save fails, the file is kept and the exact recovery command is printed:

```
exit status 2 -- run not learned
captured log kept: /home/me/.cache/log-progress-indicator/pending/mybuild-20260709-093012-4711.log
learn it later with: lpi learn --key mybuild --db /home/me/.cache/log-progress-indicator /home/me/.cache/log-progress-indicator/pending/mybuild-20260709-093012-4711.log
```

`--learn-on-failure` (implies `--learn`) saves the run into the model even on a non-zero exit -- for when a failed run's log is still a representative reference. The child's exit code propagates either way.

### `lpi analyze` -- a partial log already on disk

```sh
lpi analyze --key mybuild current-build.log
kubectl logs job/migrate | lpi analyze --key migrate -
```

```
Progress:    46.3% (time-weighted)
Units:       65 / 108 reference lines matched (60.2%)
Elapsed:     3m05s
ETA:         ~3m35s (pace 1.27x vs reference)
Confidence:  high (98.5% of lines matched; 1 novel, 0 overflow)
Reference:   108 units over 5m14s
```

Timestamps are auto-detected: ISO-8601, `HH:MM:SS`, syslog, go log, epoch, dmesg. When present, elapsed time and a pace-adjusted ETA come from the log's own clock. `--format` reads stamps the detector does not know (see below). `--json` prints one machine-readable snapshot instead.

### `lpi watch` -- follow a growing file

```sh
lpi watch --key mybuild /var/log/build.log
lpi watch --ref previous.log --json-stream current.log > progress.ndjson
```

Tails the file (handles truncation, rotation, and the file appearing late), reads pre-existing content first (`--from-start=true` by default -- history is what the estimate is built from), and repaints a status line on stderr. `--json-stream` additionally emits an NDJSON snapshot to stdout on every repaint. Ctrl-C stops and prints a final summary.

### `lpi pipe` -- sit inside a pipeline

```sh
docker build . 2>&1 | lpi pipe --key image
long-job | lpi pipe --key job --learn-key job | tee job.log
```

Forwards stdin to stdout byte-for-byte while rendering progress on stderr. `--learn-key K` also digests the stream and saves it under key `K` at EOF. When neither `--key` nor `--ref` is given it doubles as the reference key. A key with no model yet records the stream as its first baseline instead of erroring. So repeating `long-job | lpi pipe --learn-key job` bootstraps a new key, just like `lpi run`. (An explicit `--key` naming a different key than `--learn-key` must still exist.) Note: a pipe cannot see the upstream command's exit status. It learns on EOF even if the upstream failed, so prefer `lpi run` for success-gated learning.

A learning pipe streams every consumed line to a capture file under `<db>/pending/`, exactly like `lpi run --learn`. The file is removed once the stream is learned. It is kept, with the recovery command printed, when reading stdin fails or the save fails. Ctrl-C is handled specially. The same signal kills the upstream command, and that death EOFs stdin, which on its own learns a truncated stream. Instead, an interrupted learning pipe prints `interrupted -- run not learned`, keeps the capture file, and exits 128+N at once. The partial stream stays recoverable and never pollutes the model.

### `lpi learn` / `lpi model` -- manage the reference database

```sh
lpi learn --key mybuild build1.log build2.log.gz   # gzip handled transparently
lpi model list
lpi model show mybuild
lpi model rm mybuild
```

```
learned testdata/demo/build1.log: 106 lines, 5m09s, 106 unique fingerprints
learned testdata/demo/build2.log: 107 lines, 5m14s, 107 unique fingerprints
model "demo": 2 runs, 108 units over 5m14s -> /tmp/lpidemo/demo.lpi
```

A model keeps up to 8 runs, evicting the oldest. `--replace` starts a key from scratch. Models are stored in `$LPI_DB` if set, else `$XDG_CACHE_HOME/log-progress-indicator`, else `~/.cache/log-progress-indicator` -- one small gzipped file per key. Every command takes `--db DIR` to override. Model saves are atomic (temp file + rename), so a crash or full disk mid-save can never corrupt an existing model.

### `lpi eval` -- how good are the guesses, really?

Hand it complete logs of a task. It replays each one and grades the estimates it gives along the way. With several logs, each one is scored against a model built from the others. Nothing is ever graded against itself:

```sh
lpi eval build1.log build2.log build3.log
```

```
log                         lines  duration  err avg  err p90  err max   eta err   match
testdata/demo/build1.log      106     5m09s     1.4%     2.8%     3.9%        3%     99%
testdata/demo/build2.log      107     5m14s     1.2%     2.7%     3.9%        2%     98%
all                                             1.3%              3.9%        2%     99%

verdict: excellent -- progress is off by 1.3% on average, and the ETA by 2% of the run's own length.
```

`err` is how far the reported percentage sat from the log's own truth. That truth is the log's own clock, or its line count when it carries no timestamps. So a run that simply took longer than the reference is not charged for that. Halfway is halfway, whatever the machine's speed. That difference lands in `eta err`, which measures each ETA miss against the length of the run it was made in.

`--detail` prints what lpi said at each tenth of the run, next to what was really true at that moment:

```
      true     said    error       eta true left    pace
       11%     9.9%    -1.1%     4m46s     4m35s   1.10x
       51%    48.1%    -3.4%     2m47s     2m30s   1.05x
       99%    97.1%    -2.2%        9s        2s   1.01x
```

`--key NAME` scores the logs against a model you already learned, a real holdout. `--learn` adds them to that key once the scoring is done, and `--json` prints every number for a script to read. Without `--learn`, eval writes nothing to the database.

### Preloading from logs with unusual timestamps

Timestamps are auto-detected, and `lpi learn` says which reader it used (`... 3m53s (clock), ...`). When a log's stamps are none of the builtins, describe them with `--format` and, if needed, `--time-layout`:

```sh
# a regex whose 'time' group is read with a Go reference layout
lpi learn --key odd --format '^\((?P<time>[^)]+)\)' \
  --time-layout '02.01.2006 15h04m05s' build.log

# component groups, when no single layout fits
lpi learn --key web --format '^(?P<day>\d\d)/(?P<month>\w+)/(?P<year>\d{4}):(?P<hour>\d\d):(?P<min>\d\d):(?P<sec>\d\d) (?P<zone>[+-]\d{4})' access.log

# a whole stamp in one group, and a builtin selected by name
lpi learn --key svc --format '^(?P<epoch>\d+\.\d+)' service.log
lpi learn --key old --format iso8601 legacy.log
```

Lines the regex misses keep the previous line's time, so a log that mixes stamped and unstamped lines still preloads with real timing. The same two flags work on `lpi analyze`, `lpi watch`, and any `--ref` log. Full group list and rules: [docs/DESIGN.md](docs/DESIGN.md).

`lpi learn` (and `--ref`) also accepts the capture files a failed learning run keeps under `<db>/pending/`. Their header identifies them, and they replay with the exact per-line times of the recorded run. Once a pending capture is learned into the current database, `lpi learn` removes it (`removed pending capture: ...`), completing the lifecycle.

### Recovering a lost run

A 40-minute `lpi run --learn` can die at minute 41: a non-zero exit, a crashed terminal, even `kill -9` of lpi itself. The log it consumed is already on disk under `<db>/pending/`, one line per line consumed, stamped with the original times. On any failure lpi prints the exact command to recover it. Run it verbatim:

```sh
lpi learn --key mybuild --db ~/.cache/log-progress-indicator \
  ~/.cache/log-progress-indicator/pending/mybuild-20260709-093012-4711.log
```

That merges the run into the model with full timing data and removes the pending file. The next `lpi run --key mybuild` estimates against it. Failed runs are deliberately not merged automatically. A truncated log corrupts the model's time-gap weights, so the default is recoverability and the choice to merge stays yours (`--learn-on-failure` opts into merging). If lpi was killed outright and never printed the hint, look in `<db>/pending/`. Captures of successfully learned runs are always cleaned up, so anything left there is recoverable data.

## How it works

1. **Fingerprinting.** Every line is normalized into a stable template -- ANSI escapes stripped, timestamps/counters/hex hashes/UUIDs collapsed to `#`, whitespace squashed -- and hashed (FNV-1a 64). `10:04:07 [ 62%] Building C object src/net/tls.c.o` and `09:31:02 [ 58%] Building C object src/net/tls.c.o` become the same fingerprint. Variable noise vanishes. Identifying text stays.
2. **Occurrence matching, order-free.** The reference is a multiset: the k-th time a fingerprint appears live matches the k-th time it appeared in the reference. No sequence alignment, so parallel, interleaved, out-of-order logs match fine. "Units" are reference lines matched.
3. **Time-gap weighting.** Each reference line owns the time gap since the previous line of that run, as a fraction of the whole run. Progress is the sum of matched weights. The line that ends a 30-second silent link step owns that step, so the bar stalls there instead of racing to 99%.
4. **Merging runs.** Up to 8 reference runs merge per key. Expected counts take the upper median across runs, so one aborted or incremental run cannot drop lines. Times and weights average in seconds rather than in each run's own fractions, so a log cut short cannot inflate its share of the work. Each line's weight scales by how many of the runs actually print it: one run's quirks never become work the next run owes. Adding a reference must help or do nothing, and `lpi eval` is how that is checked.
5. **ETA.** pace = elapsed / (progress x reference-duration). ETA = remaining-weight x reference-duration x pace. The pace correction shrinks toward the reference in proportion to the progress behind it. A slow opening minute is a poor guide to the hour after it, and un-shrunk it stretched early ETAs by a third. Without usable timestamps, lines weigh equally and the ETA assumes reference pace, or is omitted.

Confidence is the fraction of live lines that matched: >= 90% is `high`,
>= 60% `medium`, else `low` -- novel lines (never seen in the reference) and
overflow lines (seen more often than expected) are counted separately.

## JSON output

`analyze --json` prints one object. `watch --json-stream` (stdout) and `pipe --json-stream` (stderr) print one per repaint, NDJSON-style:

| field | type | meaning |
|---|---|---|
| `progress` | float 0..1 | time-weighted progress (the primary estimate) |
| `units_done` / `units_total` | int | reference lines matched / expected |
| `units_pct` | float 0..100 | unit progress as a percentage |
| `has_times` | bool | reference has usable timing data |
| `elapsed_seconds` | float | elapsed time (0 if unknown) |
| `elapsed_known` | bool | whether elapsed time is known |
| `ref_duration_seconds` | float | merged reference run duration |
| `eta_seconds` | float | seconds remaining; **absent when no ETA** |
| `eta_kind` | string | `pace`, `ref-pace`, or `none` |
| `pace` | float | current speed vs reference (>1 = slower); 0 if unknown |
| `match_rate` | float 0..1 | fraction of live lines matched |
| `confidence` | string | `high`, `medium`, `low`, or `none` |
| `current_lines` / `matched_lines` / `novel_lines` / `overflow_lines` | int | line accounting |
| `identifying` | bool | auto mode is still picking a pattern; **absent when false** |
| `pattern` | string | display label of the locked pattern; **absent when empty** |

## Caveats

- **Incremental builds underestimate.** A full-build reference makes a short incremental run look "10% done" right up until it finishes. Learn incremental runs under their own key.
- **Novel-heavy output lowers confidence.** If the task prints mostly lines the reference never saw (new failure spew, different verbosity), the match rate -- and the estimate's trustworthiness -- drops. The `confidence` field says when to squint.
- **Pipe learning is not success-gated.** `lpi pipe --learn-key` saves whatever it saw at EOF, even if the upstream command failed (Ctrl-C is the exception: an interrupted learning pipe keeps its capture file and exits without learning). Use `lpi run --learn` when you can.
- Progress can sit still during steps the reference says are long -- that is the honest answer, not a hang.

## Building

```sh
go-toolchain
```

Runs mod tidy, vet, tests with coverage, and builds the binary to `build/lpi`. See `CLAUDE.md` for development notes and `docs/DESIGN.md` for the full design.
