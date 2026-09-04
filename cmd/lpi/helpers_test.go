package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/lpi/internal/model"
	"github.com/wow-look-at-my/lpi/internal/render"
)

const (
	demoBuild1  = "../../testdata/demo/build1.log"
	demoBuild2  = "../../testdata/demo/build2.log"
	demoPartial = "../../testdata/demo/partial.log"
)

// resetCommand restores every flag in the tree to
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

// execLpi runs the root command in-process and
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

// seedDemoModel learns the demo builds into a fresh
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

// loadModel loads a stored model or fails the test
func loadModel(t *testing.T, db, key string) *model.Model {
	t.Helper()
	m, err := model.Load(model.PathForKey(db, key))
	require.NoError(t, err)
	return m
}

// pendingFiles lists the capture files under db's
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

// parseJSONLine unmarshals JSON object into a
func parseJSONLine(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &m), "not valid JSON: %q", line)
	return m
}

// forceTTY pins render.IsTTY to a fixed answer for
func forceTTY(t *testing.T, tty bool) {
	t.Helper()
	old := render.IsTTY
	render.IsTTY = func(io.Writer) bool { return tty }
	t.Cleanup(func() { render.IsTTY = old })
}

// renderScrollback replays raw terminal bytes
func renderScrollback(raw string) []string {
	var lines []string
	var cur []byte
	col := 0
	for i := 0; i < len(raw); {
		switch {
		case raw[i] == '\n':
			lines = append(lines, string(cur))
			cur, col = cur[:0], 0
			i++
		case raw[i] == '\r':
			col = 0
			i++
		case strings.HasPrefix(raw[i:], "\x1b[K"):
			cur = cur[:col]
			i += 3
		default:
			if col < len(cur) {
				cur[col] = raw[i]
			} else {
				cur = append(cur, raw[i])
			}
			col++
			i++
		}
	}
	if len(cur) > 0 {
		lines = append(lines, string(cur))
	}
	return lines
}

var (
	// statusMark finds live-status text anywhere in a
	statusMark = regexp.MustCompile(`recording baseline  lines|identifying pattern  lines|\] \d+\.\d%  units `)
	statusFull = regexp.MustCompile(`^((recording baseline|identifying pattern)  lines \d+(  elapsed \S+)?` +
		`|\[[=> ]+\] \d+\.\d%  units .*  match \d+%(  ref .*)?)$`)
)

// assertStatusOwnsLines fails when any rendered
func assertStatusOwnsLines(t *testing.T, lines []string) {
	t.Helper()
	statuses := 0
	for _, ln := range lines {
		if !statusMark.MatchString(ln) {
			continue
		}
		if assert.True(t, statusFull.MatchString(ln),
			"status text must own its whole line, got %q", ln) {
			statuses++
		}
	}
	assert.Positive(t, statuses, "no status line rendered at all")
}
