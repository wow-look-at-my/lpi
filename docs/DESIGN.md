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
- `"none"` if no lines have been observed yet

Novel lines (fingerprint not in the model) and overflow lines (fingerprint
known but its reference occurrences are exhausted) are tracked separately.

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
        Key  string
        Runs []*Run
        // derived by Rebuild():
        Expect      map[uint64][]Occurrence
        TotalUnits  int
        RefDuration time.Duration
        HasTimes    bool
    }
    func New(key string) *Model
    func (m *Model) AddRun(r *Run) // FIFO-evict beyond MaxRuns=8, then Rebuild
    func (m *Model) Rebuild()
    func (m *Model) Save(path string) error
    func Load(path string) (*Model, error)
    func DefaultDir() string
    func PathForKey(dir, key string) string

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

`DigestFile` handles gzip transparently (sniffing the magic bytes), samples
the first 300 lines for `timeparse.Detect`, then digests the whole file.

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
`{Version: 1, Key, Runs}` (creating parent directories as needed); `Load`
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
        CurrentLines, MatchedLines, NovelLines, OverflowLines int
    }

`Observe` skips empty-normalized lines entirely. For each remaining line the
fingerprint is looked up in `model.Expect`: unknown -> novel; known with
occurrences remaining -> match (units done +1, weight done += that
occurrence's `WeightFrac`); known but exhausted -> overflow. `firstAt`/`lastAt`
are tracked from non-zero `at` values (`Tick` updates `lastAt` only, and the
clock never moves backwards); `Elapsed = lastAt - firstAt` once both are set.
`Progress = min(weightDone, 1)`. ETA, pace, and confidence follow the core
algorithm rules above.

### cmd/lpi

Cobra CLI, binary name `lpi`. This part ships only the root command with a
`version` variable wired in; subcommands (`analyze`, `watch`, `pipe`, `run`,
`learn`, `model`), the tailer, and the renderer come in a later stage.

## Build and test

`go-toolchain` (no arguments) from the repo root: mod tidy, vet, tests with
coverage enforcement, lint, build. CI runs the same via the
`wow-look-at-my/go-toolchain@v1` action. Tests use
`github.com/stretchr/testify` (`assert`/`require`) -- the toolchain's import
fixer canonicalizes testify imports to that path.
