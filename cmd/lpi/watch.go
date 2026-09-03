package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/log-progress-indicator/internal/progress"
	"github.com/wow-look-at-my/log-progress-indicator/internal/render"
	"github.com/wow-look-at-my/log-progress-indicator/internal/tailer"
	"github.com/wow-look-at-my/log-progress-indicator/internal/timeparse"
)

// newSignalContext is a seam so tests can substitute a cancellable context
// for the SIGINT/SIGTERM
var newSignalContext = func() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

var watchOpts struct {
	rf         refFlags
	fromStart  bool
	interval   time.Duration
	jsonStream bool
}

var watchCmd = &cobra.Command{
	Use:   "watch FILE",
	Short: "Follow a growing log file and show live progress",
	Long: `Watch tails FILE (following truncation and rotation, waiting for it
to appear if needed) and shows live progress on stderr. Pre-existing content
is read first by default -- the history is what the estimate is built from.

Timestamps are auto-detected from the first lines seen: when the file has
them, elapsed time and pace come from the log's own clock; otherwise the
wall clock is used. Stop with Ctrl-C to get a final summary.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := watchOpts.rf.resolve()
		if err != nil {
			return err
		}
		ctx, cancel := newSignalContext()
		defer cancel()
		lines := make(chan string, 512)
		tl := &tailer.Tailer{Path: args[0], FromStart: watchOpts.fromStart, Interval: watchOpts.interval}
		tailErr := make(chan error, 1)
		go func() { tailErr <- tl.Run(ctx, lines) }()

		w := &watcher{
			est:        progress.NewEstimator(m),
			r:          render.New(cmd.ErrOrStderr()),
			jsonStream: watchOpts.jsonStream,
		}
		// NDJSON snapshots are coordinated like child passthrough: when
		// stdout shares a terminal with the stderr status line, a painted
		// status is erased before each snapshot line and repainted after it
		// (a piped stdout is returned unwrapped).
		w.jsonW = w.r.Passthrough(cmd.OutOrStdout(), &w.mu)
		w.loop(lines)
		if err := <-tailErr; err != nil {
			// Rendering is abandoned, not closed: end any painted status so
			// the error report starts on a fresh line.
			w.r.Break()
			return err
		}
		w.finish(cmd)
		return nil
	},
}

// watcher holds the live state of watch invocation. The mutex
// serializes estimator access (progress.Estimator is not concurrency-safe)
// and doubles as the coordination lock of the NDJSON passthrough writer.
type watcher struct {
	mu         sync.Mutex
	est        *progress.Estimator
	feeder     *lineFeeder
	r          *render.Renderer
	jsonW      io.Writer
	jsonStream bool
	pending    []string // lines buffered until the time source is decided
}

// loop consumes line batches and ticks until the tailer closes the channel.
func (w *watcher) loop(lines <-chan string) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case ln, ok := <-lines:
			if !ok {
				return
			}
			w.handleBatch(w.drainBatch(ln, lines))
			w.update()
		case <-ticker.C:
			w.mu.Lock()
			if w.feeder == nil && len(w.pending) > 0 {
				// The initial burst has settled: commit to a time source.
				w.decideLocked()
			}
			if w.feeder != nil && w.feeder.wall {
				w.est.Tick(time.Now())
			}
			w.mu.Unlock()
			w.update()
		}
	}
}

// drainBatch collects everything immediately available after first, so a
// burst is fed and rendered as batch.
func (w *watcher) drainBatch(first string, lines <-chan string) []string {
	batch := []string{first}
	for {
		select {
		case ln, ok := <-lines:
			if !ok {
				return batch // closed; the next loop iteration exits
			}
			batch = append(batch, ln)
		default:
			return batch
		}
	}
}

func (w *watcher) handleBatch(batch []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.feeder == nil {
		w.pending = append(w.pending, batch...)
		if len(w.pending) >= detectLines {
			w.decideLocked()
		}
		return
	}
	for _, ln := range batch {
		w.feeder.feed(ln)
	}
}

// decideLocked picks the time source from the buffered lines -- the log's
// own timestamps if detected, the wall clock otherwise (never both) -- and
// replays the buffer. Callers hold w.mu.
func (w *watcher) decideLocked() {
	format := timeparse.Detect(w.pending)
	w.feeder = &lineFeeder{est: w.est, format: format, wall: format == nil}
	for _, ln := range w.pending {
		w.feeder.feed(ln)
	}
	w.pending = nil
}

// update repaints the status line and, when streaming, emits NDJSON
// snapshot.
func (w *watcher) update() {
	w.mu.Lock()
	s := w.est.Snapshot()
	w.mu.Unlock()
	w.r.Update(s)
	if w.jsonStream {
		_ = writeJSONSnapshot(w.jsonW, s)
	}
}

// finish flushes any undecided buffer and prints the final summary.
func (w *watcher) finish(cmd *cobra.Command) {
	w.mu.Lock()
	if w.feeder == nil && len(w.pending) > 0 {
		w.decideLocked()
	}
	final := w.est.Snapshot()
	w.mu.Unlock()
	w.r.Close(final)
	fmt.Fprint(cmd.ErrOrStderr(), render.Summary(final))
	if w.jsonStream {
		_ = writeJSONSnapshot(w.jsonW, final)
	}
}

func init() {
	addRefFlags(watchCmd, &watchOpts.rf)
	watchCmd.Flags().BoolVar(&watchOpts.fromStart, "from-start", true,
		"read the file's pre-existing content first")
	watchCmd.Flags().DurationVar(&watchOpts.interval, "interval", tailer.DefaultInterval,
		"poll interval for file changes")
	watchCmd.Flags().BoolVar(&watchOpts.jsonStream, "json-stream", false,
		"emit an NDJSON snapshot to stdout on every repaint")
	rootCmd.AddCommand(watchCmd)
}
