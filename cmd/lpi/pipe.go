package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/log-progress-indicator/internal/linescan"
	"github.com/wow-look-at-my/log-progress-indicator/internal/model"
	"github.com/wow-look-at-my/log-progress-indicator/internal/progress"
	"github.com/wow-look-at-my/log-progress-indicator/internal/render"
)

var pipeOpts struct {
	rf         refFlags
	learnKey   string
	jsonStream bool
}

var pipeCmd = &cobra.Command{
	Use:   "pipe",
	Short: "Estimate progress of a stream while passing it through",
	Long: `Pipe reads stdin, forwards every byte to stdout unmodified, and
shows live progress on stderr, e.g.:

  docker build . 2>&1 | lpi pipe --key image

With --json-stream, NDJSON snapshots replace the human status line on stderr
(stdout stays the byte-faithful passthrough). With --learn-key, the stream
is also digested and saved under that key when stdin ends.

A learning pipe also streams every consumed line to a capture file under
<db>/pending/ as it goes. When the stream is learned the file is removed;
when learning fails -- a read error, a failed save, or Ctrl-C (which would
otherwise merge a truncated stream into the model at EOF) -- the file is
kept and the exact 'lpi learn' command to recover it is printed.

When neither --key nor --ref is given, --learn-key doubles as the reference
key; and when that key -- named by --key or defaulted this way -- has no
model yet, the stream is recorded as the key's first baseline instead of
erroring (no progress can be shown yet). An explicit --key naming a
different key than --learn-key must still exist, as must --key whenever a
--ref is also given.

Note that pipe cannot see the upstream command's exit status, so it learns
on EOF even if the upstream failed -- prefer 'lpi run', which only learns
from exit code 0.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var (
			m         *model.Model
			bootstrap bool
			err       error
		)
		if pipeOpts.learnKey != "" {
			m, bootstrap, err = pipeOpts.rf.resolveOrBootstrap(pipeOpts.learnKey)
		} else {
			m, err = pipeOpts.rf.resolve()
		}
		if err != nil {
			return err
		}
		errW := cmd.ErrOrStderr()
		if bootstrap {
			bootstrapNotice(errW, pipeOpts.learnKey)
		}
		est := progress.NewEstimator(m)
		var r *render.Renderer
		msg := plainNotify(errW)
		if !pipeOpts.jsonStream {
			r = render.New(errW)
			msg = renderNotify(r)
		}
		st := &pipeLearnState{}
		var dig *model.Digester
		var capture *model.CaptureWriter
		if pipeOpts.learnKey != "" {
			source := sourceName("pipe", nil)
			dig = model.NewDigester(source, nil)
			capture = newCapture(msg, pipeOpts.rf.db, pipeOpts.learnKey, source)
			stop := st.armInterrupt(msg, dig, capture, pipeOpts.rf.db, pipeOpts.learnKey)
			defer stop()
		}

		// The tee sits at the reader: every byte the line scanner consumes
		// has already been forwarded, so passthrough stays byte-faithful
		// even for overlong or binary lines. The renderer's Passthrough
		// keeps the status line off the forwarded bytes' terminal lines
		// when the two streams share a terminal.
		out := cmd.OutOrStdout()
		if r != nil {
			out = r.Passthrough(out, &st.mu)
		}
		sc := linescan.NewScanner(io.TeeReader(cmd.InOrStdin(), out))
		for sc.Scan() {
			now := time.Now()
			st.mu.Lock()
			if st.interrupted {
				// The handler already reported and exited; only a stubbed
				// osExit (tests) continues here. Keep the passthrough alive
				// (the tee already forwarded the bytes) but stop estimating.
				st.mu.Unlock()
				continue
			}
			est.Observe(sc.Text(), now)
			if dig != nil {
				dig.LineAt(sc.Text(), now)
				if err := capture.Add(sc.Text(), now); err != nil {
					msg("warning: capture file disabled: %v", err)
				}
			}
			s := est.Snapshot()
			if r != nil {
				r.Update(s)
			} else {
				_ = writeJSONSnapshot(errW, s)
			}
			st.mu.Unlock()
		}

		// Commit to the normal completion path: a signal from here on is
		// ignored by the handler (the stream is already complete, and the
		// save is atomic), and an earlier interrupt skips learning -- the
		// truncated stream must never be merged into the model.
		st.mu.Lock()
		if st.interrupted {
			st.mu.Unlock()
			return nil
		}
		st.finished = true
		st.mu.Unlock()

		if err := sc.Err(); err != nil {
			// Rendering is abandoned, not closed: end any painted status or
			// partial passthrough line so the recovery lines and the error
			// report start fresh.
			if r != nil {
				r.Break()
			}
			if dig != nil {
				keepOrDiscardCapture(msg, dig, capture, pipeOpts.rf.db, pipeOpts.learnKey)
			}
			return err
		}

		final := est.Snapshot()
		if r != nil {
			r.Close(final)
		} else {
			_ = writeJSONSnapshot(errW, final)
		}
		fmt.Fprint(errW, render.Summary(final))
		if dig == nil {
			return nil
		}
		run, err := dig.Finish()
		if err != nil {
			// The only Finish failure is <2 nonempty lines: nothing recoverable.
			capture.Discard()
			return fmt.Errorf("run not learned: %w", err)
		}
		if err := learnRun(errW, pipeOpts.rf.db, pipeOpts.learnKey, run); err != nil {
			keepCapture(msg, capture, pipeOpts.rf.db, pipeOpts.learnKey)
			return err
		}
		capture.Discard()
		return nil
	},
}

// pipeLearnState coordinates the scan loop, the EOF learning path, and the
// interrupt handler of one learning pipe invocation.
type pipeLearnState struct {
	mu          sync.Mutex
	interrupted bool
	finished    bool
}

// armInterrupt installs the SIGINT/SIGTERM handler for a learning pipe.
// Without it, the upstream process dying from the same Ctrl-C would EOF
// stdin and the unconditional EOF-learn would merge a truncated stream into
// the model. On a signal the handler keeps the capture file (already
// durable on disk), reports, and exits 128+N immediately; a signal arriving
// after the stream completed is ignored. Reporting goes through msg under
// st.mu -- the mutex the renderer is serialized by -- so the notice never
// gets glued onto a painted status line. The returned stop func disarms the
// handler.
func (st *pipeLearnState) armInterrupt(msg notify, dig *model.Digester, capture *model.CaptureWriter, db, key string) (stop func()) {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func() {
		s, ok := <-sigc
		if !ok {
			return
		}
		st.mu.Lock()
		if st.finished {
			st.mu.Unlock()
			return
		}
		st.interrupted = true
		msg("interrupted -- run not learned")
		keepOrDiscardCapture(msg, dig, capture, db, key)
		osExit(128 + signalNumber(s))
		st.mu.Unlock() // reached only when osExit is stubbed (tests)
	}()
	return func() {
		signal.Stop(sigc)
		close(sigc)
	}
}

// signalNumber maps a caught signal to its number for the 128+N convention.
func signalNumber(s os.Signal) int {
	if sig, ok := s.(syscall.Signal); ok {
		return int(sig)
	}
	return int(syscall.SIGINT)
}

func init() {
	addRefFlags(pipeCmd, &pipeOpts.rf)
	pipeCmd.Flags().StringVar(&pipeOpts.learnKey, "learn-key", "",
		"also digest the stream and save it under this key at EOF (doubles as the reference key when --key/--ref are absent)")
	pipeCmd.Flags().BoolVar(&pipeOpts.jsonStream, "json-stream", false,
		"emit NDJSON snapshots to stderr instead of the status line")
	rootCmd.AddCommand(pipeCmd)
}
