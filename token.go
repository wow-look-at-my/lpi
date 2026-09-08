package lpi

import "github.com/wow-look-at-my/lpi/internal/fingerprint"

// Token identifies one unit of work a task emits. Equal tokens are the same
// unit of work, across runs and across machines: the hash is stable, so a
// Model outlives the process that recorded it.
type Token uint64

// TokenOf hashes s exactly as given. Use it when the caller already holds an
// identifier -- a test name, a step id, a filename, an event type. Anything
// varying between runs (a counter, a duration, a pid) must be stripped by the
// caller first, or every run emits tokens no other run can match.
func TokenOf(s string) Token { return Token(fingerprint.Sum64(s)) }

// TokenOfLine hashes a raw line of log text. The line is normalized first:
// ANSI escapes are stripped, the text after the last carriage return wins, and
// timestamps, counters, hex hashes and UUIDs collapse to a placeholder, so two
// lines that differ only in that noise share one token. ok is false for a line
// with nothing identifying left, which the caller drops.
func TokenOfLine(line string) (tok Token, ok bool) {
	norm := fingerprint.Normalize(line)
	if norm == "" {
		return 0, false
	}
	return Token(fingerprint.Sum64(norm)), true
}

// Normalize returns the template TokenOfLine hashes. It is exported for
// callers that want to see, log or test what a line collapsed to.
func Normalize(line string) string { return fingerprint.Normalize(line) }
