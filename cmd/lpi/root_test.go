package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setRoot(t *testing.T, args ...string) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	return &out
}

func TestVersionFlag(t *testing.T) {
	t.Serial()
	out := setRoot(t, "--version")
	require.NoError(t, rootCmd.Execute())
	assert.Contains(t, out.String(), "dev")
}

// setOSArgs pins os.Args for tests that go through
func setOSArgs(t *testing.T, args ...string) {
	t.Helper()
	old := os.Args
	os.Args = append([]string{"lpi"}, args...)
	t.Cleanup(func() { os.Args = old })
}

func TestMainRunsHelp(t *testing.T) {
	t.Serial()
	setOSArgs(t, "--help")
	out := setRoot(t, "--help")
	main()
	assert.Contains(t, out.String(), "lpi")
}

func TestExecuteExitsNonzeroOnError(t *testing.T) {
	t.Serial()
	code := -1
	osExit = func(c int) { code = c }
	defer func() { osExit = os.Exit }()
	setOSArgs(t, "--definitely-not-a-flag")
	setRoot(t, "--definitely-not-a-flag")
	Execute()
	assert.Equal(t, 1, code)
}
