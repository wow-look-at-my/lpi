package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/lpi/estimate"
	"github.com/wow-look-at-my/lpi/internal/render"
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
runs are evicted beyond ` + fmt.Sprint(estimate.MaxRuns) + `); --replace starts
the key from scratch instead. Gzipped logs are handled transparently, as are
the capture files a failed 'lpi run --learn' or 'lpi pipe --learn-key' keeps
under <db>/pending/ -- they replay with the exact per-line times of the
recorded run, and once learned they are removed from pending/.

Timestamps are auto-detected and the reader used is reported per file. For
stamps no builtin knows, --format takes a regex with named groups, e.g.
--format '^\((?P<time>[^)]+)\)' --time-layout '02.01.2006 15h04m05s', or
component groups such as year/month/day/hour/min/sec/frac/zone, or the
whole-stamp groups epoch/epochms/epochns. Lines the regex misses keep the
previous line's time, so a mix of stamped and unstamped lines is fine.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := learnOpts.rf.key
		if key == "" {
			return errors.New("--key is required")
		}
		db := learnOpts.rf.db
		m := estimate.NewModel(key)
		if !learnOpts.replace {
			var err error
			if m, err = loadOrCreate(db, key); err != nil {
				return err
			}
		}
		format, err := learnOpts.rf.timeFormat()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		for _, path := range args {
			run, err := estimate.RecordFileWith(path, format.Clone())
			if err != nil {
				return fmt.Errorf("digest %s: %w", path, err)
			}
			m.Add(run)
			fmt.Fprintf(out, "learned %s: %d lines, %s%s, %d unique fingerprints\n",
				path, run.Lines, runDuration(run), timeFormatNote(run), len(run.Occ))
		}
		store := estimate.OpenStore(db)
		dest := store.Path(key)
		if err := store.Save(m); err != nil {
			return err
		}
		fmt.Fprintf(out, "model %q: %d runs, %d units%s -> %s\n",
			key, len(m.Runs()), m.Units(), modelDuration(m), dest)
		removePendingCaptures(out, db, args)
		return nil
	},
}

// removePendingCaptures deletes ingested files that
func removePendingCaptures(w io.Writer, db string, paths []string) {
	pending, err := filepath.Abs(estimate.PendingDir(db))
	if err != nil {
		return
	}
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil || filepath.Dir(abs) != pending {
			continue
		}
		if os.Remove(abs) == nil {
			fmt.Fprintf(w, "removed pending capture: %s\n", path)
		}
	}
}

// timeFormatNote names the stamp reader a digest
func timeFormatNote(r *estimate.Run) string {
	if r.TimeFormat == "" {
		return ""
	}
	return " (" + r.TimeFormat + ")"
}

func runDuration(r *estimate.Run) string {
	if !r.HasTimes {
		return "no timestamps"
	}
	return render.Duration(r.Duration)
}

func modelDuration(m *estimate.Model) string {
	if !m.HasTimes() {
		return ", no timing data"
	}
	return " over " + render.Duration(m.RefDuration())
}

func init() {
	addModelFlags(learnCmd, &learnOpts.rf)
	addTimeFlags(learnCmd, &learnOpts.rf)
	learnCmd.Flags().BoolVar(&learnOpts.replace, "replace", false,
		"discard any existing runs under the key first")
	rootCmd.AddCommand(learnCmd)
}
