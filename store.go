package lpi

import (
	"os"

	"github.com/wow-look-at-my/lpi/internal/model"
)

// Store is a directory of models, one small gzipped file per key. It is the
// same database the lpi command line reads and writes, so a library caller and
// the CLI can share references: what a program learns, "lpi model list" shows.
type Store struct {
	dir string
}

// DefaultDir is where the CLI keeps its models: $LPI_DB if set, else
// $XDG_CACHE_HOME/log-progress-indicator, else ~/.cache/log-progress-indicator.
func DefaultDir() string { return model.DefaultDir() }

// OpenStore returns the store rooted at dir, or at DefaultDir when dir is
// empty. Nothing is created until a Save. A caller that wants its own private
// database passes its own directory.
func OpenStore(dir string) *Store {
	if dir == "" {
		dir = model.DefaultDir()
	}
	return &Store{dir: dir}
}

// Dir is the directory the store reads and writes.
func (s *Store) Dir() string { return s.dir }

// Path is the file a key is stored at. Characters a file name cannot carry are
// replaced, so any string is a usable key.
func (s *Store) Path(key string) string { return model.PathForKey(s.dir, key) }

// Keys lists the stored keys, sorted. A store whose directory does not exist
// holds no keys, which is not an error.
func (s *Store) Keys() ([]string, error) { return model.Keys(s.dir) }

// Load reads the model stored under key. A key with no model returns an error
// satisfying errors.Is(err, fs.ErrNotExist), which is how a caller tells "not
// learned yet" from a real failure.
func (s *Store) Load(key string) (*Model, error) { return LoadModel(s.Path(key)) }

// Save writes m under its own key, creating the directory if needed.
func (s *Store) Save(m *Model) error { return m.Save(s.Path(m.Key())) }

// Remove deletes the model stored under key.
func (s *Store) Remove(key string) error { return os.Remove(s.Path(key)) }

// Models loads every stored model. It is what a Matcher is built from when the
// caller does not know which reference a live run will turn out to fit. A
// model that fails to load stops the walk: a corrupt database is worth
// hearing about, not skipping past.
func (s *Store) Models() ([]*Model, error) {
	keys, err := s.Keys()
	if err != nil {
		return nil, err
	}
	models := make([]*Model, 0, len(keys))
	for _, key := range keys {
		m, err := s.Load(key)
		if err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, nil
}
