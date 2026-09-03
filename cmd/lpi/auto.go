package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/log-progress-indicator/internal/model"
	"github.com/wow-look-at-my/log-progress-indicator/internal/progress"
	"github.com/wow-look-at-my/log-progress-indicator/internal/render"
)

var autoOpts struct {
	db string
}

var autoCmd = &cobra.Command{
	Use:   "auto -- CMD [ARGS...]",
	Short: "Run a command with automatic pattern detection and learning (the default mode)",
	Long: `Auto is what a plain 'lpi CMD [ARGS...]' routes to: it runs CMD with
live progress and zero configuration. The identity of a run is its OUTPUT,
never its command line -- every stored pattern is matched against the live
output, and the best fit supplies the progress estimate (the status line
shows 'identifying pattern' until the fit locks, then the normal bar with
a 'ref <label>' tag). Output lpi has never seen before is recorded as a
new pattern automatically, so the next run with the same output shape gets
live progress; recognized output refines its pattern on every clean exit.

Failed runs (non-zero exit) are never merged into a pattern: the captured
log is kept under <db>/pending/ and the exact 'lpi learn' command to
recover it is printed, exactly like 'lpi run --learn'. The command's exit
code propagates either way.

A wrapped command whose name collides with an lpi subcommand needs the
explicit form 'lpi -- CMD [ARGS...]'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.ArgsLenAtDash() < 0 {
			return errors.New("missing '--': usage is lpi auto [flags] -- CMD [ARGS...]")
		}
		if cmd.ArgsLenAtDash() > 0 {
			return errors.New("unexpected argument before '--'")
		}
		if len(args) == 0 {
			return errors.New("no command given after '--'")
		}
		errW := cmd.ErrOrStderr()
		cands, err := loadCandidates(errW, autoOpts.db)
		if err != nil {
			return err
		}
		ch := progress.NewChooser(cands)
		r := render.New(errW)
		lv := &liveRun{est: ch, r: r, msg: renderNotify(r)}
		source := sourceName("auto", args)
		lv.dig = model.NewDigester(source, nil)
		lv.capture = newCapture(lv.msg, autoOpts.db, "auto", source)

		exitCode, err := lv.execute(cmd, args)
		if err != nil {
			// Transport failure (e.g. the command could not start): end any
			// in-progress status line so the error report starts fresh, and
			// keep whatever was captured if it is worth keeping.
			lv.r.Break()
			keepAutoCapture(lv.msg, ch, lv.dig, lv.capture, autoOpts.db)
			return err
		}

		final := lv.est.Snapshot()
		lv.r.Close(final)
		fmt.Fprint(errW, render.Summary(final))
		if err := finishAutoLearn(errW, lv.msg, autoOpts.db, args, exitCode, ch, lv.dig, lv.capture); err != nil {
			return err
		}
		if exitCode != 0 {
			osExit(exitCode)
		}
		return nil
	},
}

// loadCandidates offers every stored model to the fit chooser. A missing
// database means candidates (the first ever run), and a model that
// fails to load is warned about and skipped: corrupt file must not
// break the magic path.
func loadCandidates(warnW io.Writer, db string) ([]progress.Candidate, error) {
	entries, err := os.ReadDir(db)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cands []progress.Candidate
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".lpi")
		if !ok || e.IsDir() {
			continue
		}
		m, err := model.Load(model.PathForKey(db, name))
		if err != nil {
			fmt.Fprintf(warnW, "warning: skipping model %s: %v\n", e.Name(), err)
			continue
		}
		cands = append(cands, progress.Candidate{Key: name, Label: m.DisplayLabel(), Model: m})
	}
	return cands, nil
}

// autoRecoveryKey is the key a kept capture should be learned into: the
// fitted pattern when the fit was solid enough to merge, else the run's
// content-derived id -- which is where a re-run of the same output would
// have landed anyway.
func autoRecoveryKey(ch *progress.Chooser, run *model.Run) string {
	if key, _, ok := ch.MergeTarget(); ok {
		return key
	}
	return model.AutoKey(run)
}

// keepAutoCapture keeps the capture file with recovery instructions when
// dig holds anything recoverable, and removes it otherwise. It is
// keepOrDiscardCapture with the key computed after Finish: the auto
// recovery key needs the finished run.
func keepAutoCapture(msg notify, ch *progress.Chooser, dig *model.Digester, capture *model.CaptureWriter, db string) {
	run, err := dig.Finish()
	if err != nil {
		capture.Discard()
		return
	}
	keepCapture(msg, capture, db, autoRecoveryKey(ch, run))
}

// finishAutoLearn completes the always-learning side of an auto run. A
// clean exit merges the run into the fitted pattern when the fit cleared
// the merge bar, else records a new pattern under its content-derived id
// (an existing model under that id means this exact output shape was
// recorded before -- same pattern -- so the run merges into it). A clean
// run too short to digest nonempty lines) has nothing to learn: a
// notice, not an error, so the child's exit code survives. A
// failed run is never merged: the capture file is kept and the recovery
// command printed, exactly like run --learn. Rendering is closed by the
// time this runs; direct errW prints are fine, but the shared capture
// helpers take the notify seam, so msg rides along.
func finishAutoLearn(errW io.Writer, msg notify, db string, args []string, exitCode int, ch *progress.Chooser, dig *model.Digester, capture *model.CaptureWriter) error {
	if exitCode != 0 {
		fmt.Fprintf(errW, "exit status %d -- run not learned\n", exitCode)
		keepAutoCapture(msg, ch, dig, capture, db)
		return nil
	}
	run, err := dig.Finish()
	if err != nil {
		// The only Finish failure is nonempty lines: nothing recoverable,
		// and on a clean exit nothing to learn is not an error -- wrapping a
		// quick command must never turn its success into a failure. Print a
		// notice and preserve the child's exit code
		capture.Discard()
		fmt.Fprintln(errW, "nothing to learn -- fewer than 2 nonempty output lines")
		return nil
	}
	invocation := strings.Join(args, " ")
	if key, _, ok := ch.MergeTarget(); ok {
		if err := learnRun(errW, db, key, run, invocation); err != nil {
			keepCapture(msg, capture, db, key)
			return err
		}
		capture.Discard()
		return nil
	}
	id := model.AutoKey(run)
	if _, err := os.Stat(model.PathForKey(db, id)); err == nil {
		// The id is a content hash: an existing file means this exact
		// output shape was recorded before, so it IS the same pattern
		// (this also catches runs too short to ever lock).
		if err := learnRun(errW, db, id, run, invocation); err != nil {
			keepCapture(msg, capture, db, id)
			return err
		}
		capture.Discard()
		return nil
	}
	m := model.New(id)
	m.AddInvocation(invocation)
	m.AddRun(run)
	if err := m.Save(model.PathForKey(db, id)); err != nil {
		keepCapture(msg, capture, db, id)
		return err
	}
	fmt.Fprintf(errW, "recorded new pattern %q (%s) -- %d lines, %s\n",
		invocation, id, run.Lines, render.Duration(run.Duration))
	fmt.Fprintln(errW, "future runs with this output shape will show live progress")
	capture.Discard()
	return nil
}

func init() {
	autoCmd.Flags().StringVar(&autoOpts.db, "db", model.DefaultDir(), "model database directory")
	rootCmd.AddCommand(autoCmd)
}
