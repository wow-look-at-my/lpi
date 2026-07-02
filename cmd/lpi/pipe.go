package main

import (
	"fmt"
	"io"
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
is also digested and saved under that key when stdin ends. Note that pipe
cannot see the upstream command's exit status, so it learns on EOF even if
the upstream failed -- prefer 'lpi run', which only learns from exit code 0.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := pipeOpts.rf.resolve()
		if err != nil {
			return err
		}
		est := progress.NewEstimator(m)
		var dig *model.Digester
		if pipeOpts.learnKey != "" {
			dig = model.NewDigester(sourceName("pipe", nil), nil)
		}
		errW := cmd.ErrOrStderr()
		var r *render.Renderer
		if !pipeOpts.jsonStream {
			r = render.New(errW)
		}

		// The tee sits at the reader: every byte the line scanner consumes
		// has already been forwarded, so passthrough stays byte-faithful
		// even for overlong or binary lines.
		sc := linescan.NewScanner(io.TeeReader(cmd.InOrStdin(), cmd.OutOrStdout()))
		for sc.Scan() {
			now := time.Now()
			est.Observe(sc.Text(), now)
			if dig != nil {
				dig.LineAt(sc.Text(), now)
			}
			s := est.Snapshot()
			if r != nil {
				r.Update(s)
			} else {
				_ = writeJSONSnapshot(errW, s)
			}
		}
		if err := sc.Err(); err != nil {
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
			return fmt.Errorf("run not learned: %w", err)
		}
		return learnRun(errW, pipeOpts.rf.db, pipeOpts.learnKey, run)
	},
}

func init() {
	addRefFlags(pipeCmd, &pipeOpts.rf)
	pipeCmd.Flags().StringVar(&pipeOpts.learnKey, "learn-key", "",
		"also digest the stream and save it under this key at EOF")
	pipeCmd.Flags().BoolVar(&pipeOpts.jsonStream, "json-stream", false,
		"emit NDJSON snapshots to stderr instead of the status line")
	rootCmd.AddCommand(pipeCmd)
}
