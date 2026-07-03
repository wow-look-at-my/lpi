package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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

var runOpts struct {
	rf    refFlags
	learn bool
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
		if runOpts.learn && runOpts.rf.key == "" {
			return errors.New("--learn requires --key (the model to save the run into)")
		}
		var (
			m         *model.Model
			bootstrap bool
			err       error
		)
		if runOpts.learn {
			// The run will be recorded anyway, so a missing key just means
			// this run becomes the baseline instead of being estimated.
			m, bootstrap, err = runOpts.rf.resolveOrBootstrap(runOpts.rf.key)
		} else {
			m, err = runOpts.rf.resolve()
		}
		if err != nil {
			return err
		}
		if bootstrap {
			bootstrapNotice(cmd.ErrOrStderr(), runOpts.rf.key)
		}
		lv := &liveRun{est: progress.NewEstimator(m), r: render.New(cmd.ErrOrStderr())}
		if runOpts.learn {
			lv.dig = model.NewDigester(sourceName("run", args), nil)
		}
		exitCode, err := lv.execute(cmd, args)
		if err != nil {
			return err
		}

		final := lv.est.Snapshot()
		lv.r.Close(final)
		errW := cmd.ErrOrStderr()
		fmt.Fprint(errW, render.Summary(final))
		if lv.dig != nil {
			if exitCode != 0 {
				fmt.Fprintf(errW, "exit status %d -- run not learned\n", exitCode)
			} else {
				run, err := lv.dig.Finish()
				if err != nil {
					return fmt.Errorf("run not learned: %w", err)
				}
				if err := learnRun(errW, runOpts.rf.db, runOpts.rf.key, run); err != nil {
					return err
				}
			}
		}
		if exitCode != 0 {
			osExit(exitCode)
		}
		return nil
	},
}

// liveRun is the shared live state of one run invocation. The mutex
// serializes the estimator, digester, and renderer across the stdout
// consumer, stderr consumer, and ticker goroutines; stderr passthrough
// writes share it via lockedWriter.
type liveRun struct {
	mu  sync.Mutex
	est *progress.Estimator
	dig *model.Digester
	r   *render.Renderer
}

// execute spawns the child and pumps its output until it exits, returning
// the child's exit code.
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

	// Forward interrupts to the child; lpi itself survives to print the
	// final summary and propagate the exit code.
	sigc := make(chan os.Signal, 2)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func() {
		for s := range sigc {
			_ = child.Process.Signal(s)
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		lv.consume(outPipe, cmd.OutOrStdout())
	}()
	go func() {
		defer wg.Done()
		lv.consume(errPipe, &lockedWriter{mu: &lv.mu, w: cmd.ErrOrStderr()})
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

// childExitCode maps the child's ExitError to the code lpi propagates: the
// child's own exit code, or 128+N when signal N killed it (the shell
// convention: SIGTERM -> 143). The syscall.WaitStatus assertion is kept in
// this one file rather than behind build tags because the type exists on
// every GOOS this package already builds on (it requires syscall.SIGTERM
// above); non-Unix implementations simply report Signaled() == false and
// fall through to the generic path.
func childExitCode(ee *exec.ExitError) int {
	if ws, ok := ee.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	if code := ee.ExitCode(); code > 0 {
		return code
	}
	return 1
}

// consume forwards one child stream byte-faithfully (the tee sits at the
// reader) while feeding its lines to the estimator with wall-clock times.
func (lv *liveRun) consume(pipe io.Reader, passthrough io.Writer) {
	sc := linescan.NewScanner(io.TeeReader(pipe, passthrough))
	for sc.Scan() {
		now := time.Now()
		lv.mu.Lock()
		lv.est.Observe(sc.Text(), now)
		if lv.dig != nil {
			lv.dig.LineAt(sc.Text(), now)
		}
		lv.r.Update(lv.est.Snapshot())
		lv.mu.Unlock()
	}
}

// lockedWriter serializes writes with a shared mutex so child-stderr
// passthrough and the renderer never interleave mid-write.
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
}

func init() {
	addRefFlags(runCmd, &runOpts.rf)
	runCmd.Flags().BoolVar(&runOpts.learn, "learn", false,
		"digest the run and save it into --key's model when CMD exits 0 (a key with no model yet records the baseline)")
	rootCmd.AddCommand(runCmd)
}
