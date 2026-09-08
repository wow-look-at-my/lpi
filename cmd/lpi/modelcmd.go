package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/lpi/estimate"
	"github.com/wow-look-at-my/lpi/internal/render"
)

var modelDB string

var modelCmd = &cobra.Command{
	Use:   "model",
	Short: "Inspect and manage the model database",
}

var modelListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all learned models",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		store := estimate.OpenStore(modelDB)
		keys, err := store.Keys()
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "no models in %s\n", modelDB)
			return nil
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "KEY\tLABEL\tRUNS\tUNITS\tDURATION\tSIZE")
		for _, key := range keys {
			path := store.Path(key)
			m, err := store.Load(key)
			if err != nil {
				return fmt.Errorf("load %s: %w", path, err)
			}
			st, err := os.Stat(path)
			if err != nil {
				return err
			}
			dur := "-"
			if m.HasTimes() {
				dur = render.Duration(m.RefDuration())
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%dB\n",
				key, listLabel(m), len(m.Runs()), m.Units(), dur, st.Size())
		}
		return tw.Flush()
	},
}

// listLabelMax caps the LABEL column; invocation
const listLabelMax = 40

// listLabel is the LABEL column value: the most
func listLabel(m *estimate.Model) string {
	if len(m.Labels()) == 0 {
		return "-"
	}
	label := m.Label()
	if len(label) > listLabelMax {
		label = label[:listLabelMax] + "..."
	}
	return label
}

var modelShowCmd = &cobra.Command{
	Use:   "show KEY",
	Short: "Show a model's runs and merged totals",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := estimate.OpenStore(modelDB)
		path := store.Path(args[0])
		m, err := store.Load(args[0])
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("no model for key %q in %s (%s)", args[0], modelDB, availableKeys(modelDB))
			}
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "key:  %s\nfile: %s\n\n", m.Key(), path)
		tw := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "SOURCE\tLINES\tDURATION\tTIMES")
		for _, r := range m.Runs() {
			times := "no"
			if r.HasTimes {
				times = "yes"
			}
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", filepath.Base(r.Source), r.Lines, runDuration(r), times)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		fmt.Fprintf(out, "\nmerged: %d runs, %d units%s\n", len(m.Runs()), m.Units(), modelDuration(m))
		return nil
	},
}

var modelRmCmd = &cobra.Command{
	Use:   "rm KEY",
	Short: "Delete a model",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := estimate.OpenStore(modelDB)
		path := store.Path(args[0])
		if err := store.Remove(args[0]); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("no model for key %q in %s (%s)", args[0], modelDB, availableKeys(modelDB))
			}
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "removed model %q (%s)\n", args[0], path)
		return nil
	},
}

func init() {
	modelCmd.PersistentFlags().StringVar(&modelDB, "db", estimate.DefaultDir(),
		"model database directory")
	modelCmd.AddCommand(modelListCmd, modelShowCmd, modelRmCmd)
	rootCmd.AddCommand(modelCmd)
}
