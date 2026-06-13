// Package history is tubeamp's OWN local play-history store. Google removed
// watch-history from the public APIs, so this is not a YouTube feature: it records
// what the user plays in tubeamp itself, on disk, as the Library "History" source.
//
// The history is an ordered list of model.Track, most-recent first, deduped by
// VideoID (replaying a track moves it to the front) and capped. Each entry
// carries the time it was played (RecordAt) and the list is kept ordered by that
// time, so records that ARRIVE out of order (concurrent Record goroutines under
// fast track-skipping) still land in play order. It is persisted as JSON at
// DataDir()/history.json with atomic 0600 writes; a missing or corrupt file
// reads as an empty history (never a crash); files from before the played_at
// field read fine (their entries order after any timestamped ones, original
// order kept). All operations are mutex-guarded.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fkallas/tubeamp/internal/model"
)

// maxEntries caps the stored history; older entries fall off the end.
const maxEntries = 200

// entry is one persisted history row: the track plus the time it was played.
// model.Track is embedded, so the JSON shape is the track's fields plus
// played_at — files written before played_at existed unmarshal with a zero
// PlayedAt (valid: they sort after any timestamped entry, keeping their order).
type entry struct {
	model.Track
	PlayedAt time.Time `json:"played_at,omitzero"`
}

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

// Record records track as played now: RecordAt(track, time.Now()).
func (s *Store) Record(track model.Track) error {
	return s.RecordAt(track, time.Now())
}

// RecordAt records track as played at the given time: any earlier entry with the
// same VideoID is removed, the entry is inserted in play order (most-recent
// first, BY at — so two records arriving out of order, e.g. concurrent Cmd
// goroutines under fast track-skipping, still land in the order they were
// played), the list is capped, and written atomically. Callers should capture
// at when the play happens, not when the write runs. A track with no VideoID is
// ignored (nothing playable to record). A read of a missing/corrupt file is
// treated as an empty history, so RecordAt always starts from a clean slate
// rather than failing.
func (s *Store) RecordAt(track model.Track, at time.Time) error {
	if track.VideoID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries := s.load()
	deduped := make([]entry, 0, len(entries)+1)
	for _, e := range entries {
		if e.VideoID == track.VideoID {
			continue
		}
		deduped = append(deduped, e)
	}
	// Insert before the first entry not played after at (the list is kept
	// sorted most-recent first; legacy zero-time entries sort last). An
	// equal-time tie goes to the later-arriving record (inserted in front).
	pos := len(deduped)
	for i, e := range deduped {
		if !e.PlayedAt.After(at) {
			pos = i
			break
		}
	}
	deduped = append(deduped, entry{})
	copy(deduped[pos+1:], deduped[pos:])
	deduped[pos] = entry{Track: track, PlayedAt: at}
	if len(deduped) > maxEntries {
		deduped = deduped[:maxEntries]
	}
	return s.save(deduped)
}

// List returns the recorded tracks, most-recent first. A missing or corrupt file
// yields an empty slice (never an error or a crash).
func (s *Store) List() []model.Track {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.load()
	tracks := make([]model.Track, len(entries))
	for i, e := range entries {
		tracks[i] = e.Track
	}
	return tracks
}

// load reads and decodes the history file. Any error (missing/corrupt/permission)
// degrades to an empty slice — history is best-effort, never fatal.
func (s *Store) load() []entry {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil // corrupt file => empty history
	}
	return entries
}

// save writes entries atomically (temp file + rename) with 0600 permissions,
// creating the parent directory (0700) as needed.
func (s *Store) save(entries []entry) error {
	data, err := json.Marshal(entries)
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
