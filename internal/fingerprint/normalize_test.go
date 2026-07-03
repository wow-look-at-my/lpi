package fingerprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeRequiredCases(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "cmake percent",
			in:   "[ 42%] Building CXX object src/foo.cpp.o",
			want: "[ #%] Building CXX object src/foo.cpp.o",
		},
		{
			name: "iso timestamp line",
			in:   "2026-07-02T15:04:05.123Z INFO starting worker 7",
			want: "#-#-#T#:#:#.#Z INFO starting worker #",
		},
		{
			name: "make directory",
			in:   "make[2]: Entering directory '/build/x'",
			want: "make[#]: Entering directory '/build/x'",
		},
		{
			name: "git sha",
			in:   "commit a1b2c3d4e5f6",
			want: "commit #",
		},
		{
			name: "uuid",
			in:   "id=550e8400-e29b-41d4-a716-446655440000 done",
			want: "id=# done",
		},
		{
			name: "ansi colors and duration",
			in:   "\x1b[32mPASS\x1b[0m ok  pkg/foo  1.234s",
			want: "PASS ok pkg/foo #.#s",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Normalize(tc.in))
		})
	}
}

func TestNormalizeEdgeCases(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"only spaces", "   \t  ", ""},
		{"carriage return keeps last", "progress 10%\rprogress 99%", "progress #%"},
		{"trailing carriage return", "progress 10%\r", ""},
		{"whitespace collapse", "a \t  b\tc", "a b c"},
		{"leading trailing trim", "   hello world   ", "hello world"},
		{"hex without digit stays", "deadbeef", "deadbeef"},
		{"word stays", "Building", "Building"},
		{"digit run inside word", "foo123bar", "foo#bar"},
		{"sha256 keeps prefix", "sha256", "sha#"},
		{"short hex mixed", "a1b2c", "a#b#c"},
		{"hex six chars", "a1b2c3", "#"},
		{"hex eight upper", "ABCDEF12", "#"},
		{"hex prefix 0x", "addr 0x7fff5fbff8a0", "addr #"},
		{"hex prefix 0X", "0X1F", "#"},
		{"0x with non-hex", "0xzz", "#xzz"},
		{"bare 0x", "0x", "#x"},
		{"uuid uppercase", "550E8400-E29B-41D4-A716-446655440000", "#"},
		{"uuid embedded in token", "550e8400-e29b-41d4-a716-446655440000xyz", "#-e#b-#d#-a#-#xyz"},
		{"uuid too short tail", "550e8400-e29b-41d4-a716-44665544", "#-e#b-#d#-a#-#"},
		{"osc bel", "\x1b]0;window title\x07real text", "real text"},
		{"osc esc backslash", "\x1b]0;t\x1b\\rest", "rest"},
		{"two byte escape", "\x1b(Bfoo", "Bfoo"},
		{"unterminated csi", "\x1b[12", ""},
		{"bare esc at end", "abc\x1b", "abc"},
		{"csi cursor moves", "a\x1b[2Kb\x1b[1;31mc", "abc"},
		{"punctuation passes", "err: open(/tmp/x): E-2", "err: open(/tmp/x): E-#"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Normalize(tc.in))
		})
	}
}

func TestNormalizeCapsAt512(t *testing.T) {
	long := strings.Repeat("x", 600)
	got := Normalize(long)
	assert.Len(t, got, 512)
	assert.Equal(t, strings.Repeat("x", 512), got)

	// Multi-token long lines truncate to the same prefix a full pass yields.
	mixed := strings.Repeat("word 123 ", 200)
	got = Normalize(mixed)
	assert.LessOrEqual(t, len(got), 512)
	assert.True(t, strings.HasPrefix(got, "word # word # word #"))
}

func TestNormalizeIdempotent(t *testing.T) {
	inputs := []string{
		"[ 42%] Building CXX object src/foo.cpp.o",
		"2026-07-02T15:04:05.123Z INFO starting worker 7",
		"id=550e8400-e29b-41d4-a716-446655440000 done",
		"\x1b[32mPASS\x1b[0m ok  pkg/foo  1.234s",
		"addr 0x7fff5fbff8a0 len 128",
	}
	for _, in := range inputs {
		once := Normalize(in)
		assert.Equal(t, once, Normalize(once), "input %q", in)
	}
}

func BenchmarkNormalize(b *testing.B) {
	line := "\x1b[1;32m[ 87%]\x1b[0m 2026-07-02T15:04:05.123Z Building CXX object " +
		"src/render/pipeline_cache.cpp.o (hash a1b2c3d4e5f6, id 550e8400-e29b-41d4-a716-446655440000) took 1.234s"
	b.SetBytes(int64(len(line)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Normalize(line)
	}
}
