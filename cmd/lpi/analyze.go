package main

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/lpi/estimate"
	"github.com/wow-look-at-my/lpi/internal/render"
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
present, elapsed time and a pace-adjusted ETA come from them. --format pins
how each line's stamp is read (a builtin name, or a regex with named
groups) instead of detecting it.`,
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
		format, err := analyzeOpts.rf.timeFormat()
		if err != nil {
			return err
		}
		est := estimate.NewEstimator(m, estimate.TokenOfLine)
		if err := analyzeReader(r, est, format); err != nil {
			return err
		}
		s := est.Estimate()
		if analyzeOpts.json {
			return writeJSONSnapshot(cmd.OutOrStdout(), s)
		}
		_, err = io.WriteString(cmd.OutOrStdout(), render.Summary(s))
		return err
	},
}

// analyzeReader buffers the lines for timestamp
func analyzeReader(r io.Reader, est *estimate.Estimator[string], format *estimate.TimeFormat) error {
	sc := estimate.NewScanner(r)
	det := estimate.NewDetector(format)
	for !det.Ready() && sc.Scan() {
		det.Add(sc.Text())
	}
	format, sample := det.Decide()
	feeder := newLineFeeder(est, format, false)
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
	addTimeFlags(analyzeCmd, &analyzeOpts.rf)
	analyzeCmd.Flags().BoolVar(&analyzeOpts.json, "json", false,
		"print a single JSON snapshot instead of the summary")
	rootCmd.AddCommand(analyzeCmd)
}
