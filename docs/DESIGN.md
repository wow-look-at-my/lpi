# lpi -- design

`lpi` answers the question "how far along is this long-running task?" by comparing the task's partial log output against reference logs from previous completed runs of the same task. Think: "how far along is this cmake build, given last build's log?"

Logs vary between runs -- timestamps, hashes, paths, counters, and parallel out-of-order interleaving all change -- so the comparison must be fuzzy and order-independent. The design below is built around that constraint.

## Core algorithm

### 1. Line fingerprinting

Every log line is normalized to a stable template and hashed with FNV-1a 64:

- ANSI escape sequences are stripped: CSI (`ESC [` ... final byte `0x40`-`0x7e`), OSC (`ESC ]` ... `BEL` or `ESC \`), and other two-byte `ESC` sequences.
- If the line contains `\r`, only the text after the last `\r` is kept (progress-bar overwrite semantics: the final rewrite wins).
- Word tokens are maximal runs of `[A-Za-z0-9]`. Non-word bytes pass through as-is, except that whitespace runs collapse to a single space and leading/trailing space is trimmed.
- Tokens are rewritten by the first matching rule:
  1. all digits -> `#`
  2. `0x`/`0X` followed by hex digits -> `#`
  3. all hex chars, length >= 6, with at least one digit and one letter -> `#` (git SHAs)
  4. all hex chars, length >= 8, with at least one digit -> `#`
  5. otherwise each maximal digit run inside the token becomes `#` (`foo123bar` -> `foo#bar`, `sha256` -> `sha#`)
- UUID pre-check: a token of exactly 8 hex chars followed immediately by `-hex4-hex4-hex4-hex12` collapses to a single `#`. Without it the 4-char hex groups survive rules 3/4.
- Output is capped at 512 bytes.

The normalizer is a hand-rolled single pass over bytes -- no regexp -- because it sits on the hot path for every log line.

Variable content vanishes: timestamps, counters, addresses, durations, percentages. Identifying text stays: file paths, target names, messages. The fingerprint of a line is the FNV-1a 64-bit hash of its normalized form.

### 2. Occurrence matching (order-free)

A reference run is a multiset of fingerprints with per-occurrence metadata. The k-th occurrence of fingerprint `f` in the current log matches the k-th occurrence of `f` in the reference. There is no sequence alignment, which makes matching robust to parallel and out-of-order logs.

"Units" are reference lines. Units done = sum over `f` of `min(count_current(f), count_reference(f))`.

### 3. Time weighting

Each reference occurrence owns the time gap since the previous line in that run: `w_i = t_i - t_{i-1}`, stored as a fraction of the total run duration. Time-weighted progress is the sum of `WeightFrac` over matched occurrences.

This is what makes progress honest during silent stretches. The line that ends a 10-minute link step owns that step, so progress stalls at the pre-link percentage until that line appears.

If a reference has no usable timestamps, every line weighs equally, and time progress equals units progress (position mode: line indices stand in for times).

### 4. ETA

With `Progress` the time-weighted progress and `RefDuration` the reference run duration:

- pace = `elapsed_current / (Progress * RefDuration)`
- paceApplied = `1 + (pace - 1) * Progress`
- ETA = `(1 - Progress) * RefDuration * paceApplied`, kind `"pace"`

This applies only when elapsed time is known, `Progress >= 0.02`, matched lines >= 5, and `RefDuration > 0`.

`pace` is measured over the part of the run seen so far. Applying it whole extrapolates a local speed over a remainder it says little about. A run that opens slowly and then catches up reported an ETA a third too long for its first half. `paceApplied` shrinks the correction toward the reference by the progress standing behind it. That is the pace ETA blended with the `"ref-pace"` ETA at weight `Progress`. At the end of a run the two agree, so only the early, thinly-evidenced ETAs move. Take a 400-line run whose first quarter took 2.5x the reference pace. Its mean ETA error across the deciles falls from 90% of the time left to 27%. On two near-identical runs it does not move. `Snapshot.Pace` still reports the measured pace, what the run is really doing. `Snapshot.PaceApplied` reports what the ETA used.

Fallback when elapsed is unknown but `RefDuration > 0` and `Progress >= 0.001`: ETA = `(1 - Progress) * RefDuration`, kind `"ref-pace"`.

Otherwise there is no ETA (kind `"none"`). ETAs are clamped to >= 0.

### 5. Confidence

`matchRate = matchedLines / currentLines`:

- `>= 0.9` -> `"high"`
- `>= 0.6` -> `"medium"`
- otherwise -> `"low"`
- `"none"` when no line arrived yet, or when the model itself is empty (`TotalUnits == 0`, the live-learning baseline recording). With nothing to match against, confidence is meaningless rather than merely low

Novel lines (fingerprint not in the model) and overflow lines (fingerprint known but its reference occurrences are exhausted) are tracked separately.

## Automatic mode (content-first pattern identity)

`lpi CMD [ARGS...]` -- no subcommand, no flags -- runs CMD under live progress tracking with nothing to configure: no key to invent, no learn flag to remember. The routing layer turns it into `lpi auto -- CMD ...`. `auto` then identifies which stored pattern the output belongs to, shows progress against it, and learns from every clean exit.

### Identity is the output, never the command

The command line cannot be a pattern's identity. Commands are many-to-one with what they actually do. `make`, `make -j8`, and `nice make -j12` produce the same build, and so do `cat build.log` and `sh -c 'cat build.log'`. They are also one-to-many: `make` in two different worktrees, or before and after a checkout, is two entirely different workloads. The output already carries the identity, because the fingerprint multiset IS the workload. So auto mode matches the live output against every stored pattern and lets the output claim its own reference. Command lines are kept only as display labels.

### Patterns are ordinary models

A "pattern" is a normal model with two extra conventions:

- **Storage id.** Auto-recorded patterns live under `auto.<hash16>`: `model.AutoKey(run)` hashes the first recorded run's fingerprint multiset (each fingerprint and its occurrence count, ascending fingerprint order, 8 bytes big-endian each, FNV-1a 64) into `auto.` plus 16 lowercase hex chars. The id is only a storage handle, because content re-establishes identity on every run. A content-derived id still makes re-recording identical output land on the same file. `auto.` is the reserved namespace for auto-recorded patterns. `.` is already in the key charset, so the id survives sanitizeKey unchanged.
- **Invocation labels.** `Model.Invocations` keeps the command lines that produced the pattern: most recent first, deduplicated (a repeat moves to the front), capped at `MaxInvocations = 5`, empty strings ignored. `DisplayLabel()` is `Invocations[0]` when present, else the key. User-keyed models gain labels too when auto mode merges into them.

### The fit chooser

`progress.NewChooser` builds one estimator per stored model, plus a "null" estimator over an empty model. The null one supplies line counts, elapsed time, and confidence-"none" snapshots for the pre-lock and no-candidate states. Every observed line feeds every estimator. Matching is order-free, and every candidate consumes the stream from line 1. So locking late or switching the lock costs nothing. The winner's estimator state is already exact. The display is correct from the moment of the decision.

The lifecycle is a small state machine over cumulative match rates (matched/current), evaluated after every counted line:

| threshold | value | role |
|---|---|---|
| `lockMinLines` | 12 | never lock before this many counted lines |
| `earlyLockRate` | 0.80 | rate that locks as soon as lockMinLines is reached |
| `lockWindowLines` | 32 | standard decision point |
| `lockRate` | 0.50 | minimum rate to lock at/after the window |
| `switchMargin` | 0.15 | a rival must beat the locked rate by this to steal the lock |
| `mergeRate` | 0.60 | minimum final rate to merge the run into the locked pattern |

- **Unlocked.** From `lockMinLines` counted lines, a candidate at `earlyLockRate` locks immediately (an 80% rate on 12+ lines is not a coincidence). From `lockWindowLines`, `lockRate` suffices. The check keeps running on every later line, so a preamble-heavy log locks late, the moment a candidate's cumulative rate climbs through `lockRate`.
- **Locked.** A rival steals the lock only at `lockRate` or better AND `switchMargin` above the locked candidate's rate. The margin is hysteresis. Without it, sibling patterns with heavy overlap flap the lock back and forth line by line, and the label with it.
- **Ties** break deterministically. The order is higher rate, then higher matched weight, then more runs in the model, then the lexicographically smaller key.

Rationale for the two-bar design: locking is a *display* decision. Being provisionally wrong costs a mislabeled status line that the next lines correct. That bar can afford `lockRate` 0.5. Merging a run into the wrong pattern corrupts stored state, so the merge bar is higher. `mergeRate` 0.6 aligns with the "medium" confidence boundary. Below that the estimate itself is labeled untrustworthy, and a run the estimator does not trust has no business as reference data.

### Status surface

Pre-lock, the status line shows `identifying pattern  lines N  elapsed E`, and the snapshot carries `Identifying: true`. With no stored patterns at all the snapshot is the plain empty-model one and renders as the existing `recording baseline` branch. After the lock, the normal bar/percent line gains a trailing `ref <label>` segment, from `Snapshot.Label` and truncated for the status line. The final summary names the pattern on its own `Pattern` row. There are NO out-of-band prints mid-run. The learned, recorded and recovery notices print only after the renderer closes, so they never interleave with a repainting status line.

### Learning semantics

Auto mode always digests the run (and always streams a capture file, key prefix `auto`). On exit 0:

- locked with final rate >= `mergeRate` -> the run merges into the locked pattern (`learnRun`), and the command line joins its Invocations.
- otherwise -> a new pattern is recorded under `model.AutoKey(run)`, labeled with the command line. A model that already exists under that id takes the run as a merge instead. The id is a content hash. An existing file means this exact output shape was recorded before. That is the same pattern. This also catches sub-`lockMinLines` runs, which can never lock.
- fewer than 2 nonempty output lines -> nothing to learn. No pattern is recorded and the capture is removed. A one-line notice prints: `nothing to learn -- fewer than 2 nonempty output lines`. The child's exit code (0) is preserved. Wrapping a quick command must never turn its success into an lpi failure.

On a non-zero exit the run is never merged, for the same reason as `run --learn`. A truncated log corrupts time-gap weights. The capture file is kept and the recovery command printed. Its key is the fitted pattern when the fit cleared `mergeRate`, else the content id. Those are the two places a recovery sensibly lands. A failed run with fewer than 2 nonempty lines follows the usual rule: nothing recoverable, capture removed. The child's exit code propagates unchanged.

### CLI routing

`routeArgs` rewrites `os.Args[1:]` before cobra parses:

- empty, or a first arg starting with `-` (`--help`, `--version`): unchanged.
- first arg exactly `--`: `auto -- <rest>` (the explicit magic form).
- first arg a registered subcommand or alias, or one of cobra's implicit `help`/`completion`/`__complete`/`__completeNoDesc` entry points: unchanged. Subcommands always win over the magic path.
- anything else: `auto -- <all args>`, so the wrapped command's own flags (`lpi make -j8`) are never parsed as lpi flags.

A wrapped command whose name collides with a subcommand (`lpi run ...` meaning some other `run` binary) needs the `lpi -- run ...` escape.

## Capture durability

Learning happens live, in memory. The digester keeps fingerprint occurrences, not raw text. So without this mechanism, a failure at the end of a 40-minute learning run discards everything. That failure is a non-zero exit, a signal, a failed save, or lpi itself dying. The capture file makes learning durable. A learning run (`run --learn`/`--learn-on-failure`, `pipe --learn-key`) streams every line the digester consumes to `<db>/pending/<key>-<YYYYMMDD-HHMMSS>-<pid>.log` as it goes. The writes are direct and unbuffered, so even SIGKILL of lpi loses nothing that reached the digester.

### Format (v1)

The first line is the header: `#lpi-capture v1`, optionally followed by one tab and a source label. That label becomes `Run.Source` on replay. Without it the file path is used, matching plain logs. Each subsequent record is

```
    <int64 unix nanoseconds>\t<line text>\n
```

split on the FIRST tab only, so line text may itself contain tabs. Empty lines are recorded too, stamped like any other. The digester skips them identically live and on replay, so the reconstructed Run matches the one digested live.

Times are out-of-band, a stamp column rather than part of the text, because fingerprints hash the FULL line text. Stamps prepended in-band produce templates that never match live lines, which makes a recovered model useless. This is the trap the format exists to avoid.

`model.DigestFile` sniffs the header, after the gzip sniff so gzipped captures work. It replays records through `Digester.LineAt` with the recorded times. So capture files work everywhere a reference log does, with full timing: `lpi learn`, and `--ref` on any command.

### Lifecycle

- Created when a learning run starts. A creation or write failure disables capture with a single stderr warning (`warning: capture file disabled:`) and never fails the run. Recovery must not break the primary flow.
- Removed when the run is learned, on exit 0, `--learn-on-failure`, or pipe EOF. A clean success leaves nothing behind.
- Kept on any failure that loses the digest: a non-zero exit, a signal-killed child, a stdin read error, an interrupted pipe, a failed model save. Two recovery lines print with it, `captured log kept: <path>` and `learn it later with: lpi learn --key <key> --db <db> <path>`. For auto mode the file's name prefix is the fixed `auto`. The recovery command's `--key` is computed after the fact: the fitted pattern when the fit cleared mergeRate, else the run's content id (`auto.<hash16>`).
- Exception: when the digester saw fewer than 2 nonempty lines the printed command can never succeed, because Finish fails. The file is removed and only the error is printed.
- `lpi learn` completes the cycle. After a successful save, any ingested file inside the current db's `pending/` directory is removed (`removed pending capture: <path>`). Files outside `pending/` are never touched. Nothing is removed when learning fails.

Failed runs are deliberately NOT merged automatically. A truncated log corrupts the time-gap weights, redistributing the missing tail's time over the lines that did print. So the default is recoverability. `run --learn-on-failure` is the explicit opt-in for "this failed run is still representative".

`pipe --learn-key` additionally installs a SIGINT/SIGTERM handler, only when learning. The same Ctrl-C that interrupts lpi also kills the upstream command, and that death EOFs stdin. Pipe's unconditional EOF-learn then merges a truncated stream into the model. The handler wins instead. It serializes with the render/estimator mutex and prints `interrupted -- run not learned` plus the recovery lines. Those go through the renderer's `Message`, so they land on their own terminal lines instead of gluing onto a painted status. It then exits 128+N at once, the capture file already durable on disk. A signal that arrives after the stream completed is ignored.

### Atomic save

`Model.Save` writes to a temp file in the same directory and renames it over the target only once complete. It removes the temp on any error, so a failed save leaves an existing model byte-identical. Creating the target in place instead loses both the run and its reference when a crash or a full disk hits mid-encode. The truncated file then fails every later Load until `model rm`.

## Package reference

### internal/fingerprint

```go
    func Normalize(line string) string
    func Fingerprint(line string) uint64  // FNV-1a 64 of Normalize(line)
    func Sum64(s string) uint64           // FNV-1a 64 of s as-is
```

`Fingerprint(line) == Sum64(Normalize(line))`. `Sum64` exists so callers that already hold the normalized form (to check for emptiness) do not have to normalize twice.

### internal/linescan

```go
    type Scanner struct{ ... }
    func NewScanner(r io.Reader) *Scanner
    func (s *Scanner) Scan() bool
    func (s *Scanner) Text() string
    func (s *Scanner) Err() error
```

Splits on `\n`, strips one trailing `\r`, and caps lines at 1 MiB (longer lines are truncated and the remainder is discarded silently). Implemented as a `bufio.Reader.ReadSlice` loop rather than `bufio.Scanner`, which errors on long lines. A final unterminated line is still yielded.

### internal/timeparse

```go
    type Format struct{ ... }            // stateful Parse; not thread-safe
    func (f *Format) Name() string
    func (f *Format) Clone() *Format     // same reader, rollover state reset
    func (f *Format) Parse(line string) (t time.Time, ok bool)
    func Detect(lines []string) *Format
    func Compile(spec, layout string) (*Format, error)  // nil = detect
    func Names() []string                // builtin format names
    func Groups() []string               // regex group names
```

Detects and parses per-line timestamps of log files. Live modes use the wall clock instead. `Detect` samples up to 300 lines. It returns the best format matching at least 30% of the non-empty sample with at least 5 hits, else nil.

Supported formats, anchored at line start (tolerating an optional leading `[` plus spaces, and a trailing `]`):

- ISO-8601/RFC3339: `2026-07-02T15:04:05`, optional `.frac`/`,frac`, optional `Z`/`+hh:mm`/`-hh:mm`, and a space instead of `T` (covers python's `2026-07-02 15:04:05,123`)
- Go log: `2026/07/02 15:04:05`, optional fraction
- syslog: `Jan  2 15:04:05`
- bare time: `15:04:05`, optional fraction, also `[15:04:05]`. Midnight rollover is stateful: a new time earlier than the last by more than two hours adds a day, and the offset persists across calls
- epoch at line start: 10 digits in 1.4e9-2.6e9 (seconds) or 13 digits whose second value falls in the same range (milliseconds)
- dmesg: `[   12.345678]` as relative seconds

Only differences between parsed times matter, so date-less formats parse onto a fixed arbitrary base day. Lines that do not match return `ok == false`. The caller then carries the previous time forward.

#### User-specified formats

`Compile(spec, layout)` builds the reader behind `--format`/`--time-layout`, so a log the detector cannot read still preloads with real times. An empty spec and no layout return `nil`, which means "detect". Otherwise:

- a builtin name (`iso8601`, `golog`, `syslog`, `epoch`, `dmesg`, `clock`) selects that parser. A layout alongside it is an error
- a regex, optionally written `regex:<expr>`, is matched against each line, and its named groups say how to read the stamp. A `time` group hands its text to `layout` when one is given, else to the builtin parser that first recognizes it. That choice is remembered for the rest of the log
- component groups build the stamp piecewise. They are `year` (two digits mean the current century), `month` (number, abbreviation, or full name), `day`, `hour`, `min`, `sec`, `frac`, and `zone` (`Z`, `+hh:mm`, `+hhmm`, `+hh`, or an IANA name). `epoch`, `epochms` and `epochns` carry a whole stamp
- a layout with no regex parses the start of each line. It widens the window around the layout's own length, because a layout renders narrower than it parses

A regex naming none of the known groups is rejected, so a typo fails loudly instead of silently reading nothing. A line the regex misses, or whose stamp will not parse, is treated exactly like an unstamped line. The digester carries the previous time forward, so a log that mixes stamped and unstamped lines still yields one clock. Component groups without a date land on the base day and take the same midnight rollover as the bare-clock builtin. `Clone` resets that state, so one compiled format can read several logs.

`Run.TimeFormat` records the reader a digest actually used, which is what `lpi learn` prints after each file.

### internal/model

```go
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
    func DigestFileWith(path string, format *timeparse.Format) (*Run, error)
    func ReplayFile(path string, format *timeparse.Format, fn func(text string, at time.Time)) error
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
```

Digester rules:

- Lines whose normalized form is empty are skipped entirely.
- With a format, or explicit `LineAt` times, each line's raw stamp is its parsed time. Unparsed lines carry the previous time forward. Negative gaps clamp to zero, so the effective clock never moves backwards. Lines before the first parsed timestamp pin to the run start, at TimeFrac 0 and weight 0.
- At `Finish`: `Duration = last - first`. `HasTimes` is true only when times were supplied AND `Duration > 0`. Otherwise the run falls back to position mode, where with N nonempty lines `t_i = i` and the denominator is `N - 1`.
- `TimeFrac_i = (t_i - t_first) / Duration` and `WeightFrac_i = (t_i - t_prev) / Duration`. The first line overall has weight 0. The sum of all `WeightFrac` is 1, within float error.

`DigestFile` handles gzip transparently by sniffing the magic bytes. It handles capture files the same way, sniffing the `#lpi-capture v1` header, and their records replay through `LineAt` with the recorded times. For a plain log it samples the first 300 lines for `timeparse.Detect`, then digests the whole file.

`CaptureWriter` streams a learning run's consumed lines to `<db>/pending/`. `Add` disables itself after the first write failure, returning that failure once so the caller can warn. Every method is nil-receiver-safe. A disabled capture needs no call-site branches. A sanitized key prefix (capped at 40 bytes), a timestamp and a pid make the file name unique. `pending/` entries lack the `.lpi` suffix, so `model list` and the available-keys error listing never mistake them for models.

`Rebuild` merges up to `MaxRuns = 8` runs:

- For each fingerprint, per-run counts (absent = 0) sort ascending and the expected count is `counts[n/2]`, the upper median. With 2 runs that takes the max. This protects against a single incremental or short run dropping everything. Fingerprints with expected count 0 are omitted.
- For occurrence index k, `TimeFrac`/`WeightFrac` are the mean over the runs that have that occurrence, taken in SECONDS. Each run's stored fractions are shares of ITS OWN duration. `runScales` multiplies them by that duration first, scaling a run with no clock by `RefDuration`. Merging the raw fractions is what made a truncated reference catastrophic. A log covering a tenth of the work states per-line shares ten times too large. Averaging those at face value moved the model's mass to the opening minutes.
- The merged weight then scales by `support`, the share of the runs able to print that line that did. A line only one run in four prints is only that often expected of the next one. It holds a quarter of the weight instead of a full share the next run can never claim. A run that ends before the occurrence's time never had the chance to print it. The absence does not count against it. A short log reads as short, not as different.
- All `WeightFrac` renormalize so the grand total is 1, and `TimeFrac` divides back by `RefDuration`, clamped to 1. `Timeline` lists every expected occurrence sorted by `TimeFrac`. That is what the estimator walks to retire work a live run has gone past.
- `TotalUnits` = sum of expected counts. `RefDuration` = upper-median duration among runs with `HasTimes`, 0 if none. `HasTimes` = any run has times.

`internal/eval`'s corpus tests measure the merge. They score one holdout against models differing only in what was learned. A second reference must never raise the error. A truncated reference must not bias the estimate upward. A finished run must read as finished.

Persistence: `Save` writes a gzip-compressed gob envelope `{Version: 1, Key, Runs}`, creating parent directories as needed. The write is atomic, through a same-directory temp file renamed over the target. `Load` rejects unknown versions and calls `Rebuild`. `DefaultDir` honors `$LPI_DB`, then `$XDG_CACHE_HOME/log-progress-indicator`, then `~/.cache/log-progress-indicator`. `PathForKey` sanitizes the key to `[A-Za-z0-9._-]`, turning other bytes into `_` and an empty result into `default`, then appends `.lpi`.

### internal/progress

```go
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
        Pace         float64 // measured; 0 if unknown
        PaceApplied  float64 // shrunk pace the ETA used; 0 if unknown
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
```

`Observe` skips empty-normalized lines entirely. For each remaining line it looks the fingerprint up in `model.Expect`. Unknown is novel. Known with occurrences remaining is a match, which adds 1 to units done and that occurrence's `WeightFrac` to weight done. Known but exhausted is overflow. `firstAt`/`lastAt` track non-zero `at` values, `Tick` updates `lastAt` only, and the clock never moves backwards. `Elapsed = lastAt - firstAt` once both are set.

`Progress = min(weightDone / (1 - skipped), 1)`. `skipped` is the weight of expected occurrences the run has demonstrably gone past without printing. The run's position on the reference is the earliest `TimeFrac` among its last `windowFor(TotalUnits)` matches. That window is a share of the model, between 8 and 32. The position never moves backwards. Every `Timeline` entry behind it that is still unmatched retires into `skipped`, and comes back if it turns up late. Taking the earliest of the window rather than the latest match keeps a stray or out-of-order match from declaring the run nearly done. Without this, a run that does not do some of the reference's work can never reach the end. The error was one-sided, always an undercount, and its size tracked how much of the reference the run never printed. ETA, pace, and confidence follow the core algorithm rules above.

An empty model (`TotalUnits == 0`, as used by the live-learning bootstrap) is a valid input. Every line is novel. `Progress`, `UnitsPct`, `MatchRate` and `Pace` stay 0, and `ETAKind` and `Confidence` stay `"none"`. No snapshot field is ever NaN or Inf, because every division is guarded, so the JSON output stays well-formed.

### internal/eval

```go
    type Target struct{ Path string; Run *model.Run }
    type Point struct{ Line int; Truth, Pred float64; ETA, TrueLeft time.Duration; ETAKind string; Pace float64; Elapsed time.Duration }
    type Result struct{ ... } // per-log scores: MeanAbsErr, MedAbsErr, P90AbsErr, MaxAbsErr, WorstAt, Bias, ETAMeanRelErr, ETAMeanRunErr, Checkpoints
    type Overall struct{ ... } // suite averages
    func LeaveOneOut(targets []Target, format *timeparse.Format) ([]*Result, error)
    func Against(m *model.Model, targets []Target, format *timeparse.Format) ([]*Result, error)
    func Score(m *model.Model, t Target, format *timeparse.Format) (*Result, error)
    func Aggregate(rs []*Result) Overall
    func Grade(mae float64) string // "excellent" | "good" | "rough" | "poor"
    func Report(w io.Writer, rs []*Result, detail bool)
    func JSON(r *Result) JSONResult
    func Verdict(o Overall) string
```

`Score` replays a complete log through a `progress.Estimator` with `model.ReplayFile`. It mirrors the digester's effective clock: carry-forward for an unstamped line, a monotonic clamp, and empty-normalized lines skipped. So the replay sees exactly the times the digest was built from. At every counted line it compares the estimate with the truth the log itself carries. That truth is its share of the run's own clock when the log is timed, else its share of the line count. The signed error feeds mean, median, p90, max and bias. `Checkpoints` keeps the first estimate at or past each tenth of the run, plus a final row.

Truth being the run's OWN clock is what makes the progress error a fair score across runs of different length. A machine that takes half as long is still halfway at halfway, and lpi is neither credited nor charged for that. A run that is slow in ONE PLACE does move truth away from the reference's shape. That error is charged in full. `TestScoreSlowerRunIsBehindTheReference` pins the direction.

The ETA gets two errors, because the obvious one is a trap. `ETAMeanRelErr` divides each miss by the time still left. A miss in the final seconds divides by nearly zero and dominates the mean. A run whose ETA is within 6% for its first half can still report a mean in the thousands. `ETAMeanRunErr` divides the same misses by the run's whole length, which weighs a miss the same wherever it happened. The report and the verdict use `ETAMeanRunErr`. Both ride the JSON, as `eta_run_err` and `eta_rel_err`.

`LeaveOneOut` scores each target against a model built from the others, the only honest read on an unseen run. A lone target is scored against itself and marked `SelfFit`, because that flatters the result. `Against` scores holdout logs against an already-stored model.

### internal/tailer

```go
    type Tailer struct {
        Path      string
        FromStart bool          // read pre-existing content first
        Interval  time.Duration // poll interval, default 150ms if zero
    }
    func (t *Tailer) Run(ctx context.Context, lines chan<- string) error
```

Polling follower. It waits for the file to exist and reads appended data. It detects truncation, where a size below the read offset rewinds to 0. It detects rotation, where the path resolves to a different file per `os.SameFile`, or disappears, and reopens the new file from 0. `FromStart=false` skips only content that existed at start, so a file that appears later is read from 0. Line splitting is not reimplemented. All bytes pump through an `io.Pipe` into a `linescan.Scanner`, at the same 1 MiB cap and `\r` semantics. A partial final line waits for its newline. `Run` closes `lines` before returning. It returns nil when ctx is done, and the error on a hard failure. An undelivered trailing partial line is dropped at shutdown.

### internal/render

```go
    var PlainInterval = 2 * time.Second        // seam for tests
    var IsTTY = func(w io.Writer) bool { ... } // *os.File char-device check
    type Renderer struct{ ... }
    func New(w io.Writer) *Renderer
    func (r *Renderer) Update(s progress.Snapshot)
    func (r *Renderer) Close(final progress.Snapshot)
    func (r *Renderer) Message(msg string)
    func (r *Renderer) Break()
    func (r *Renderer) Passthrough(dst io.Writer, mu *sync.Mutex) io.Writer
    func Bar(frac float64, width int) string
    func StatusLine(s progress.Snapshot) string
    func Summary(s progress.Snapshot) string
```

`StatusLine` is the single-line live status: bar, %, units, elapsed, eta, pace, match. The elapsed, eta and pace segments are omitted when unknown. Durations round to seconds: `47s`, `12m34s`, `1h02m`. Percentages get one decimal, and `match` gets none. `Summary` is the aligned multi-line block used by analyze and the final summaries. Its ETA line carries `(pace 1.07x vs reference)` or `(assuming reference pace)`, and is omitted when there is no ETA. Its Reference line says `no timing data` for an untimed model. Against an empty model (`UnitsTotal == 0`, baseline recording) a bar means nothing. So `StatusLine` renders `recording baseline  lines 1234  elapsed 2m14s`, dropping elapsed when unknown. `Summary` then renders a three-row block: `Reference: none yet (recording baseline)`, `Lines`, and `Elapsed`. A snapshot with `Identifying` set, the auto-mode Chooser pre-lock, renders `identifying pattern  lines 1234  elapsed 2m14s` instead. A non-empty `Label` appends a `ref <label>` segment to the status line, truncated to 28 bytes plus `...`, plus a `Pattern` row in `Summary`. On a TTY the Renderer repaints in place with `\r ESC[K`. Otherwise it prints a line for the first update, then at most every `PlainInterval` or when the whole progress percent changes. `Close` always prints the final line.

`Passthrough` is how run and pipe forward child output without ever letting it share a terminal line with the status. On a TTY a pending status is erased before the child's bytes and repainted after them. Bytes ending mid-line make the next paint start on a fresh line instead of gluing onto the child's partial one. In plain mode every status print ends with a newline. A partial child line pending on the renderer's own stream is terminated before the next print. The child's bytes themselves are never modified, buffered, or re-timed. Only lpi's own rendering adapts. Writes through the returned writer lock mu, the same mutex the caller serializes Update/Close with. A destination that cannot collide with the status stream is returned unwrapped and stays lock-free. Such a destination is neither the renderer's own writer nor a TTY alongside a TTY status.

`Message` gives lpi's OWN out-of-band lines the same discipline while a renderer is live. Those lines are capture warnings, pipe's interrupt notice, and the printed recovery command. A painted TTY status is erased, a partial child line is terminated, and the message prints with a trailing newline. The next Update, or completed passthrough line, repaints the status. It follows the Update/Close convention, so the caller serializes it under the same mutex. cmd/lpi routes these prints through the `notify` func type in refs.go. That is `renderNotify` when a renderer is active, and `plainNotify` otherwise, such as a json-stream pipe. `Break` is for paths that abandon rendering without a final status, such as a mid-stream read error or a tailer failure. It newline-terminates a painted status or partial child line. Whatever follows then starts on a fresh line: recovery lines, the CLI's error report. watch's `--json-stream` snapshots ride `Passthrough` for the same reason. When stdout shares a terminal with the stderr status, each NDJSON line gets the erase/repaint treatment. A piped stdout is returned unwrapped.

### cmd/lpi

Cobra CLI, binary name `lpi`, one self-registering command per file. `refs.go` holds the shared pieces. It resolves `--ref`/`--key`/`--db`. A key loads from the database, with a clear error listing available keys. Each `--ref` is digested and added in memory on top. It holds the pinned JSON snapshot type used by `--json`/`--json-stream`, where `eta_seconds` is absent when there is no ETA and `units_pct` is a percentage. It also holds the `lineFeeder` that stamps lines with parsed-or-wall-clock times.

- `analyze [--ref|--key] CURRENT.log [--json]` -- `-` reads stdin. It buffers up to 300 lines for `timeparse.Detect`, then streams everything through the estimator, carrying forward over unparsable lines.
- `watch [--ref|--key] FILE [--from-start] [--interval] [--json-stream]` -- tails through the tailer, and FromStart defaults to true because the history is the estimate. The time source is decided once, from the first up-to-300 lines, or whatever arrived by the first 500ms tick. It is the log's own timestamps if detected, else wall clock with a periodic `Tick`, never a mix. It renders per line batch and at least every 500ms. `--json-stream` emits NDJSON to stdout per repaint. SIGINT/SIGTERM gives a final summary and exit 0.
- `pipe [--ref|--key] [--learn-key K] [--json-stream]` -- stdin to stdout byte-faithfully, with the tee at the reader, and wall-clock estimation on stderr. `--json-stream` replaces the status line with NDJSON on stderr, leaving stdout as the passthrough. At EOF it prints the summary, and `--learn-key` digests the run into that key unconditionally, because a pipe cannot see the upstream exit status. With neither `--key` nor `--ref`, `--learn-key` doubles as the reference key. A learning pipe streams to a capture file and arms the interrupt handler described in "Capture durability". A read error or a failed save keeps the file with recovery hints. Ctrl-C keeps it and exits 128+N without learning the truncated stream.
- `run [--ref|--key] [--learn|--learn-on-failure] -- CMD [ARGS...]` -- spawns CMD and passes both streams through. It feeds one estimator, mutex-shared across both stream goroutines and a 500ms ticker, and forwards SIGINT/SIGTERM. It propagates the exit code through the `osExit` seam, 128+N when signal N killed the child, by shell convention. The `syscall.WaitStatus` assertion lives in run.go without build tags, because the type exists on every GOOS the package builds on. `--learn` (requires `--key`) saves the run only on exit code 0. `--learn-on-failure` (implies `--learn`) saves it regardless. Both stream every consumed line to the capture file, and the digester sees stdout and stderr alike. A failed run keeps that file and prints the recovery command ("Capture durability").
- `auto -- CMD [ARGS...]` -- the magic default mode, where a bare `lpi CMD [ARGS...]` routes here (see "Automatic mode"). Every stored model feeds the fit chooser. The status line shows `identifying pattern` until the lock, then the normal bar with a `ref <label>` segment. It is always learning. Exit 0 merges into the fitted pattern at final rate >= mergeRate, or records a new `auto.<hash16>` pattern. A non-zero exit is never merged and keeps the capture file with the recovery command, exactly like `run --learn`. The only flag is `--db`.
- `learn --key K [--replace] LOG...` -- digests completed logs, transparently for gzip and for capture files. It prints per-log stats and the model summary, then removes any ingested captures inside the current db's `pending/` directory.
- `model list|show KEY|rm KEY` -- database management, honoring `--db`.

Live learning re-loads the key's model from disk before saving, so ad-hoc `--ref` runs mixed in for matching are never persisted.

Live-learning bootstrap lives in `resolveOrBootstrap` in refs.go. `run --learn` and `pipe --learn-key` treat a learn-target key with no stored model as "record the baseline" rather than an error. That holds when no `--ref` was given and any explicit `--key` equals the learn target. A `--key` naming a different key still errors, as does a `--key` combined with `--ref`. The mode prints `no model for key "K" yet -- recording baseline run` to stderr and runs the estimator against an empty model. The run is digested and saved under the usual rules, which are exit 0 or --learn-on-failure for run, and clean EOF for pipe. The next invocation then has a real reference. A failed baseline is not lost either. Its capture file is kept like any other failed learning run.

## Build and test

`go-toolchain` (no arguments) from the repo root: mod tidy, vet, tests with coverage enforcement, lint, build. CI runs the same via the `wow-look-at-my/go-toolchain@v1` action. Tests use `github.com/stretchr/testify` (`assert`/`require`) -- the toolchain's import fixer canonicalizes testify imports to that path.
