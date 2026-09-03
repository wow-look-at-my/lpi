package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// routeArgs implements the magic default mode: a
func routeArgs(args []string) []string {
	switch {
	case len(args) == 0:
		return args
	case args[0] == "--":
		return append([]string{"auto", "--"}, args[1:]...)
	case strings.HasPrefix(args[0], "-"):
		// Root flags like --help and --version
		return args
	case isSubcommandName(args[0]):
		// Subcommands always win over the magic path
		return args
	default:
		return append([]string{"auto", "--"}, args...)
	}
}

// isSubcommandName reports whether name is a
func isSubcommandName(name string) bool {
	switch name {
	case "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return true
	}
	for _, c := range rootCmd.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return true
		}
	}
	return false
}
