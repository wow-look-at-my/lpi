package fingerprint

import (
	"hash/fnv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSum64MatchesStdlibFNV(t *testing.T) {
	inputs := []string{
		"",
		"a",
		"hello world",
		"[ #%] Building CXX object src/foo.cpp.o",
		"#-#-#T#:#:#.#Z INFO starting worker #",
	}
	for _, in := range inputs {
		h := fnv.New64a()
		_, err := h.Write([]byte(in))
		require.NoError(t, err)
		assert.Equal(t, h.Sum64(), Sum64(in), "input %q", in)
	}
}

func TestFingerprintIsHashOfNormalized(t *testing.T) {
	line := "[ 42%] Building CXX object src/foo.cpp.o"
	assert.Equal(t, Sum64(Normalize(line)), Fingerprint(line))
}

func TestFingerprintStableAcrossVariableContent(t *testing.T) {
	pairs := [][2]string{
		{"[ 42%] Building CXX object src/foo.cpp.o", "[ 87%] Building CXX object src/foo.cpp.o"},
		{"2026-07-02T15:04:05.123Z INFO starting worker 7", "2026-07-03T09:11:22.999Z INFO starting worker 12"},
		{"commit a1b2c3d4e5f6", "commit 9f8e7d6c5b4a"},
		{"id=550e8400-e29b-41d4-a716-446655440000 done", "id=123e4567-e89b-12d3-a456-426614174000 done"},
		{"\x1b[32mPASS\x1b[0m ok  pkg/foo  1.234s", "PASS ok pkg/foo 9.876s"},
	}
	for _, p := range pairs {
		assert.Equal(t, Fingerprint(p[0]), Fingerprint(p[1]), "%q vs %q", p[0], p[1])
	}
}

func TestFingerprintDistinguishesDifferentText(t *testing.T) {
	assert.NotEqual(t,
		Fingerprint("Building CXX object src/foo.cpp.o"),
		Fingerprint("Building CXX object src/bar.cpp.o"))
}
