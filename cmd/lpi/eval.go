package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/lpi/internal/eval"
	"github.com/wow-look-at-my/lpi/internal/model"
)

var evalOpts struct {
	rf     refFlags
	json   bool
	detail bool
	learn  bool
}

var evalCmd = &cobra.Command{
	Use:     "eval LOG...",
	Aliases: []string{"score", "backtest"},
	Short:   "Score lpi against complete logs whose real answer is known",
	Long: `Eval replays complete logs and grades the estimates lpi would have
given while each one was still running. Every line is compared with the truth
the log itself records -- its own clock, or its line count when it carries no
timestamps -- so the report says how wrong the progress and the ETA were, not
just that they existed.

With two or more logs each is scored against a model built from the OTHERS, so
no log is ever graded against itself. One log alone is scored against itself,
which is marked in the report because it flatters the result.

--key scores the logs against a stored model instead (a real holdout), and
--learn then adds them to that key once the scoring is done. Without --learn
nothing is written to the model database.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if evalOpts.learn && evalOpts.rf.key == "" {
			return errors.New("--learn needs --key NAME to learn into")
		}
		format, err := evalOpts.rf.timeFormat()
		if err != nil {
			return err
		}
		targets := make([]eval.Target, 0, len(args))
		for _, path := range args {
			run, err := model.DigestFileWith(path, format.Clone())
			if err != nil {
				return fmt.Errorf("digest %s: %w", path, err)
			}
			targets = append(targets, eval.Target{Path: path, Run: run})
		}

		var results []*eval.Result
		if evalOpts.rf.key != "" {
			m, err := model.Load(model.PathForKey(evalOpts.rf.db, evalOpts.rf.key))
			if err != nil {
				return err
			}
			results, err = eval.Against(m, targets, format)
			if err != nil {
				return err
			}
		} else if results, err = eval.LeaveOneOut(targets, format); err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		if evalOpts.json {
			if err := writeEvalJSON(out, results); err != nil {
				return err
			}
		} else {
			eval.Report(out, results, evalOpts.detail)
		}
		if !evalOpts.learn {
			return nil
		}
		return learnTargets(out, targets)
	},
}

// learnTargets stores every scored run under the --key model.
func learnTargets(w io.Writer, targets []eval.Target) error {
	db, key := evalOpts.rf.db, evalOpts.rf.key
	m, err := loadOrCreate(db, key)
	if err != nil {
		return err
	}
	for _, t := range targets {
		m.AddRun(t.Run)
	}
	dest := model.PathForKey(db, key)
	if err := m.Save(dest); err != nil {
		return err
	}
	fmt.Fprintf(w, "learned %d run(s) into %q: %d runs total -> %s\n",
		len(targets), key, len(m.Runs), dest)
	return nil
}

// evalJSON is the stable JSON form of a whole scoring run.
type evalJSON struct {
	Runs          []eval.JSONResult `json:"runs"`
	MeanAbsErr    float64           `json:"err_mean"`
	WorstAbsErr   float64           `json:"err_max"`
	MatchRate     float64           `json:"match_rate"`
	ETAMeanRelErr float64           `json:"eta_rel_err,omitempty"`
	SelfFit       bool              `json:"self_fit"`
	Grade         string            `json:"grade"`
	Verdict       string            `json:"verdict"`
}

func writeEvalJSON(w io.Writer, results []*eval.Result) error {
	o := eval.Aggregate(results)
	doc := evalJSON{
		MeanAbsErr: o.MeanAbsErr, WorstAbsErr: o.WorstAbsErr, MatchRate: o.MatchRate,
		ETAMeanRelErr: o.ETAMeanRelErr, SelfFit: o.SelfFit,
		Grade: o.Grade(), Verdict: eval.Verdict(o),
	}
	for _, r := range results {
		doc.Runs = append(doc.Runs, eval.JSON(r))
	}
	buf, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", buf)
	return err
}

func init() {
	addModelFlags(evalCmd, &evalOpts.rf)
	addTimeFlags(evalCmd, &evalOpts.rf)
	evalCmd.Flags().BoolVar(&evalOpts.json, "json", false,
		"print the scores as one JSON object instead of the table")
	evalCmd.Flags().BoolVar(&evalOpts.detail, "detail", false,
		"also print what lpi would have said at every tenth of each run")
	evalCmd.Flags().BoolVar(&evalOpts.learn, "learn", false,
		"after scoring, add the logs to the --key model")
	rootCmd.AddCommand(evalCmd)
}
