package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// routeArgs implements the magic default mode: a first argument that is
// neither a flag nor a registered subcommand is treated as a command to run
// under 'auto', with '--' inserted so the wrapped command's own flags
// (lpi make -j8) are never parsed as lpi flags. "lpi -- CMD" is the
// explicit escape for wrapped commands whose name collides with an lpi
// subcommand.
func routeArgs(args []string) []string {
	switch {
	case len(args) == 0:
		return args
	case args[0] == "--":
		return append([]string{"auto", "--"}, args[1:]...)
	case strings.HasPrefix(args[0], "-"):
		// Root flags like --help and --version.
		return args
	case isSubcommandName(args[0]):
		// Subcommands always win over the magic path.
		return args
	default:
		return append([]string{"auto", "--"}, args...)
	}
}

// isSubcommandName reports whether name is a registered subcommand or alias
// on the root command, or one of cobra's implicit commands (help,
// completion, and the hidden shell-completion entry points), which are not
// registered until Execute runs.
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
