# log-progress-indicator -- design

`lpi` answers the question "how far along is this long-running task?" by
comparing the task's partial log output against reference logs from previous
completed runs of the same task. Think: "how far along is this cmake build,
given last build's log?"

Logs vary between runs -- timestamps, hashes, paths, counters, and parallel
out-of-order interleaving all change -- so the comparison must be fuzzy and
order-independent. The design below is built around that constraint.

## Core algorithm

### 1. Line fingerprinting

Every log line is normalized to a stable template and hashed with FNV-1a 64:

- ANSI escape sequences are stripped: CSI (`ESC [` ... final byte
  `0x40`-`0x7e`), OSC (`ESC ]` ... `BEL` or `ESC \`), and other two-byte
  `ESC` sequences.
- If the line contains `\r`, only the text after the last `\r` is kept
  (progress-bar overwrite semantics: the final rewrite wins).
- Word tokens are maximal runs of `[A-Za-z0-9]`. Non-word bytes pass through
  as-is, except that whitespace runs collapse to a single space and
  leading/trailing space is trimmed.
- Tokens are rewritten by the first matching rule:
  1. all digits -> `#`
  2. `0x`/`0X` followed by hex digits -> `#`
  3. all hex chars, length >= 6, with at least one digit and one letter -> `#`
     (git SHAs)
  4. all hex chars, length >= 8, with at least one digit -> `#`
  5. otherwise each maximal digit run inside the token becomes `#`
     (`foo123bar` -> `foo#bar`, `sha256` -> `sha#`)
- UUID pre-check: a token of exactly 8 hex chars followed immediately by
  `-hex4-hex4-hex4-hex12` collapses to a single `#` (the 4-char hex groups
  would otherwise survive rules 3/4).
- Output is capped at 512 bytes.

The normalizer is a hand-rolled single pass over bytes -- no regexp -- because
it sits on the hot path for every log line.

Variable content (timestamps, counters, addresses, durations, percentages)
vanishes; identifying text (file paths, target names, messages) stays. The
fingerprint of a line is the FNV-1a 64-bit hash of its normalized form.

### 2. Occurrence matching (order-free)

A reference run is a multiset of fingerprints with per-occurrence metadata.
The k-th occurrence of fingerprint `f` in the current log matches the k-th
occurrence of `f` in the reference. There is no sequence alignment, which
makes matching robust to parallel and out-of-order logs.

"Units" are reference lines. Units done = sum over `f` of
`min(count_current(f), count_reference(f))`.

### 3. Time weighting

Each reference occurrence owns the time gap since the previous line in that
run: `w_i = t_i - t_{i-1}`, stored as a fraction of the total run duration.
Time-weighted progress is the sum of `WeightFrac` over matched occurrences.

This is what makes progress honest during silent stretches: a 10-minute link
step that prints nothing is owned by the line that ends it, so progress
correctly stalls at the pre-link percentage until that line appears.

If a reference has no usable timestamps, every line weighs equally, and time
progress equals units progress (position mode: line indices stand in for
times).

### 4. ETA

With `Progress` the time-weighted progress and `RefDuration` the reference run
duration:

- pace = `elapsed_current / (Progress * RefDuration)`
- ETA = `(1 - Progress) * RefDuration * pace`, kind `"pace"`

This applies only when elapsed time is known, `Progress >= 0.02`, matched
lines >= 5, and `RefDuration > 0`.

Fallback when elapsed is unknown but `RefDuration > 0` and
`Progress >= 0.001`: ETA = `(1 - Progress) * RefDuration`, kind `"ref-pace"`.

Otherwise there is no ETA (kind `"none"`). ETAs are clamped to >= 0.

### 5. Confidence

`matchRate = matchedLines / currentLines`:

- `>= 0.9` -> `"high"`
- `>= 0.6` -> `"medium"`
- otherwise -> `"low"`
- `"none"` if no lines have been observed yet, or if the model itself is
  empty (`TotalUnits == 0`, the live-learning baseline recording): with
  nothing to match against, confidence in the estimate is meaningless
  rather than merely low

Novel lines (fingerprint not in the model) and overflow lines (fingerprint
known but its reference occurrences are exhausted) are tracked separately.

## Automatic mode (content-first pattern identity)

`lpi CMD [ARGS...]` -- no subcommand, no flags -- runs CMD under live
progress tracking with nothing to configure: no key to invent, no learn
flag to remember. The routing layer turns it into `lpi auto -- CMD ...`,
and `auto` identifies which stored pattern the output belongs to, shows
progress against it, and learns from every clean exit.

### Identity is the output, never the command

The command line cannot be a pattern's identity. Commands are many-to-one
with what they actually do (`make`, `make -j8`, and `nice make -j12`
produce the same build; so do `cat build.log` and `sh -c 'cat build.log'`)
and one-to-many (`make` in two different worktrees, or before and after a
checkout, is two entirely different workloads). The output already carries
the identity -- the fingerprint multiset IS the workload -- so auto mode
matches the live output against every stored pattern and lets the output
claim its own reference. Command lines are kept only as display labels.

### Patterns are ordinary models

A "pattern" is a normal model with two extra conventions:

- **Storage id.** Auto-recorded patterns live under `auto.<hash16>`:
  `model.AutoKey(run)` hashes the first recorded run's fingerprint
  multiset (each fingerprint and its occurrence count, ascending
  fingerprint order, 8 bytes big-endian each, FNV-1a 64) into `auto.`
  plus 16 lowercase hex chars. The id is only a storage handle --
  identity is re-established from content on every run -- but a
  content-derived id makes re-recording identical output land on the
  same file. `auto.` is the reserved namespace for auto-recorded
  patterns; `.` is already in the key charset, so the id survives
  sanitizeKey unchanged.
- **Invocation labels.** `Model.Invocations` keeps the command lines
  that produced the pattern: most recent first, deduplicated (a repeat
  moves to the front), capped at `MaxInvocations = 5`, empty strings
  ignored. `DisplayLabel()` is `Invocations[0]` when present, else the
  key. User-keyed models gain labels too when auto mode merges into them.

### The fit chooser

`progress.NewChooser` builds one estimator per stored model plus a "null"
estimator over an empty model (it supplies line counts, elapsed time, and
confidence-"none" snapshots for the pre-lock and no-candidate states).
Every observed line feeds every estimator. Because matching is order-free
and every candidate consumes the stream from line 1, locking late or
switching the lock costs nothing: the winner's estimator state is already
exact, so the display is correct from the moment of the decision.

The lifecycle is a small state machine over cumulative match rates
(matched/current), evaluated after every counted line:

| threshold | value | role |
|---|---|---|
| `lockMinLines` | 12 | never lock before this many counted lines |
| `earlyLockRate` | 0.80 | rate that locks as soon as lockMinLines is reached |
| `lockWindowLines` | 32 | standard decision point |
| `lockRate` | 0.50 | minimum rate to lock at/after the window |
| `switchMargin` | 0.15 | a rival must beat the locked rate by this to steal the lock |
| `mergeRate` | 0.60 | minimum final rate to merge the run into the locked pattern |

- **Unlocked.** From `lockMinLines` counted lines, a candidate at
  `earlyLockRate` locks immediately (an 80% rate on 12+ lines is not a
  coincidence). From `lockWindowLines`, `lockRate` suffices -- and the
  check keeps running on every later line, so a preamble-heavy log locks
  late, the moment a candidate's cumulative rate climbs through
  `lockRate`.
- **Locked.** A rival steals the lock only at `lockRate` or better AND
  `switchMargin` above the locked candidate's rate. The margin is
  hysteresis: sibling patterns with heavy overlap would otherwise flap
  the lock (and the label) back and forth line by line.
- **Ties** break deterministically: higher rate, then higher matched
  weight, then more runs in the model, then the lexicographically
  smaller key.

Rationale for the two-bar design: locking is a *display* decision --
being provisionally wrong costs a mislabeled status line that the next
lines correct -- so it can afford `lockRate` 0.5. Merging a run into the
wrong pattern corrupts stored state, so the merge bar is higher:
`mergeRate` 0.6 aligns with the "medium" confidence boundary (below it
the estimate itself is labeled untrustworthy, and a run the estimator
does not trust has no business becoming reference data).

### Status surface

Pre-lock, the status line shows `identifying pattern  lines N  elapsed E`
(the snapshot carries `Identifying: true`); with no stored patterns at all
the snapshot is the plain empty-model one and renders as the existing
`recording baseline` branch. After the lock, the normal bar/percent line
gains a trailing `ref <label>` segment (`Snapshot.Label`, truncated for
the status line), and the final summary names the pattern on its own
`Pattern` row. There are NO out-of-band prints mid-run: notices (learned/
recorded/recovery lines) print only after the renderer closes, so they
never interleave with a repainting status line.

### Learning semantics

Auto mode always digests the run (and always streams a capture file, key
prefix `auto`). On exit 0:

- locked with final rate >= `mergeRate` -> the run merges into the locked
  pattern (`learnRun`), and the command line joins its Invocations.
- otherwise -> a new pattern is recorded under `model.AutoKey(run)`,
  labeled with the command line. If a model already exists under that id,
  the run merges into it instead: the id is a content hash, so an
  existing file means this exact output shape was recorded before --
  same pattern (this also catches sub-`lockMinLines` runs, which can
  never lock).

On a non-zero exit the run is never merged (same reasoning as
`run --learn`: a truncated log corrupts time-gap weights). The capture
file is kept and the recovery command printed; its key is the fitted
pattern when the fit cleared `mergeRate`, else the content id -- in both
cases where a recovery would sensibly land. Fewer than 2 nonempty lines
follows the usual rule: nothing recoverable, capture removed. The child's
exit code propagates unchanged.

### CLI routing

`routeArgs` rewrites `os.Args[1:]` before cobra parses:

- empty, or a first arg starting with `-` (`--help`, `--version`):
  unchanged.
- first arg exactly `--`: `auto -- <rest>` (the explicit magic form).
- first arg a registered subcommand or alias, or one of cobra's implicit
  `help`/`completion`/`__complete`/`__completeNoDesc` entry points:
  unchanged -- subcommands always win over the magic path.
- anything else: `auto -- <all args>`, so the wrapped command's own flags
  (`lpi make -j8`) are never parsed as lpi flags.

A wrapped command whose name collides with a subcommand (`lpi run ...`
meaning some other `run` binary) needs the `lpi -- run ...` escape.

## Capture durability

Learning happens live, in memory: the digester keeps fingerprint
occurrences, not raw text, so before this mechanism existed a failure at
the end of a 40-minute learning run -- a non-zero exit, a signal, a failed
save, or lpi itself dying -- discarded everything. The capture file makes
learning durable: a learning run (`run --learn`/`--learn-on-failure`,
`pipe --learn-key`) streams every line the digester consumes to
`<db>/pending/<key>-<YYYYMMDD-HHMMSS>-<pid>.log` as it goes, with direct
unbuffered writes, so even SIGKILL of lpi loses nothing that reached the
digester.

### Format (v1)

The first line is the header: `#lpi-capture v1`, optionally followed by one
tab and a source label (which becomes `Run.Source` on replay; without it
the file path is used, matching plain logs). Each subsequent record is

    <int64 unix nanoseconds>\t<line text>\n

split on the FIRST tab only, so line text may itself contain tabs. Empty
lines are recorded too, stamped like any other; the digester skips them
identically live and on replay, so the reconstructed Run is identical to
the one digested live.

Times are out-of-band (a stamp column, not part of the text) because
fingerprints hash the FULL line text: prepending stamps in-band would
produce templates that never match live lines, making a recovered model
useless. This is the trap the format exists to avoid.

`model.DigestFile` sniffs the header (after the gzip sniff, so gzipped
captures work) and replays records through `Digester.LineAt` with the
recorded times -- which means capture files work everywhere a reference log
does: `lpi learn`, and `--ref` on any command, with full timing.

### Lifecycle

- Created when a learning run starts. Creation or write failures disable
  capture with a single stderr warning (`warning: capture file disabled:`)
  and never fail the run: recovery must not break the primary flow.
- Removed when the run is learned (exit 0, `--learn-on-failure`, or pipe
  EOF): a clean success leaves nothing behind.
- Kept on any failure that loses the digest -- non-zero exit, signal-killed
  child, stdin read error, interrupted pipe, failed model save -- along
  with two printed recovery lines: `captured log kept: <path>` and
  `learn it later with: lpi learn --key <key> --db <db> <path>`. For auto
  mode the file's name prefix is the fixed `auto`, and the recovery
  command's `--key` is computed after the fact: the fitted pattern when
  the fit cleared mergeRate, else the run's content id (`auto.<hash16>`).
- Exception: when the digester saw fewer than 2 nonempty lines the printed
  command could never succeed (Finish would fail), so the file is removed
  and only the error is printed.
- `lpi learn` completes the cycle: after a successful save, any ingested
  file living inside the current db's `pending/` directory is removed
  (`removed pending capture: <path>`). Files outside `pending/` are never
  touched, and nothing is removed when learning fails.

Failed runs are deliberately NOT merged automatically: a truncated log
would corrupt the time-gap weights (the missing tail's time would be
redistributed over the lines that did print), so the default is
recoverability, and `run --learn-on-failure` is the explicit opt-in for
"this failed run is still representative".

`pipe --learn-key` additionally installs a SIGINT/SIGTERM handler (only
when learning): the same Ctrl-C that interrupts lpi also kills the upstream
command, whose death EOFs stdin -- and pipe's unconditional EOF-learn would
merge a truncated stream into the model. The handler wins instead: it
serializes with the render/estimator mutex, prints
`interrupted -- run not learned` plus the recovery lines, and exits 128+N
immediately (the capture file is already durable on disk). A signal that
arrives after the stream completed is ignored.

### Atomic save

`Model.Save` used to create the target in place, so a crash or full disk
mid-encode destroyed the previous model (double loss: the run AND its
reference), and a truncated file made every later Load fail until
`model rm`. Save now writes to a temp file in the same directory and
renames it over the target only once complete, removing the temp on any
error: a failed save leaves an existing model byte-identical.

## Package reference

### internal/fingerprint

    func Normalize(line string) string
    func Fingerprint(line string) uint64  // FNV-1a 64 of Normalize(line)
    func Sum64(s string) uint64           // FNV-1a 64 of s as-is

`Fingerprint(line) == Sum64(Normalize(line))`. `Sum64` exists so callers that
already hold the normalized form (to check for emptiness) do not have to
normalize twice.

### internal/linescan

    type Scanner struct{ ... }
    func NewScanner(r io.Reader) *Scanner
    func (s *Scanner) Scan() bool
    func (s *Scanner) Text() string
    func (s *Scanner) Err() error

Splits on `\n`, strips one trailing `\r`, and caps lines at 1 MiB (longer
lines are truncated and the remainder is discarded silently). Implemented as a
`bufio.Reader.ReadSlice` loop rather than `bufio.Scanner`, which errors on
long lines. A final unterminated line is still yielded.

### internal/timeparse

    type Format struct{ ... }            // stateful Parse; not thread-safe
    func (f *Format) Name() string
    func (f *Format) Parse(line string) (t time.Time, ok bool)
    func Detect(lines []string) *Format

Detects and parses per-line timestamps of log files (live modes use the wall
clock instead). `Detect` samples up to 300 lines and returns the best format
matching at least 30% of the non-empty sample with at least 5 hits, else nil.

Supported formats, anchored at line start (tolerating an optional leading `[`
plus spaces, and a trailing `]`):

- ISO-8601/RFC3339: `2026-07-02T15:04:05`, optional `.frac`/`,frac`, optional
  `Z`/`+hh:mm`/`-hh:mm`, and a space instead of `T` (covers python's
  `2026-07-02 15:04:05,123`)
- Go log: `2026/07/02 15:04:05`, optional fraction
- syslog: `Jan  2 15:04:05`
- bare time: `15:04:05`, optional fraction, also `[15:04:05]`; stateful
  midnight rollover: if a new time is earlier than the last by more than two
  hours, a day is added (the offset persists across calls)
- epoch at line start: 10 digits in 1.4e9-2.6e9 (seconds) or 13 digits whose
  second value falls in the same range (milliseconds)
- dmesg: `[   12.345678]` as relative seconds

Only differences between parsed times matter; date-less formats parse onto a
fixed arbitrary base day. Lines that do not match return `ok == false` and the
caller carries the previous time forward.

### internal/model

    type Occurrence struct {
        TimeFrac   float32 // completion time as fraction of run duration
        WeightFrac float32 // share of total run duration this occurrence owns
    }
    type Run struct {
        Source   string
        Lines    int           // nonempty lines digested
        Duration time.Duration // 0 if unknown
        HasTimes bool
        Occ      map[uint64][]Occurrence
    }
    type Digester struct{ ... }
    func NewDigester(source string, format *timeparse.Format) *Digester
    func (d *Digester) Line(text string)
    func (d *Digester) LineAt(text string, at time.Time)
    func (d *Digester) Finish() (*Run, error) // error if < 2 nonempty lines
    func DigestReader(r io.Reader, source string, format *timeparse.Format) (*Run, error)
    func DigestFile(path string) (*Run, error)
    type Model struct {
        Key         string
        Runs        []*Run
        Invocations []string // display labels, most recent first (never identity)
        // derived by Rebuild():
        Expect      map[uint64][]Occurrence
        TotalUnits  int
        RefDuration time.Duration
        HasTimes    bool
    }
    func New(key string) *Model
    func (m *Model) AddRun(r *Run) // FIFO-evict beyond MaxRuns=8, then Rebuild
    func (m *Model) AddInvocation(cmd string) // dedupe to front, cap MaxInvocations=5
    func (m *Model) DisplayLabel() string     // Invocations[0], else Key
    func AutoKey(r *Run) string // "auto." + 16 hex of the fingerprint multiset
    func (m *Model) Rebuild()
    func (m *Model) Save(path string) error
    func Load(path string) (*Model, error)
    func DefaultDir() string
    func PathForKey(dir, key string) string
    func PendingDir(db string) string // <db>/pending, the capture-file dir
    type CaptureWriter struct{ ... }  // nil-safe methods; see "Capture durability"
    func NewCaptureWriter(db, key, source string) (*CaptureWriter, error)
    func (cw *CaptureWriter) Add(text string, at time.Time) error
    func (cw *CaptureWriter) Path() string
    func (cw *CaptureWriter) Close() error
    func (cw *CaptureWriter) Discard()

Digester rules:

- Lines whose normalized form is empty are skipped entirely.
- With a format (or explicit `LineAt` times), each line's raw stamp is its
  parsed time; unparsed lines carry the previous time forward; negative gaps
  are clamped to zero (the effective clock never moves backwards). Lines
  before the first parsed timestamp are pinned to the run start (TimeFrac 0,
  weight 0).
- At `Finish`: `Duration = last - first`. `HasTimes` is true only when times
  were supplied AND `Duration > 0`; otherwise the run falls back to position
  mode, where with N nonempty lines `t_i = i` and the denominator is `N - 1`.
- `TimeFrac_i = (t_i - t_first) / Duration`;
  `WeightFrac_i = (t_i - t_prev) / Duration`; the first line overall has
  weight 0. The sum of all `WeightFrac` is 1 (within float error).

`DigestFile` handles gzip transparently (sniffing the magic bytes) and
capture files transparently (sniffing the `#lpi-capture v1` header, whose
records replay through `LineAt` with their recorded times); for plain logs
it samples the first 300 lines for `timeparse.Detect`, then digests the
whole file.

`CaptureWriter` streams a learning run's consumed lines to
`<db>/pending/`; `Add` disables itself after the first write failure
(returning that failure once so the caller can warn), and every method is
nil-receiver-safe so a disabled capture needs no call-site branches. A
sanitized key prefix (capped at 40 bytes), timestamp, and pid make the file
name unique; `pending/` entries lack the `.lpi` suffix, so `model list` and
the available-keys error listing never mistake them for models.

`Rebuild` merges up to `MaxRuns = 8` runs:

- For each fingerprint, per-run counts (absent = 0) are sorted ascending and
  the expected count is `counts[n/2]` -- the upper median, so with 2 runs it
  takes the max. This protects against a single incremental or short run
  dropping everything. Fingerprints with expected count 0 are omitted.
- For occurrence index k, `TimeFrac`/`WeightFrac` are the mean over the runs
  that have that occurrence. All `WeightFrac` are then renormalized so the
  grand total is 1.
- `TotalUnits` = sum of expected counts. `RefDuration` = upper-median duration
  among runs with `HasTimes` (0 if none). `HasTimes` = any run has times.

Persistence: `Save` writes a gzip-compressed gob envelope
`{Version: 1, Key, Runs}` (creating parent directories as needed),
atomically via a same-directory temp file renamed over the target; `Load`
rejects unknown versions and calls `Rebuild`. `DefaultDir` honors `$LPI_DB`,
then `$XDG_CACHE_HOME/log-progress-indicator`, then
`~/.cache/log-progress-indicator`. `PathForKey` sanitizes the key to
`[A-Za-z0-9._-]` (other bytes become `_`, an empty result becomes `default`)
and appends `.lpi`.

### internal/progress

    type Estimator struct{ ... }
    func NewEstimator(m *model.Model) *Estimator
    func (e *Estimator) Observe(line string, at time.Time) // at zero if unknown
    func (e *Estimator) Tick(at time.Time) // advance clock without a line
    func (e *Estimator) Snapshot() Snapshot
    type Snapshot struct {
        Progress     float64 // primary, 0..1, time-weighted
        UnitsDone    int
        UnitsTotal   int
        UnitsPct     float64
        HasTimes     bool
        Elapsed      time.Duration
        ElapsedKnown bool
        RefDuration  time.Duration
        ETA          time.Duration
        ETAKind      string  // "pace" | "ref-pace" | "none"
        Pace         float64 // 0 if unknown
        MatchRate    float64
        Confidence   string  // "high" | "medium" | "low" | "none"
        Identifying  bool    // set by the Chooser pre-lock; zero for Estimators
        Label        string  // locked pattern's display label; zero for Estimators
        CurrentLines, MatchedLines, NovelLines, OverflowLines int
    }
    type Candidate struct {
        Key   string
        Label string
        Model *model.Model
    }
    type Chooser struct{ ... } // see "Automatic mode"
    func NewChooser(cands []Candidate) *Chooser
    func (c *Chooser) Observe(line string, at time.Time)
    func (c *Chooser) Tick(at time.Time)
    func (c *Chooser) Snapshot() Snapshot
    func (c *Chooser) Locked() (key, label string, ok bool)
    func (c *Chooser) Best() (key string, rate float64, ok bool)
    func (c *Chooser) FinalRate(key string) float64
    func (c *Chooser) MergeTarget() (key, label string, ok bool) // Locked + rate >= mergeRate

`Observe` skips empty-normalized lines entirely. For each remaining line the
fingerprint is looked up in `model.Expect`: unknown -> novel; known with
occurrences remaining -> match (units done +1, weight done += that
occurrence's `WeightFrac`); known but exhausted -> overflow. `firstAt`/`lastAt`
are tracked from non-zero `at` values (`Tick` updates `lastAt` only, and the
clock never moves backwards); `Elapsed = lastAt - firstAt` once both are set.
`Progress = min(weightDone, 1)`. ETA, pace, and confidence follow the core
algorithm rules above.

An empty model (`TotalUnits == 0`, as used by the live-learning bootstrap)
is a valid input: every line is novel, `Progress`/`UnitsPct`/`MatchRate`/
`Pace` stay 0, `ETAKind` and `Confidence` stay `"none"`, and no snapshot
field is ever NaN or Inf (all divisions are guarded), so the JSON output
stays well-formed.

### internal/tailer

    type Tailer struct {
        Path      string
        FromStart bool          // read pre-existing content first
        Interval  time.Duration // poll interval, default 150ms if zero
    }
    func (t *Tailer) Run(ctx context.Context, lines chan<- string) error

Polling follower. It waits for the file to exist, reads appended data,
detects truncation (size below the read offset -> rewind to 0) and rotation
(the path resolves to a different file per `os.SameFile`, or disappears ->
reopen the new file from 0). `FromStart=false` skips only content that
existed at start; a file that appears later is read from 0. Line splitting
is not reimplemented: all bytes are pumped through an `io.Pipe` into a
`linescan.Scanner` (same 1 MiB cap and `\r` semantics), so a partial final
line is held until its newline arrives. `Run` closes `lines` before
returning; it returns nil when ctx is done and the error on hard failures.
An undelivered trailing partial line is dropped at shutdown.

### internal/render

    var PlainInterval = 2 * time.Second        // seam for tests
    var IsTTY = func(w io.Writer) bool { ... } // *os.File char-device check
    type Renderer struct{ ... }
    func New(w io.Writer) *Renderer
    func (r *Renderer) Update(s progress.Snapshot)
    func (r *Renderer) Close(final progress.Snapshot)
    func Bar(frac float64, width int) string
    func StatusLine(s progress.Snapshot) string
    func Summary(s progress.Snapshot) string

`StatusLine` is the single-line live status (bar, %, units, elapsed, eta,
pace, match); the elapsed/eta/pace segments are omitted when unknown.
Durations round to seconds: `47s`, `12m34s`, `1h02m`. Percentages get one
decimal (`match` gets none). `Summary` is the aligned multi-line block used
by analyze and the final summaries; the ETA line carries
`(pace 1.07x vs reference)` or `(assuming reference pace)` and is omitted
when there is no ETA; the Reference line says `no timing data` for untimed
models. Against an empty model (`UnitsTotal == 0`, baseline recording) a
bar would be meaningless, so `StatusLine` renders
`recording baseline  lines 1234  elapsed 2m14s` (elapsed omitted when
unknown) and `Summary` renders a three-row block: `Reference: none yet
(recording baseline)`, `Lines`, and `Elapsed`. A snapshot with
`Identifying` set (the auto-mode Chooser, pre-lock) renders
`identifying pattern  lines 1234  elapsed 2m14s` instead, and a non-empty
`Label` appends a `ref <label>` segment to the status line (label
truncated to 28 bytes plus `...`) plus a `Pattern` row in `Summary`. On a
TTY the Renderer repaints in place with `\r ESC[K`; otherwise
it prints a line for the first update, then at most every `PlainInterval`
or when the whole progress percent changes. `Close` always prints the final
line.

### cmd/lpi

Cobra CLI, binary name `lpi`; one self-registering command per file.
`refs.go` holds the shared pieces: `--ref`/`--key`/`--db` resolution (a key
loads from the database -- with a clear error listing available keys -- and
each `--ref` is digested and added in memory on top), the pinned JSON
snapshot type used by `--json`/`--json-stream` (`eta_seconds` absent when
there is no ETA; `units_pct` is a percentage), and the `lineFeeder` that
stamps lines with parsed-or-wall-clock times.

- `analyze [--ref|--key] CURRENT.log [--json]` -- `-` reads stdin; buffers
  up to 300 lines for `timeparse.Detect`, then streams everything through
  the estimator (carry-forward for unparsable lines).
- `watch [--ref|--key] FILE [--from-start] [--interval] [--json-stream]` --
  tails via the tailer (FromStart defaults to true: the history is the
  estimate). The time source is decided once, from the first up-to-300
  lines (or whatever arrived by the first 500ms tick): the log's own
  timestamps if detected, else wall clock with a periodic `Tick` -- never a
  mix. Renders per line batch and at least every 500ms; `--json-stream`
  emits NDJSON to stdout per repaint. SIGINT/SIGTERM -> final summary,
  exit 0.
- `pipe [--ref|--key] [--learn-key K] [--json-stream]` -- stdin to stdout
  byte-faithfully (the tee sits at the reader), wall-clock estimation to
  stderr. `--json-stream` replaces the status line with NDJSON on stderr
  (stdout is the passthrough). At EOF: summary, and `--learn-key` digests
  the run into that key -- unconditionally, since a pipe cannot see the
  upstream exit status. With neither `--key` nor `--ref`, `--learn-key`
  doubles as the reference key. A learning pipe streams to a capture file
  and arms the interrupt handler described in "Capture durability": read
  errors and failed saves keep the file with recovery hints; Ctrl-C keeps
  it and exits 128+N without learning the truncated stream.
- `run [--ref|--key] [--learn|--learn-on-failure] -- CMD [ARGS...]` --
  spawns CMD, passes both streams through, feeds one estimator
  (mutex-shared across both stream goroutines and a 500ms ticker), forwards
  SIGINT/SIGTERM, propagates the exit code through the `osExit` seam --
  128+N when signal N killed the child (shell convention; the
  `syscall.WaitStatus` assertion lives in run.go without build tags because
  the type exists on every GOOS the package builds on). `--learn` (requires
  `--key`) saves the run only on exit code 0; `--learn-on-failure` (implies
  `--learn`) saves it regardless. Both stream every consumed line -- the
  digester sees stdout and stderr alike -- to the capture file; a failed
  run keeps it and prints the recovery command ("Capture durability").
- `auto -- CMD [ARGS...]` -- the magic default mode: a bare `lpi CMD
  [ARGS...]` routes here (see "Automatic mode"). Every stored model is fed
  to the fit chooser; the status line shows `identifying pattern` until
  the lock, then the normal bar with a `ref <label>` segment. Always
  learning: exit 0 merges into the fitted pattern (final rate >=
  mergeRate) or records a new `auto.<hash16>` pattern; a non-zero exit is
  never merged and keeps the capture file with the recovery command,
  exactly like `run --learn`. The only flag is `--db`.
- `learn --key K [--replace] LOG...` -- digests completed logs
  (gzip-transparent, capture-file-transparent), prints per-log stats and
  the model summary, then removes any ingested captures living in the
  current db's `pending/` directory.
- `model list|show KEY|rm KEY` -- database management, honoring `--db`.

Live learning re-loads the key's model from disk before saving, so ad-hoc
`--ref` runs mixed in for matching are never persisted.

Live-learning bootstrap (`resolveOrBootstrap` in refs.go): `run --learn`
and `pipe --learn-key` treat a learn-target key with no stored model as
"record the baseline" rather than an error, provided no `--ref` was given
and any explicit `--key` equals the learn target (a missing `--key` naming
a different key still errors, as does a missing `--key` combined with
`--ref`). The mode prints `no model for key "K" yet -- recording baseline
run` to stderr and runs the estimator against an empty model, and the run
is digested and saved under the usual rules (run: exit 0 or
--learn-on-failure; pipe: clean EOF), so the next invocation has a real
reference. A failed baseline is not lost either: its capture file is kept
like any other failed learning run.

## Build and test

`go-toolchain` (no arguments) from the repo root: mod tidy, vet, tests with
coverage enforcement, lint, build. CI runs the same via the
`wow-look-at-my/go-toolchain@v1` action. Tests use
`github.com/stretchr/testify` (`assert`/`require`) -- the toolchain's import
fixer canonicalizes testify imports to that path.
