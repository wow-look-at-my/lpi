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
output against reference logs recorded from previous completed runs.`,
	Version: version,
}

// Execute runs the root command and exits nonzero on error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		osExit(1)
	}
}
