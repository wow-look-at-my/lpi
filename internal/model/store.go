package model

import (
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
)

// currentVersion is the on-disk model format version
const currentVersion = 1

// envelope is the persisted form of a model
type envelope struct {
	Version     int
	Key         string
	Runs        []*Run
	Invocations []string
}

// Save writes the model as a gzip-compressed gob
func (m *Model) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	if err := writeEnvelope(tmp, m); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// writeEnvelope encodes the model onto f as gzipped
func writeEnvelope(f *os.File, m *Model) error {
	gz := gzip.NewWriter(f)
	env := envelope{Version: currentVersion, Key: m.Key, Runs: m.Runs, Invocations: m.Invocations}
	if err := gob.NewEncoder(gz).Encode(env); err != nil {
		return err
	}
	return gz.Close()
}

// Load reads a model written by Save, rejecting
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
	m := &Model{Key: env.Key, Runs: env.Runs, Invocations: env.Invocations}
	m.Rebuild()
	return m, nil
}

// DefaultDir returns the model database directory
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

// PathForKey maps a model key to its file path
func PathForKey(dir, key string) string {
	return filepath.Join(dir, sanitizeKey(key)+".lpi")
}

// sanitizeKey maps a model key to a safe file-name
func sanitizeKey(key string) string {
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
	return string(san)
}

func isSafeKeyByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
		c == '.' || c == '_' || c == '-'
}
