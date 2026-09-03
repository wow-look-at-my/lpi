package main

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/log-progress-indicator/internal/linescan"
	"github.com/wow-look-at-my/log-progress-indicator/internal/progress"
	"github.com/wow-look-at-my/log-progress-indicator/internal/render"
	"github.com/wow-look-at-my/log-progress-indicator/internal/timeparse"
)

var analyzeOpts struct {
	rf   refFlags
	json bool
}

var analyzeCmd = &cobra.Command{
	Use:   "analyze CURRENT.log",
	Short: "Estimate progress of a partial log captured on disk",
	Long: `Analyze estimates how far along a task is from a partial log file,
matched against the reference model. Pass '-' to read the partial log from
stdin. Timestamps are auto-detected from the first ` + "300" + ` lines; when
present, elapsed time and a pace-adjusted ETA come from them.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := analyzeOpts.rf.resolve()
		if err != nil {
			return err
		}
		var r io.Reader
		if args[0] == "-" {
			r = cmd.InOrStdin()
		} else {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			r = f
		}
		est := progress.NewEstimator(m)
		if err := analyzeReader(r, est); err != nil {
			return err
		}
		s := est.Snapshot()
		if analyzeOpts.json {
			return writeJSONSnapshot(cmd.OutOrStdout(), s)
		}
		_, err = io.WriteString(cmd.OutOrStdout(), render.Summary(s))
		return err
	},
}

// analyzeReader buffers the lines for timestamp
func analyzeReader(r io.Reader, est *progress.Estimator) error {
	sc := linescan.NewScanner(r)
	var sample []string
	for len(sample) < detectLines && sc.Scan() {
		sample = append(sample, sc.Text())
	}
	feeder := &lineFeeder{est: est, format: timeparse.Detect(sample)}
	for _, ln := range sample {
		feeder.feed(ln)
	}
	for sc.Scan() {
		feeder.feed(sc.Text())
	}
	return sc.Err()
}

func init() {
	addRefFlags(analyzeCmd, &analyzeOpts.rf)
	analyzeCmd.Flags().BoolVar(&analyzeOpts.json, "json", false,
		"print a single JSON snapshot instead of the summary")
	rootCmd.AddCommand(analyzeCmd)
}
