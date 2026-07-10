package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/log-progress-indicator/internal/model"
)

const (
	demoBuild1  = "../../testdata/demo/build1.log"
	demoBuild2  = "../../testdata/demo/build2.log"
	demoPartial = "../../testdata/demo/partial.log"
)

// resetCommand restores every flag in the tree to its default and clears
// pflag's sticky '--' position so commands can be executed repeatedly
// in-process.
func resetCommand(c *cobra.Command) {
	c.Flags().Init(c.Name(), pflag.ContinueOnError)
	reset := func(f *pflag.Flag) {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			_ = sv.Replace(nil)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	}
	c.Flags().VisitAll(reset)
	c.PersistentFlags().VisitAll(reset)
	for _, sub := range c.Commands() {
		resetCommand(sub)
	}
}

// execLpi runs the root command in-process and returns stdout, stderr, and
// the execution error. Args pass through routeArgs, so tests exercise the
// production magic-mode routing.
func execLpi(t *testing.T, stdin io.Reader, args ...string) (string, string, error) {
	t.Helper()
	resetCommand(rootCmd)
	var out, errOut bytes.Buffer
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	rootCmd.SetIn(stdin)
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetArgs(routeArgs(args))
	err := rootCmd.Execute()
	return out.String(), errOut.String(), err
}

// seedDemoModel learns the two demo builds into a fresh database directory
// under key "demo" and returns the directory.
func seedDemoModel(t *testing.T) string {
	t.Helper()
	db := t.TempDir()
	m := model.New("demo")
	for _, path := range []string{demoBuild1, demoBuild2} {
		run, err := model.DigestFile(path)
		require.NoError(t, err)
		m.AddRun(run)
	}
	require.NoError(t, m.Save(model.PathForKey(db, "demo")))
	return db
}

// loadModel loads a stored model or fails the test.
func loadModel(t *testing.T, db, key string) *model.Model {
	t.Helper()
	m, err := model.Load(model.PathForKey(db, key))
	require.NoError(t, err)
	return m
}

// pendingFiles lists the capture files under db's pending directory
// (empty when the directory does not exist).
func pendingFiles(t *testing.T, db string) []string {
	t.Helper()
	dir := model.PendingDir(db)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var paths []string
	for _, e := range entries {
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	return paths
}

// parseJSONLine unmarshals one JSON object into a generic map.
func parseJSONLine(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &m), "not valid JSON: %q", line)
	return m
}
