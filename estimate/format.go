package estimate

import "github.com/wow-look-at-my/lpi/internal/timeparse"

// TimeFormat reads the timestamp off a line of log text. A nil TimeFormat reads none.
type TimeFormat = timeparse.Format

// CompileFormat builds a TimeFormat from a builtin name, a named-group regex, or a Go layout.
func CompileFormat(spec, layout string) (*TimeFormat, error) { return timeparse.Compile(spec, layout) }

// DetectFormat picks the TimeFormat that reads sample, and nil for unstamped text.
func DetectFormat(sample []string) *TimeFormat { return timeparse.Detect(sample) }

// FormatNames lists the builtin format names CompileFormat takes.
func FormatNames() []string { return timeparse.Names() }

// FormatGroups lists the regex group names CompileFormat takes.
func FormatGroups() []string { return timeparse.Groups() }
