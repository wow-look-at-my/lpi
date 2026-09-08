package estimate

import "github.com/wow-look-at-my/lpi/internal/fingerprint"

// Token identifies a unit of work. The hash is stable, so a Model outlives the process that recorded it.
type Token uint64

// TokenOf hashes s as given: the caller must strip whatever varies run to run.
func TokenOf(s string) Token { return Token(fingerprint.Sum64(s)) }

// TokenOfLine normalizes raw log text -- ANSI, stamps, counters, hex ids --
// then hashes it. ok is false for a line with nothing identifying left.
func TokenOfLine(line string) (tok Token, ok bool) {
	norm := fingerprint.Normalize(line)
	if norm == "" {
		return 0, false
	}
	return Token(fingerprint.Sum64(norm)), true
}

// Normalize returns the template TokenOfLine hashes, for logging and tests.
func Normalize(line string) string { return fingerprint.Normalize(line) }
