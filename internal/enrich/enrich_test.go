package enrich

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/store"
)

// fakeDetailer is an in-memory TrackDetailer that records concurrency and call
// counts. details maps videoID -> (album, albumID, dur); a missing id errors.
type fakeDetailer struct {
	mu      sync.Mutex
	details map[string]Detail
	calls   int32
	cur     int32
	maxCur  int32
	block   time.Duration // forces overlap so concurrency is observable
}

func (f *fakeDetailer) TrackDetails(ctx context.Context, videoID string) (string, string, time.Duration, error) {
	atomic.AddInt32(&f.calls, 1)
	n := atomic.AddInt32(&f.cur, 1)
	for {
		m := atomic.LoadInt32(&f.maxCur)
		if n <= m || atomic.CompareAndSwapInt32(&f.maxCur, m, n) {
			break
		}
	}
	if f.block > 0 {
		time.Sleep(f.block)
	}
	atomic.AddInt32(&f.cur, -1)

	f.mu.Lock()
	d, ok := f.details[videoID]
	f.mu.Unlock()
	if !ok {
		return "", "", 0, errors.New("no details for " + videoID)
	}
	return d.Album, d.AlbumID, d.Duration, nil
}

func newEnricherForTest(t *testing.T, d TrackDetailer) *Enricher {
	t.Helper()
	e := NewEnricher(store.NewCache(t.TempDir()), d)
	e.delay = 0 // keep tests fast
	return e
}

// TestFill_cacheHitFillsWithoutNetwork pre-populates the cache and asserts Fill
// fills from disk with no detailer calls and reports nothing missing.
func TestFill_cacheHitFillsWithoutNetwork(t *testing.T) {
	fake := &fakeDetailer{details: map[string]Detail{}}
	e := newEnricherForTest(t, fake)

	// Seed the cache directly via put.
	e.put(Detail{VideoID: "v1", Album: "Cached", AlbumID: "MPRE1", Duration: 3 * time.Minute})

	tracks := []model.Track{{VideoID: "v1", Title: "Song"}}
	filled, missing := e.Fill(tracks)
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none (cache hit)", missing)
	}
	if filled[0].Album != "Cached" || filled[0].AlbumID != "MPRE1" || filled[0].Duration != 3*time.Minute {
		t.Errorf("filled[0] = %+v, want filled from cache", filled[0])
	}
	if got := atomic.LoadInt32(&fake.calls); got != 0 {
		t.Errorf("detailer calls = %d, want 0 (Fill must not hit the network)", got)
	}
	// The source track must not be mutated (Fill copies).
	if tracks[0].Album != "" {
		t.Errorf("source track mutated: %+v", tracks[0])
	}
}

// TestFill_reportsMissingAndNotComplete checks the missing set: a cache-miss track
// lacking album/duration is reported; an already-complete track is not.
func TestFill_reportsMissingAndNotComplete(t *testing.T) {
	e := newEnricherForTest(t, &fakeDetailer{details: map[string]Detail{}})
	tracks := []model.Track{
		{VideoID: "miss1"}, // needs enrichment
		{VideoID: "miss1"}, // dup -> reported once
		{VideoID: "done", AlbumID: "MPREx", Duration: time.Minute}, // already complete
		{VideoID: ""}, // no id -> ignored
	}
	_, missing := e.Fill(tracks)
	if len(missing) != 1 || missing[0] != "miss1" {
		t.Errorf("missing = %v, want [miss1]", missing)
	}
}

// TestEnrichMissing_fetchesAndCaches asserts EnrichMissing fetches via the seam,
// caches each result, and that a subsequent Fill then needs no network.
func TestEnrichMissing_fetchesAndCaches(t *testing.T) {
	fake := &fakeDetailer{details: map[string]Detail{
		"a": {Album: "AlbA", AlbumID: "MPREa", Duration: time.Minute},
		"b": {Album: "AlbB", AlbumID: "MPREb", Duration: 2 * time.Minute},
	}}
	e := newEnricherForTest(t, fake)

	got, err := e.EnrichMissing(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("EnrichMissing: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d details, want 2", len(got))
	}

	// Second Fill is served entirely from cache.
	calls0 := atomic.LoadInt32(&fake.calls)
	filled, missing := e.Fill([]model.Track{{VideoID: "a"}, {VideoID: "b"}})
	if len(missing) != 0 {
		t.Errorf("missing after enrich = %v, want none", missing)
	}
	if filled[0].AlbumID != "MPREa" || filled[1].AlbumID != "MPREb" {
		t.Errorf("filled = %+v, want cached album ids", filled)
	}
	if atomic.LoadInt32(&fake.calls) != calls0 {
		t.Error("Fill after enrich hit the network; cache was not populated")
	}
}

// TestEnrichMissing_skipsErrorsPartial — a failing id is skipped, the rest cached.
func TestEnrichMissing_skipsErrorsPartial(t *testing.T) {
	fake := &fakeDetailer{details: map[string]Detail{
		"ok": {Album: "A", AlbumID: "MPREok", Duration: time.Minute},
	}}
	e := newEnricherForTest(t, fake)

	got, err := e.EnrichMissing(context.Background(), []string{"ok", "bad"})
	if err != nil {
		t.Fatalf("EnrichMissing: %v", err)
	}
	if len(got) != 1 || got[0].VideoID != "ok" {
		t.Errorf("got = %+v, want only the ok result", got)
	}
	if _, ok := e.get("bad"); ok {
		t.Error("the failing id must not be cached")
	}
}

// TestEnrichMissing_concurrencyBounded asserts no more than e.concurrency requests
// are ever in flight at once.
func TestEnrichMissing_concurrencyBounded(t *testing.T) {
	details := map[string]Detail{}
	var ids []string
	for i := 0; i < 40; i++ {
		id := "v" + itoa(i)
		ids = append(ids, id)
		details[id] = Detail{VideoID: id, AlbumID: "MPRE" + itoa(i)}
	}
	fake := &fakeDetailer{details: details, block: 5 * time.Millisecond}
	e := newEnricherForTest(t, fake)
	e.concurrency = 4

	if _, err := e.EnrichMissing(context.Background(), ids); err != nil {
		t.Fatalf("EnrichMissing: %v", err)
	}
	if got := atomic.LoadInt32(&fake.maxCur); got > 4 {
		t.Errorf("max concurrent = %d, want <= 4", got)
	}
	if got := atomic.LoadInt32(&fake.calls); got != 40 {
		t.Errorf("calls = %d, want 40", got)
	}
}

// TestEnrichMissing_nilDetailerNoOp — a cache-only Enricher never fetches.
func TestEnrichMissing_nilDetailerNoOp(t *testing.T) {
	e := NewEnricher(store.NewCache(t.TempDir()), nil)
	e.delay = 0
	got, err := e.EnrichMissing(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("EnrichMissing: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got = %+v, want none (nil detailer)", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
