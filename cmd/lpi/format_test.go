package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// demoOdd carries stamps no builtin parser recognizes, plus unstamped lines.
const demoOdd = "../../testdata/demo/oddstamps.log"

// oddRegex pulls the parenthesized stamp out; oddLayout reads it.
const (
	oddRegex  = `^\((?P<time>[^)]+)\)`
	oddLayout = "02.01.2006 15h04m05s"
)

func TestLearnAutodetectNamesTheFormat(t *testing.T) {
	t.Serial()
	db := t.TempDir()
	out, _, err := execLpi(t, nil, "learn", "--db", db, "--key", "demo", demoBuild1)
	require.NoError(t, err)
	assert.Contains(t, out, "(clock)", "an autodetected log says how it was read")

	m := loadModel(t, db, "demo")
	require.Len(t, m.Runs, 1)
	assert.Equal(t, "clock", m.Runs[0].TimeFormat)
}

func TestLearnUnreadableStampsFallBackToPosition(t *testing.T) {
	t.Serial()
	db := t.TempDir()
	out, _, err := execLpi(t, nil, "learn", "--db", db, "--key", "odd", demoOdd)
	require.NoError(t, err)
	assert.Contains(t, out, "no timestamps",
		"without a format the odd stamps are unreadable")

	m := loadModel(t, db, "odd")
	assert.False(t, m.HasTimes)
}

func TestLearnCustomRegexAndLayout(t *testing.T) {
	t.Serial()
	db := t.TempDir()
	out, _, err := execLpi(t, nil, "learn", "--db", db, "--key", "odd",
		"--format", oddRegex, "--time-layout", oddLayout, demoOdd)
	require.NoError(t, err)
	assert.Contains(t, out, "(custom)")
	assert.Contains(t, out, "3m53s", "the stamps span the whole build")

	m := loadModel(t, db, "odd")
	require.Len(t, m.Runs, 1)
	assert.True(t, m.HasTimes)
	assert.Equal(t, 12, m.Runs[0].Lines, "unstamped lines are digested too")
}

func TestLearnComponentGroups(t *testing.T) {
	t.Serial()
	db := t.TempDir()
	spec := `^\((?P<day>\d\d)\.(?P<month>\d\d)\.(?P<year>\d{4}) ` +
		`(?P<hour>\d\d)h(?P<min>\d\d)m(?P<sec>\d\d)s\)`
	_, _, err := execLpi(t, nil, "learn", "--db", db, "--key", "odd", "--format", spec, demoOdd)
	require.NoError(t, err)

	m := loadModel(t, db, "odd")
	assert.True(t, m.HasTimes)
	assert.Equal(t, "custom", m.Runs[0].TimeFormat)
}

func TestLearnBuiltinFormatByName(t *testing.T) {
	t.Serial()
	db := t.TempDir()
	out, _, err := execLpi(t, nil, "learn", "--db", db, "--key", "demo",
		"--format", "clock", demoBuild1)
	require.NoError(t, err)
	assert.Contains(t, out, "(clock)")
}

func TestFormatErrorsReachTheUser(t *testing.T) {
	t.Serial()
	db := t.TempDir()
	for _, tc := range []struct{ name, format, layout, want string }{
		{"unknown name", "rfc9999", "", "unknown format"},
		{"bad regex", `regex:(?P<time>[`, "", "--format regex"},
		{"no known group", `(?P<stamp>\d+)`, "", "names no timestamp group"},
		{"layout on builtin", "clock", "15:04:05", "does not apply"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"learn", "--db", db, "--key", "odd", "--format", tc.format}
			if tc.layout != "" {
				args = append(args, "--time-layout", tc.layout)
			}
			_, _, err := execLpi(t, nil, append(args, demoOdd)...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestAnalyzeHonorsCustomFormat(t *testing.T) {
	t.Serial()
	db := t.TempDir()
	_, _, err := execLpi(t, nil, "learn", "--db", db, "--key", "odd",
		"--format", oddRegex, "--time-layout", oddLayout, demoOdd)
	require.NoError(t, err)

	partial := strings.Join([]string{
		"(04.03.2026 05h06m07s) configure: checking build system type",
		"(04.03.2026 05h06m11s) configure: checking for gcc",
		"(04.03.2026 05h06m20s) configure: creating config.status",
		"(04.03.2026 05h07m02s) make: entering directory src",
	}, "\n") + "\n"

	out, _, err := execLpi(t, strings.NewReader(partial), "analyze", "--db", db, "--key", "odd",
		"--format", oddRegex, "--time-layout", oddLayout, "--json", "-")
	require.NoError(t, err)
	snap := parseJSONLine(t, out)
	assert.True(t, snap["elapsed_known"].(bool), "the custom stamps give a real elapsed time")
	assert.InDelta(t, 55.0, snap["elapsed_seconds"].(float64), 0.001)
}

func TestRefLogsUseTheFormatFlag(t *testing.T) {
	t.Serial()
	out, _, err := execLpi(t, strings.NewReader("(04.03.2026 05h07m05s) CC parser.o\n"),
		"analyze", "--ref", demoOdd, "--format", oddRegex, "--time-layout", oddLayout,
		"--json", "-")
	require.NoError(t, err)
	snap := parseJSONLine(t, out)
	assert.True(t, snap["has_times"].(bool), "--ref digests with the same format")
	assert.InDelta(t, 233.0, snap["ref_duration_seconds"].(float64), 0.001)
}
