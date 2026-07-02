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
	out := setRoot(t, "--version")
	require.NoError(t, rootCmd.Execute())
	assert.Contains(t, out.String(), "dev")
}

func TestMainRunsHelp(t *testing.T) {
	out := setRoot(t, "--help")
	main()
	assert.Contains(t, out.String(), "lpi")
}

func TestExecuteExitsNonzeroOnError(t *testing.T) {
	code := -1
	osExit = func(c int) { code = c }
	defer func() { osExit = os.Exit }()
	setRoot(t, "--definitely-not-a-flag")
	Execute()
	assert.Equal(t, 1, code)
}
