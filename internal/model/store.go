package model

import (
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
)

// currentVersion is the on-disk model format version.
const currentVersion = 1

// envelope is the persisted form of a model; derived fields are rebuilt on
// load.
type envelope struct {
	Version int
	Key     string
	Runs    []*Run
}

// Save writes the model as a gzip-compressed gob envelope, creating parent
// directories as needed.
func (m *Model) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	if err := gob.NewEncoder(gz).Encode(envelope{Version: currentVersion, Key: m.Key, Runs: m.Runs}); err != nil {
		f.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Load reads a model written by Save, rejecting unknown versions, and
// rebuilds the derived fields.
func Load(path string) (*Model, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	var env envelope
	if err := gob.NewDecoder(gz).Decode(&env); err != nil {
		return nil, err
	}
	if env.Version != currentVersion {
		return nil, fmt.Errorf("model: unsupported version %d (want %d)", env.Version, currentVersion)
	}
	m := &Model{Key: env.Key, Runs: env.Runs}
	m.Rebuild()
	return m, nil
}

// DefaultDir returns the model database directory: $LPI_DB if set, else
// $XDG_CACHE_HOME/log-progress-indicator, else
// ~/.cache/log-progress-indicator (falling back to the system temp dir when
// no home directory is known).
func DefaultDir() string {
	if dir := os.Getenv("LPI_DB"); dir != "" {
		return dir
	}
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "log-progress-indicator")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "log-progress-indicator")
	}
	return filepath.Join(home, ".cache", "log-progress-indicator")
}

// PathForKey maps a model key to its file path under dir: the key is
// sanitized to [A-Za-z0-9._-] (any other byte becomes '_', an empty result
// becomes "default") and ".lpi" is appended.
func PathForKey(dir, key string) string {
	san := make([]byte, 0, len(key))
	for i := 0; i < len(key); i++ {
		if isSafeKeyByte(key[i]) {
			san = append(san, key[i])
		} else {
			san = append(san, '_')
		}
	}
	if len(san) == 0 {
		san = append(san, "default"...)
	}
	return filepath.Join(dir, string(san)+".lpi")
}

func isSafeKeyByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
		c == '.' || c == '_' || c == '-'
}
