package player

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/fkallas/tubeamp/internal/model"
)

// The mpv playlist IS the queue. These methods drive the daemon's playlist and
// keep an in-memory, richly-typed mirror that is persisted to queue.json so a
// re-attaching client can rebuild full metadata (mpv only remembers titles for
// entries it has actually played).

// trackURL resolves the load URL for a track. The default is the canonical YT
// Music watch URL; tests override urlFunc to load local files.
func (p *Player) trackURL(t model.Track) string {
	if p.urlFunc != nil {
		return p.urlFunc(t)
	}
	return t.URL()
}

// getTracks returns a copy of the in-memory queue model.
func (p *Player) getTracks() []model.Track {
	p.tracksMu.RLock()
	defer p.tracksMu.RUnlock()
	return append([]model.Track(nil), p.tracks...)
}

// setTracks replaces the in-memory queue model.
func (p *Player) setTracks(ts []model.Track) {
	p.tracksMu.Lock()
	p.tracks = ts
	p.tracksMu.Unlock()
}

// loadEntry issues one loadfile for a track. mpv >= 0.38 takes the 5-element
// form ["loadfile", url, mode, index, options]; we pass index -1 (append at the
// natural position) and force-media-title as an options map so bare mpv
// consumers (and the sidecar fallback) see the track name. The map form avoids
// the comma-escaping pitfalls of the options *string* form.
func (p *Player) loadEntry(t model.Track, mode string) error {
	if t.Title != "" {
		_, err := p.command("loadfile", p.trackURL(t), mode, -1,
			map[string]string{"force-media-title": t.Title})
		return err
	}
	_, err := p.command("loadfile", p.trackURL(t), mode)
	return err
}

// PlaylistReplace replaces the entire playlist with ts and begins playing the
// entry at start (clamped). An empty ts clears the playlist. The old playlist
// is cleared with stop, the new entries are appended while idle, and only then
// is playlist-pos set — so mpv never transiently loads entry 0 (resolving it
// via yt-dlp for nothing) or reports playlist-pos=0 when start > 0. Persists
// the sidecar.
func (p *Player) PlaylistReplace(ts []model.Track, start int) error {
	p.plMu.Lock()
	defer p.plMu.Unlock()

	// stop clears the current playlist and idles; the appends below do not
	// start playback on their own.
	if _, err := p.command("stop"); err != nil {
		return err
	}
	if len(ts) == 0 {
		p.setTracks(nil)
		return p.persist(nil)
	}

	for _, t := range ts {
		if err := p.loadEntry(t, "append"); err != nil {
			return err
		}
	}
	if start < 0 {
		start = 0
	}
	if start >= len(ts) {
		start = len(ts) - 1
	}
	// Setting playlist-pos on the idle player starts playback at start directly.
	if _, err := p.command("set_property", "playlist-pos", start); err != nil {
		return err
	}
	p.ensurePlaying()
	cp := append([]model.Track(nil), ts...)
	p.setTracks(cp)
	return p.persist(cp)
}

// ensurePlaying clears any paused state so an explicit track change starts
// playing regardless of whether playback was previously paused. mpv keeps the
// pause property across playlist-pos/loadfile changes, so without this, picking
// a new song while paused would load it paused. mpv emits a pause
// property-change in response, so the UI's play/pause indicator (which tracks
// EvPause) updates to match. Best-effort: a failure does not abort the
// already-issued track change.
func (p *Player) ensurePlaying() {
	_, _ = p.command("set_property", "pause", false)
}

// PlaylistAppend appends tracks to the end of the playlist. Persists the sidecar.
func (p *Player) PlaylistAppend(ts ...model.Track) error {
	if len(ts) == 0 {
		return nil
	}
	p.plMu.Lock()
	defer p.plMu.Unlock()
	// Re-sync with the live playlist first so mutations made by another
	// attached client since our last sync are not clobbered when we persist.
	base := p.syncedTracks()
	for _, t := range ts {
		if err := p.loadEntry(t, "append"); err != nil {
			return err
		}
	}
	cur := append(append([]model.Track(nil), base...), ts...)
	p.setTracks(cur)
	return p.persist(cur)
}

// PlaylistRemove removes the entry at index i. Out-of-range is a no-op. mpv
// advances to the next entry if the removed one was current. Persists the sidecar.
func (p *Player) PlaylistRemove(i int) error {
	p.plMu.Lock()
	defer p.plMu.Unlock()
	cur := p.syncedTracks()
	if i < 0 || i >= len(cur) {
		return nil
	}
	if _, err := p.command("playlist-remove", i); err != nil {
		return err
	}
	cur = append(cur[:i], cur[i+1:]...)
	p.setTracks(cur)
	return p.persist(cur)
}

// PlaylistMove moves the entry at index i to index j. The semantics match
// mpv's playlist-move (for i < j the entry lands at j-1). Persists the sidecar.
func (p *Player) PlaylistMove(i, j int) error {
	p.plMu.Lock()
	defer p.plMu.Unlock()
	cur := p.syncedTracks()
	if i < 0 || i >= len(cur) || j < 0 || j >= len(cur) || i == j {
		return nil
	}
	if _, err := p.command("playlist-move", i, j); err != nil {
		return err
	}
	cur = moveTrack(cur, i, j)
	p.setTracks(cur)
	return p.persist(cur)
}

// PlaylistJump makes the entry at index i the current one and plays it. No
// sidecar write (the position is read live by Snapshot, not stored).
func (p *Player) PlaylistJump(i int) error {
	p.plMu.Lock()
	defer p.plMu.Unlock()
	if i < 0 {
		return nil
	}
	if _, err := p.command("set_property", "playlist-pos", i); err != nil {
		return err
	}
	p.ensurePlaying()
	return nil
}

// Next advances to the next playlist entry (no-op at the end of the queue). An
// explicit skip always resumes playback even if it was paused.
func (p *Player) Next() error {
	if _, err := p.command("playlist-next", "weak"); err != nil {
		return err
	}
	p.ensurePlaying()
	return nil
}

// Prev returns to the previous playlist entry (no-op at the start). An explicit
// skip always resumes playback even if it was paused.
func (p *Player) Prev() error {
	if _, err := p.command("playlist-prev", "weak"); err != nil {
		return err
	}
	p.ensurePlaying()
	return nil
}

// PlaylistClear empties the playlist and stops playback. Identical to Stop —
// mpv's stop already clears the playlist, and both reset the mirror + sidecar.
func (p *Player) PlaylistClear() error {
	return p.Stop()
}

// moveTrack returns ts with the element at i relocated to index j using mpv's
// playlist-move convention (for i < j the element ends at j-1).
func moveTrack(ts []model.Track, i, j int) []model.Track {
	item := ts[i]
	rest := append(ts[:i:i], ts[i+1:]...)
	insertPos := j
	if i < j {
		insertPos = j - 1
	}
	if insertPos >= len(rest) {
		return append(rest, item)
	}
	out := make([]model.Track, 0, len(rest)+1)
	out = append(out, rest[:insertPos]...)
	out = append(out, item)
	out = append(out, rest[insertPos:]...)
	return out
}

// Snapshot captures the full daemon state for a re-attaching client: the queue
// (sidecar reconciled against the live playlist), the current playlist index,
// and the pause/position/duration/volume/mute properties. A missing or corrupt
// queue.json never errors — the queue degrades to titles from mpv's playlist.
type Snapshot struct {
	Tracks      []model.Track
	PlaylistPos int // current playing index, -1 when idle
	Paused      bool
	TimePos     float64 // seconds
	Duration    float64 // seconds
	Volume      int
	Mute        bool
}

// Snapshot gathers the current daemon state.
func (p *Player) Snapshot() (Snapshot, error) {
	p.plMu.Lock()
	defer p.plMu.Unlock()

	s := Snapshot{PlaylistPos: -1}
	if ts, ok := p.reconcileTracks(); ok {
		s.Tracks = ts
		// Keep the in-memory model in sync so later index-based mutations are valid.
		p.setTracks(append([]model.Track(nil), ts...))
	} else {
		// Live playlist unreadable: report this client's mirror rather than
		// pretending the queue is empty.
		s.Tracks = p.getTracks()
	}

	if raw, err := p.getProp("playlist-pos"); err == nil {
		if n, ok := intData(raw); ok {
			s.PlaylistPos = n
		}
	}
	if raw, err := p.getProp("pause"); err == nil {
		if b, ok := boolData(raw); ok {
			s.Paused = b
		}
	}
	if raw, err := p.getProp("time-pos"); err == nil {
		if f, ok := floatData(raw); ok {
			s.TimePos = f
		}
	}
	if raw, err := p.getProp("duration"); err == nil {
		if f, ok := floatData(raw); ok {
			s.Duration = f
		}
	}
	if raw, err := p.getProp("volume"); err == nil {
		if f, ok := floatData(raw); ok {
			s.Volume = int(f)
		}
	}
	if raw, err := p.getProp("mute"); err == nil {
		if b, ok := boolData(raw); ok {
			s.Mute = b
		}
	}
	return s, nil
}

// getProp fetches a property's raw JSON value via get_property.
func (p *Player) getProp(name string) (json.RawMessage, error) {
	resp, err := p.command("get_property", name)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// playlistEntry is the subset of mpv's playlist property we consume.
type playlistEntry struct {
	Filename string `json:"filename"`
	Title    string `json:"title"`
}

// livePlaylist reads mpv's current playlist. ok is false when the property
// cannot be read or parsed — callers must not mistake that for an empty
// playlist.
func (p *Player) livePlaylist() ([]playlistEntry, bool) {
	raw, err := p.getProp("playlist")
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var entries []playlistEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, false
	}
	return entries, true
}

// reconcileTracks merges the sidecar queue with the live mpv playlist entry by
// entry: a live entry whose filename matches the URL we would load the
// same-index sidecar track with keeps the sidecar's rich metadata; any other
// entry (another attached client mutated the shared playlist; missing or
// corrupt sidecar) degrades to a bare track built from the live entry.
// Matching on identity — not just on length — means rich metadata is never
// reported for the wrong track. ok is false when the live playlist itself
// could not be read.
func (p *Player) reconcileTracks() ([]model.Track, bool) {
	live, ok := p.livePlaylist()
	if !ok {
		return nil, false
	}
	disk, derr := readQueueFile(p.queuePath)
	out := make([]model.Track, len(live))
	for i, e := range live {
		if derr == nil && i < len(disk) && p.trackURL(disk[i]) == e.Filename {
			out[i] = disk[i]
		} else {
			out[i] = trackFromEntry(e)
		}
	}
	return out, true
}

// syncedTracks returns the freshest available queue model: the live playlist
// reconciled with the sidecar when readable, otherwise this client's own
// in-memory mirror. Mutations base themselves on it so changes made by other
// attached clients since our last sync are not clobbered when we persist.
func (p *Player) syncedTracks() []model.Track {
	if ts, ok := p.reconcileTracks(); ok {
		p.setTracks(ts)
		return ts
	}
	return p.getTracks()
}

// trackFromEntry builds a degraded Track from a bare mpv playlist entry.
func trackFromEntry(e playlistEntry) model.Track {
	t := model.Track{VideoID: videoIDFromURL(e.Filename), Title: e.Title}
	if t.Title == "" {
		t.Title = titleFromFilename(e.Filename)
	}
	return t
}

// videoIDFromURL extracts the v= parameter from a YouTube/YT-Music watch URL,
// returning "" for anything else (e.g. a local file path).
func videoIDFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Query().Get("v")
}

// titleFromFilename derives a display title from a file path or URL.
func titleFromFilename(raw string) string {
	base := raw
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	if base == "" {
		return raw
	}
	return base
}

// queueFile is the on-disk sidecar shape.
type queueFile struct {
	Tracks []model.Track `json:"tracks"`
}

// readQueueFile loads the sidecar queue. A missing or corrupt file yields an
// error the caller treats as "no sidecar" rather than a crash.
func readQueueFile(path string) ([]model.Track, error) {
	if path == "" {
		return nil, fmt.Errorf("player: no queue path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var qf queueFile
	if err := json.Unmarshal(data, &qf); err != nil {
		return nil, fmt.Errorf("player: parse queue.json: %w", err)
	}
	return qf.Tracks, nil
}

// persist writes the sidecar queue atomically (temp file + rename). It is a
// no-op when no queue path is configured (e.g. tests using a bare fake socket).
func (p *Player) persist(ts []model.Track) error {
	if p.queuePath == "" {
		return nil
	}
	data, err := json.MarshalIndent(queueFile{Tracks: ts}, "", "  ")
	if err != nil {
		return fmt.Errorf("player: marshal queue.json: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p.queuePath), 0o755); err != nil {
		return fmt.Errorf("player: queue dir: %w", err)
	}
	tmp := p.queuePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("player: write queue.json: %w", err)
	}
	if err := os.Rename(tmp, p.queuePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("player: rename queue.json: %w", err)
	}
	return nil
}
