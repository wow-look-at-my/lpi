package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/lpi/internal/linescan"
	"github.com/wow-look-at-my/lpi/internal/model"
	"github.com/wow-look-at-my/lpi/internal/progress"
	"github.com/wow-look-at-my/lpi/internal/render"
)

var runOpts struct {
	rf             refFlags
	learn          bool
	learnOnFailure bool
}

var runCmd = &cobra.Command{
	Use:   "run -- CMD [ARGS...]",
	Short: "Run a command with live progress; optionally learn from it",
	Long: `Run spawns CMD, passes its stdout and stderr through byte-faithfully,
and shows live progress on stderr, e.g.:

  lpi run --key mybuild --learn -- make -j8

The command's exit code is propagated; a child killed by signal N exits
128+N, following the shell convention (SIGTERM -> 143). With --learn
(requires --key), the run is digested and saved into the key's model -- but
only when the command exits 0, so failed runs never pollute the reference.

A learning run also streams every consumed line to a capture file under
<db>/pending/ as it goes. When the run is learned the file is removed; when
the command fails (or the save fails) the file is kept and the exact
'lpi learn' command to recover it is printed, so a long run is never lost
to a final-moment failure. --learn-on-failure (implies --learn) saves the
run into the model even on a non-zero exit -- use it when a failed run's
log is still a representative reference.

With --learn and no --ref, a --key that has no model yet is not an error:
the first run is recorded as the key's baseline (no progress can be shown
yet) and the next invocation gets a real estimate. With a --ref, a missing
--key still errors.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.ArgsLenAtDash() < 0 {
			return errors.New("missing '--': usage is lpi run [flags] -- CMD [ARGS...]")
		}
		if cmd.ArgsLenAtDash() > 0 {
			return errors.New("unexpected argument before '--'")
		}
		if len(args) == 0 {
			return errors.New("no command given after '--'")
		}
		learning := runOpts.learn || runOpts.learnOnFailure
		if learning && runOpts.rf.key == "" {
			return errors.New("--learn requires --key (the model to save the run into)")
		}
		var (
			m         *model.Model
			bootstrap bool
			err       error
		)
		if learning {
			// The run will be recorded anyway, so a missing key
			m, bootstrap, err = runOpts.rf.resolveOrBootstrap(runOpts.rf.key)
		} else {
			m, err = runOpts.rf.resolve()
		}
		if err != nil {
			return err
		}
		errW := cmd.ErrOrStderr()
		if bootstrap {
			bootstrapNotice(errW, runOpts.rf.key)
		}
		r := render.New(errW)
		lv := &liveRun{est: progress.NewEstimator(m), r: r, msg: renderNotify(r)}
		if learning {
			source := sourceName("run", args)
			lv.dig = model.NewDigester(source, nil)
			lv.capture = newCapture(lv.msg, runOpts.rf.db, runOpts.rf.key, source)
		}
		exitCode, err := lv.execute(cmd, args)
		if err != nil {
			// Transport failure (e.g
			lv.r.Break()
			if lv.dig != nil {
				keepOrDiscardCapture(lv.msg, lv.dig, lv.capture, runOpts.rf.db, runOpts.rf.key)
			}
			return err
		}

		final := lv.est.Snapshot()
		lv.r.Close(final)
		fmt.Fprint(errW, render.Summary(final))
		if lv.dig != nil {
			if err := lv.finishLearn(errW, exitCode, args); err != nil {
				return err
			}
		}
		if exitCode != 0 {
			osExit(exitCode)
		}
		return nil
	},
}

// finishLearn completes the learning side of a run
func (lv *liveRun) finishLearn(errW io.Writer, exitCode int, args []string) error {
	db, key := runOpts.rf.db, runOpts.rf.key
	if exitCode != 0 && !runOpts.learnOnFailure {
		fmt.Fprintf(errW, "exit status %d -- run not learned\n", exitCode)
		keepOrDiscardCapture(lv.msg, lv.dig, lv.capture, db, key)
		return nil
	}
	run, err := lv.dig.Finish()
	if err != nil {
		// The only Finish failure is nonempty lines
		lv.capture.Discard()
		return fmt.Errorf("run not learned: %w", err)
	}
	if err := learnRun(errW, db, key, run, strings.Join(args, " ")); err != nil {
		keepCapture(lv.msg, lv.capture, db, key)
		return err
	}
	lv.capture.Discard()
	return nil
}

// feeder is the estimator-shaped dependency of
type feeder interface {
	Observe(string, time.Time)
	Tick(time.Time)
	Snapshot() progress.Snapshot
}

// liveRun is the shared live state of run invocation
type liveRun struct {
	mu      sync.Mutex
	est     feeder
	dig     *model.Digester
	capture *model.CaptureWriter
	r       *render.Renderer
	msg     notify
}

// execute spawns the child and pumps its output
func (lv *liveRun) execute(cmd *cobra.Command, args []string) (int, error) {
	child := exec.Command(args[0], args[1:]...)
	child.Stdin = os.Stdin
	outPipe, err := child.StdoutPipe()
	if err != nil {
		return 0, err
	}
	errPipe, err := child.StderrPipe()
	if err != nil {
		return 0, err
	}
	if err := child.Start(); err != nil {
		return 0, err
	}

	// Forward interrupts to the child; lpi itself
	sigc := make(chan os.Signal, 2)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func() {
		for s := range sigc {
			_ = child.Process.Signal(s)
		}
	}()

	// Both passthrough streams coordinate with the
	outW := lv.r.Passthrough(cmd.OutOrStdout(), &lv.mu)
	errW := lv.r.Passthrough(cmd.ErrOrStderr(), &lv.mu)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		lv.consume(outPipe, outW)
	}()
	go func() {
		defer wg.Done()
		lv.consume(errPipe, errW)
	}()

	tickStop := make(chan struct{})
	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-tickStop:
				return
			case <-ticker.C:
				lv.mu.Lock()
				lv.est.Tick(time.Now())
				lv.r.Update(lv.est.Snapshot())
				lv.mu.Unlock()
			}
		}
	}()

	wg.Wait()
	waitErr := child.Wait()
	close(tickStop)
	<-tickDone
	signal.Stop(sigc)
	close(sigc)

	if waitErr != nil {
		var ee *exec.ExitError
		if !errors.As(waitErr, &ee) {
			return 0, waitErr
		}
		return childExitCode(ee), nil
	}
	return 0, nil
}

// childExitCode maps the child's ExitError to the
func childExitCode(ee *exec.ExitError) int {
	if ws, ok := ee.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	if code := ee.ExitCode(); code > 0 {
		return code
	}
	return 1
}

// consume forwards child stream byte-faithfully
func (lv *liveRun) consume(pipe io.Reader, passthrough io.Writer) {
	sc := linescan.NewScanner(io.TeeReader(pipe, passthrough))
	for sc.Scan() {
		now := time.Now()
		lv.mu.Lock()
		lv.est.Observe(sc.Text(), now)
		if lv.dig != nil {
			lv.dig.LineAt(sc.Text(), now)
			if err := lv.capture.Add(sc.Text(), now); err != nil {
				lv.msg("warning: capture file disabled: %v", err)
			}
		}
		lv.r.Update(lv.est.Snapshot())
		lv.mu.Unlock()
	}
}

func init() {
	addRefFlags(runCmd, &runOpts.rf)
	runCmd.Flags().BoolVar(&runOpts.learn, "learn", false,
		"digest the run and save it into --key's model when CMD exits 0 (a key with no model yet records the baseline)")
	runCmd.Flags().BoolVar(&runOpts.learnOnFailure, "learn-on-failure", false,
		"save the run into --key's model even when CMD exits non-zero (implies --learn; the exit code still propagates)")
	rootCmd.AddCommand(runCmd)
}
