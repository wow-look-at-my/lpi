package main

import (
	"os"

	"github.com/spf13/cobra"
)

// version is the build version; overridden at
var version = "dev"

// osExit is a seam so tests can observe the exit
var osExit = os.Exit

var rootCmd = &cobra.Command{
	Use:   "lpi",
	Short: "Estimate progress and ETA of long-running tasks from their log output",
	Long: `lpi puts a live progress bar and ETA on any long-running command:

  lpi CMD [ARGS...]     # e.g.:  lpi make -j8

Nothing to configure: lpi identifies which previously seen output pattern
the command's output belongs to -- by the output itself, never by the
command line -- shows live progress against it, and keeps learning on every
clean exit. Output it has never seen is recorded as a new pattern
automatically. A wrapped command whose name collides with an lpi subcommand
needs the explicit form 'lpi -- CMD [ARGS...]'.

Under the hood, lpi estimates how far along a task is -- completion
percentage, units of work done, and ETA -- by fuzzy-matching its partial log
output against reference logs recorded from previous completed runs. Log
lines are normalized into stable templates (timestamps, counters, hashes,
and paths' variable parts are collapsed), matched order-free against the
reference, and weighted by the share of the reference run's time each line
accounts for, so long silent steps are honestly represented. Reference
models live in a small on-disk database managed with 'lpi learn' and
'lpi model'.

Explicit keys for power users:

  lpi learn --key mybuild old-build.log       # seed the model from a past run
  lpi run --key mybuild --learn -- make -j8   # live progress bar + ETA`,
	Version: version,
}

// Execute routes the process arguments (a bare
func Execute() {
	rootCmd.SetArgs(routeArgs(os.Args[1:]))
	if err := rootCmd.Execute(); err != nil {
		osExit(1)
	}
}
