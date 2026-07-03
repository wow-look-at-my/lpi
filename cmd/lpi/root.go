package main

import (
	"os"

	"github.com/spf13/cobra"
)

// version is the build version; overridden at release time via -ldflags.
var version = "dev"

// osExit is a seam so tests can observe the exit code.
var osExit = os.Exit

var rootCmd = &cobra.Command{
	Use:   "lpi",
	Short: "Estimate progress and ETA of long-running tasks from their log output",
	Long: `lpi estimates how far along a long-running task is -- completion
percentage, units of work done, and ETA -- by fuzzy-matching its partial log
output against reference logs recorded from previous completed runs. Log
lines are normalized into stable templates (timestamps, counters, hashes,
and paths' variable parts are collapsed), matched order-free against the
reference, and weighted by the share of the reference run's time each line
accounts for, so long silent steps are honestly represented. Reference
models live in a small on-disk database managed with 'lpi learn' and
'lpi model'.

Quickstart:

  lpi learn --key mybuild old-build.log       # seed the model from a past run
  lpi run --key mybuild --learn -- make -j8   # live progress bar + ETA`,
	Version: version,
}

// Execute runs the root command and exits nonzero on error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		osExit(1)
	}
}
