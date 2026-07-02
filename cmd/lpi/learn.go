package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/log-progress-indicator/internal/model"
	"github.com/wow-look-at-my/log-progress-indicator/internal/render"
)

var learnOpts struct {
	rf      refFlags
	replace bool
}

var learnCmd = &cobra.Command{
	Use:   "learn --key NAME LOG...",
	Short: "Digest completed run logs into a named model",
	Long: `Learn digests one or more logs of COMPLETED runs and stores them
under a model key. Later invocations add runs to the same key (the oldest
runs are evicted beyond ` + fmt.Sprint(model.MaxRuns) + `); --replace starts
the key from scratch instead. Gzipped logs are handled transparently.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := learnOpts.rf.key
		if key == "" {
			return errors.New("--key is required")
		}
		db := learnOpts.rf.db
		m := model.New(key)
		if !learnOpts.replace {
			var err error
			if m, err = loadOrCreate(db, key); err != nil {
				return err
			}
		}
		out := cmd.OutOrStdout()
		for _, path := range args {
			run, err := model.DigestFile(path)
			if err != nil {
				return fmt.Errorf("digest %s: %w", path, err)
			}
			m.AddRun(run)
			fmt.Fprintf(out, "learned %s: %d lines, %s, %d unique fingerprints\n",
				path, run.Lines, runDuration(run), len(run.Occ))
		}
		dest := model.PathForKey(db, key)
		if err := m.Save(dest); err != nil {
			return err
		}
		fmt.Fprintf(out, "model %q: %d runs, %d units%s -> %s\n",
			key, len(m.Runs), m.TotalUnits, modelDuration(m), dest)
		return nil
	},
}

func runDuration(r *model.Run) string {
	if !r.HasTimes {
		return "no timestamps"
	}
	return render.Duration(r.Duration)
}

func modelDuration(m *model.Model) string {
	if !m.HasTimes {
		return ", no timing data"
	}
	return " over " + render.Duration(m.RefDuration)
}

func init() {
	addModelFlags(learnCmd, &learnOpts.rf)
	learnCmd.Flags().BoolVar(&learnOpts.replace, "replace", false,
		"discard any existing runs under the key first")
	rootCmd.AddCommand(learnCmd)
}
