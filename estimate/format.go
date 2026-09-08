package estimate

import "github.com/wow-look-at-my/lpi/internal/timeparse"

// TimeFormat reads the timestamp off a line of log text. A nil TimeFormat reads none.
type TimeFormat = timeparse.Format

// DetectLines is how many leading lines DetectFormat wants to see.
const DetectLines = timeparse.DetectLines

// Detector buffers the leading lines of a stream until it can pick a format.
type Detector = timeparse.Detector

// NewDetector returns a Detector, nil format to detect from the text itself.
func NewDetector(format *TimeFormat) *Detector { return timeparse.NewDetector(format) }

// Stamper is the clock of a line stream: stamps read, gaps carried, never backwards.
type Stamper = timeparse.Stamper

// NewStamper returns a Stamper reading stamps with format, nil to supply your own times.
func NewStamper(format *TimeFormat) *Stamper { return timeparse.NewStamper(format) }

// CompileFormat builds a TimeFormat from a builtin name, a named-group regex, or a Go layout.
func CompileFormat(spec, layout string) (*TimeFormat, error) { return timeparse.Compile(spec, layout) }

// DetectFormat picks the TimeFormat that reads sample, and nil for unstamped text.
func DetectFormat(sample []string) *TimeFormat { return timeparse.Detect(sample) }

// FormatNames lists the builtin format names CompileFormat takes.
func FormatNames() []string { return timeparse.Names() }

// FormatGroups lists the regex group names CompileFormat takes.
func FormatGroups() []string { return timeparse.Groups() }
