package fingerprint

// constants
const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// returns the hash of s as-is
func Sum64(s string) uint64 {
	h := uint64(fnvOffset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime64
	}
	return h
}

// Fingerprint returns the hash of Normalize(line)
func Fingerprint(line string) uint64 {
	return Sum64(Normalize(line))
}
