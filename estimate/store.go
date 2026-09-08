package estimate

import (
	"os"

	"github.com/wow-look-at-my/lpi/internal/model"
)

// Store is a directory of models, a small gzipped file per key: the CLI's own database.
type Store struct {
	dir string
}

// DefaultDir is where the CLI keeps its models: $LPI_DB, else the user cache directory.
func DefaultDir() string { return model.DefaultDir() }

// OpenStore returns the store rooted at dir, or at DefaultDir when dir is
// empty. Nothing is created until a Save.
func OpenStore(dir string) *Store {
	if dir == "" {
		dir = model.DefaultDir()
	}
	return &Store{dir: dir}
}

// Dir is the directory the store reads and writes.
func (s *Store) Dir() string { return s.dir }

// Path is the file a key is stored at: unusable name bytes are replaced, so any key works.
func (s *Store) Path(key string) string { return model.PathForKey(s.dir, key) }

// Keys lists the stored keys, sorted. A missing directory holds none, and is no error.
func (s *Store) Keys() ([]string, error) { return model.Keys(s.dir) }

// Load reads the model under key, answering fs.ErrNotExist for a key never learned.
func (s *Store) Load(key string) (*Model, error) { return LoadModel(s.Path(key)) }

// Save writes m under its own key, creating the directory if needed.
func (s *Store) Save(m *Model) error { return m.Save(s.Path(m.Key())) }

// Remove deletes the model stored under key.
func (s *Store) Remove(key string) error { return os.Remove(s.Path(key)) }

// Models loads every stored model, which is what a Matcher is built from. A
// model that fails to load stops the walk: a corrupt database is worth hearing
// about, not skipping past.
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
