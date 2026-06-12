// Package enrich fills the album and duration the OAuth-backed YouTube Data API
// omits, by enriching tracks from the anonymous InnerTube `next` endpoint and
// caching the results permanently.
//
// The Data API exposes neither a song's album nor its duration; anonymous
// InnerTube does (ytm.Client.TrackDetails). Because a song's album never changes,
// results are written to a store-backed PERMANENT cache (no expiry) keyed by
// videoID, so a library is enriched once and then filled instantly from disk on
// every later load. Enrichment is designed to run progressively in the background:
// Fill returns the still-missing videoIDs the UI can hand to EnrichMissing in
// Cmd-sized chunks; EnrichMissing fetches with bounded concurrency and a small
// politeness delay so tubeamp stays light on InnerTube.
package enrich

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/store"
)

const (
	// defaultConcurrency bounds in-flight InnerTube enrichment requests.
	defaultConcurrency = 4
	// defaultDelay is the per-request politeness pause each worker takes after a
	// fetch, so a large library does not hammer InnerTube.
	defaultDelay = 150 * time.Millisecond
)

// TrackDetailer is the InnerTube seam enrich fetches missing album/duration
// through. *ytm.Client satisfies it via TrackDetails. A nil TrackDetailer makes
// EnrichMissing a no-op (cache-only operation), so a tubeamp with no search client
// still fills from whatever the cache already holds.
type TrackDetailer interface {
	TrackDetails(ctx context.Context, videoID string) (album, albumID string, dur time.Duration, err error)
}

// Detail is one enriched track's album + duration, the unit stored in the cache
// and returned by EnrichMissing.
type Detail struct {
	VideoID  string        `json:"video_id"`
	Album    string        `json:"album"`
	AlbumID  string        `json:"album_id"`
	Duration time.Duration `json:"duration"`
}

// Enricher fills tracks from a permanent cache and, on cache miss, from InnerTube.
// It is safe for concurrent use.
type Enricher struct {
	cache       *store.Cache
	detailer    TrackDetailer // nil => cache-only (EnrichMissing is a no-op)
	concurrency int
	delay       time.Duration
}

// NewEnricher builds an Enricher over a permanent store.Cache (callers point it at
// config.CacheDir()/enrich) and the InnerTube seam d (pass nil for cache-only).
func NewEnricher(cache *store.Cache, d TrackDetailer) *Enricher {
	return &Enricher{
		cache:       cache,
		detailer:    d,
		concurrency: defaultConcurrency,
		delay:       defaultDelay,
	}
}

// Fill returns a copy of tracks with Album/AlbumID/Duration filled from the cache
// (only fields the track was missing are touched, so source data is never
// clobbered), alongside the deduped list of videoIDs that are still missing — a
// track is "missing" when it lacks an album id or a duration and the cache has no
// entry for it. The caller feeds the missing ids to EnrichMissing (in chunks),
// then re-runs Fill to pick up the freshly cached details.
func (e *Enricher) Fill(tracks []model.Track) (filled []model.Track, missing []string) {
	out := make([]model.Track, len(tracks))
	copy(out, tracks)

	seen := make(map[string]bool)
	for i := range out {
		vid := out[i].VideoID
		if vid == "" {
			continue
		}
		if d, ok := e.get(vid); ok {
			applyDetail(&out[i], d)
			continue
		}
		if out[i].AlbumID != "" && out[i].Duration != 0 {
			continue // already complete from the source; nothing to enrich
		}
		if !seen[vid] {
			seen[vid] = true
			missing = append(missing, vid)
		}
	}
	return out, missing
}

// EnrichMissing fetches album+duration for each id via the InnerTube seam, writing
// every successful result to the permanent cache, and returns the details fetched.
// Requests run with bounded concurrency (default 4) and a small per-request delay.
// A single fetch failure is skipped (partial result), not fatal. With a nil seam
// (cache-only Enricher) or no ids it is a no-op. A cancelled ctx stops further
// dispatch and returns the partial results gathered so far with ctx.Err().
func (e *Enricher) EnrichMissing(ctx context.Context, ids []string) ([]Detail, error) {
	if e.detailer == nil || len(ids) == 0 {
		return nil, nil
	}
	conc := e.concurrency
	if conc < 1 {
		conc = 1
	}

	idCh := make(chan string)
	var mu sync.Mutex
	var out []Detail
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for id := range idCh {
			album, albumID, dur, err := e.detailer.TrackDetails(ctx, id)
			if err == nil {
				d := Detail{VideoID: id, Album: album, AlbumID: albumID, Duration: dur}
				e.put(d)
				mu.Lock()
				out = append(out, d)
				mu.Unlock()
			}
			// Politeness pause (also paces failures), abortable on cancel.
			if e.delay > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(e.delay):
				}
			}
		}
	}

	wg.Add(conc)
	for i := 0; i < conc; i++ {
		go worker()
	}

	for _, id := range ids {
		select {
		case <-ctx.Done():
			close(idCh)
			wg.Wait()
			return out, ctx.Err()
		case idCh <- id:
		}
	}
	close(idCh)
	wg.Wait()
	return out, nil
}

// applyDetail fills only the fields the track is still missing, so an album-sourced
// track (already carrying its album/duration) is never overwritten.
func applyDetail(t *model.Track, d Detail) {
	if t.Album == "" {
		t.Album = d.Album
	}
	if t.AlbumID == "" {
		t.AlbumID = d.AlbumID
	}
	if t.Duration == 0 {
		t.Duration = d.Duration
	}
}

// get reads a cached Detail for videoID. A missing or corrupt entry is a miss.
func (e *Enricher) get(videoID string) (Detail, bool) {
	raw, ok := e.cache.Get(videoID, 0) // maxAge 0 => never expires
	if !ok {
		return Detail{}, false
	}
	var d Detail
	if err := json.Unmarshal(raw, &d); err != nil {
		return Detail{}, false
	}
	return d, true
}

// put writes a Detail to the permanent cache (best-effort: a cache write failure
// is non-fatal, the value is still returned to the caller).
func (e *Enricher) put(d Detail) {
	raw, err := json.Marshal(d)
	if err != nil {
		return
	}
	_ = e.cache.Put(d.VideoID, raw)
}
