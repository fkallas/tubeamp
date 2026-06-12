// Package history is tubeamp's OWN local play-history store. Google removed
// watch-history from the public APIs, so this is not a YouTube feature: it records
// what the user plays in tubeamp itself, on disk, as the Library "History" source.
//
// The history is an ordered list of model.Track, most-recent first, deduped by
// VideoID (replaying a track moves it to the front) and capped. It is persisted as
// JSON at DataDir()/history.json with atomic 0600 writes; a missing or corrupt file
// reads as an empty history (never a crash). All operations are mutex-guarded.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/fkallas/tubeamp/internal/model"
)

// maxEntries caps the stored history; older entries fall off the end.
const maxEntries = 200

// Store is a file-backed play-history list. The zero value is not usable; build
// one with New. Safe for concurrent use.
type Store struct {
	mu   sync.Mutex
	path string
}

// New returns a Store backed by the file at path (DataDir()/history.json by
// convention). The file is created lazily on the first Record.
func New(path string) *Store {
	return &Store{path: path}
}

// Record moves track to the front of the history (most-recent first), removing any
// earlier entry with the same VideoID, caps the list, and writes it atomically.
// A track with no VideoID is ignored (nothing playable to record). A read of a
// missing/corrupt file is treated as an empty history, so Record always starts
// from a clean slate rather than failing.
func (s *Store) Record(track model.Track) error {
	if track.VideoID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tracks := s.load()
	deduped := make([]model.Track, 0, len(tracks)+1)
	deduped = append(deduped, track)
	for _, t := range tracks {
		if t.VideoID == track.VideoID {
			continue
		}
		deduped = append(deduped, t)
		if len(deduped) >= maxEntries {
			break
		}
	}
	return s.save(deduped)
}

// List returns the recorded tracks, most-recent first. A missing or corrupt file
// yields an empty slice (never an error or a crash).
func (s *Store) List() []model.Track {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// load reads and decodes the history file. Any error (missing/corrupt/permission)
// degrades to an empty slice — history is best-effort, never fatal.
func (s *Store) load() []model.Track {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var tracks []model.Track
	if err := json.Unmarshal(data, &tracks); err != nil {
		return nil // corrupt file => empty history
	}
	return tracks
}

// save writes tracks atomically (temp file + rename) with 0600 permissions,
// creating the parent directory (0700) as needed.
func (s *Store) save(tracks []model.Track) error {
	data, err := json.Marshal(tracks)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	// WriteFile honours umask, which can clear bits; force 0600 explicitly.
	if err := os.Chmod(tmp, 0600); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
