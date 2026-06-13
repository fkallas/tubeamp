package history

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fkallas/tubeamp/internal/model"
)

func storePath(t *testing.T) string {
	t.Helper()
	// A nested dir that does not exist yet, to exercise lazy parent creation.
	return filepath.Join(t.TempDir(), "data", "history.json")
}

func TestRecordAndList(t *testing.T) {
	s := New(storePath(t))
	if got := s.List(); len(got) != 0 {
		t.Fatalf("empty store List = %v, want []", got)
	}
	if err := s.Record(model.Track{VideoID: "a", Title: "A"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Record(model.Track{VideoID: "b", Title: "B"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got := s.List()
	if len(got) != 2 || got[0].VideoID != "b" || got[1].VideoID != "a" {
		t.Fatalf("List = %+v, want [b a] (most-recent first)", got)
	}
}

func TestRecord_dedupMovesToFront(t *testing.T) {
	s := New(storePath(t))
	for _, id := range []string{"a", "b", "c"} {
		if err := s.Record(model.Track{VideoID: id}); err != nil {
			t.Fatalf("Record(%s): %v", id, err)
		}
	}
	// Replaying "a" moves it to the front; no duplicate.
	if err := s.Record(model.Track{VideoID: "a", Title: "A again"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got := s.List()
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (dedup)", len(got))
	}
	wantOrder := []string{"a", "c", "b"}
	for i, id := range wantOrder {
		if got[i].VideoID != id {
			t.Errorf("got[%d] = %q, want %q (order %v)", i, got[i].VideoID, id, wantOrder)
		}
	}
	if got[0].Title != "A again" {
		t.Errorf("front entry Title = %q, want the latest record's title", got[0].Title)
	}
}

// TestRecordAt_outOfOrderArrivalsKeepPlayOrder — two records arriving out of
// order (concurrent Cmd goroutines under fast track-skipping) still land in
// play order, most-recent first, because ordering is by the played-at time
// captured at the transition, not by write-arrival order.
func TestRecordAt_outOfOrderArrivalsKeepPlayOrder(t *testing.T) {
	s := New(storePath(t))
	base := time.Now()
	// "b" was played AFTER "a", but its record lands first.
	if err := s.RecordAt(model.Track{VideoID: "b", Title: "B"}, base.Add(2*time.Second)); err != nil {
		t.Fatalf("RecordAt(b): %v", err)
	}
	if err := s.RecordAt(model.Track{VideoID: "a", Title: "A"}, base.Add(time.Second)); err != nil {
		t.Fatalf("RecordAt(a): %v", err)
	}
	got := s.List()
	if len(got) != 2 || got[0].VideoID != "b" || got[1].VideoID != "a" {
		t.Fatalf("List = %+v, want [b a] (play order, not arrival order)", got)
	}
}

// TestLegacyFileWithoutPlayedAt — a history file written before the played_at
// field reads fine: its entries keep their order, sorting after any
// timestamped entries (a new record lands in front).
func TestLegacyFileWithoutPlayedAt(t *testing.T) {
	path := storePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `[{"VideoID":"old1","Title":"Old One"},{"VideoID":"old2","Title":"Old Two"}]`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(path)
	if got := s.List(); len(got) != 2 || got[0].VideoID != "old1" || got[1].VideoID != "old2" {
		t.Fatalf("legacy List = %+v, want [old1 old2]", got)
	}
	if err := s.Record(model.Track{VideoID: "new1", Title: "New"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got := s.List()
	if len(got) != 3 || got[0].VideoID != "new1" || got[1].VideoID != "old1" || got[2].VideoID != "old2" {
		t.Fatalf("List = %+v, want [new1 old1 old2]", got)
	}
}

func TestRecord_caps(t *testing.T) {
	s := New(storePath(t))
	// Record more than the cap; oldest should fall off the end.
	for i := 0; i < maxEntries+50; i++ {
		if err := s.Record(model.Track{VideoID: id(i)}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	got := s.List()
	if len(got) != maxEntries {
		t.Fatalf("len = %d, want cap %d", len(got), maxEntries)
	}
	// The most-recent record is at the front.
	if got[0].VideoID != id(maxEntries+49) {
		t.Errorf("front = %q, want the last recorded", got[0].VideoID)
	}
}

func TestRecord_ignoresEmptyVideoID(t *testing.T) {
	s := New(storePath(t))
	if err := s.Record(model.Track{Title: "no id"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("List = %+v, want [] (empty-id record ignored)", got)
	}
}

func TestRoundtrip_acrossStores(t *testing.T) {
	path := storePath(t)
	s1 := New(path)
	if err := s1.Record(model.Track{VideoID: "x", Title: "X", Artists: []string{"Artist"}}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// A fresh Store over the same path reads the persisted history.
	s2 := New(path)
	got := s2.List()
	if len(got) != 1 || got[0].VideoID != "x" || got[0].Title != "X" || len(got[0].Artists) != 1 {
		t.Fatalf("roundtrip List = %+v, want the persisted track", got)
	}
}

func TestCorruptFileToleratedAndOverwritten(t *testing.T) {
	path := storePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{ this is not json"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(path)
	if got := s.List(); len(got) != 0 {
		t.Errorf("corrupt List = %+v, want [] (tolerated)", got)
	}
	// Record starts clean over the corrupt file.
	if err := s.Record(model.Track{VideoID: "fresh"}); err != nil {
		t.Fatalf("Record over corrupt: %v", err)
	}
	if got := s.List(); len(got) != 1 || got[0].VideoID != "fresh" {
		t.Errorf("List after record = %+v, want [fresh]", got)
	}
}

func TestMissingFileIsEmpty(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if got := s.List(); len(got) != 0 {
		t.Errorf("List = %+v, want [] for missing file", got)
	}
}

func TestWritePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	path := storePath(t)
	s := New(path)
	if err := s.Record(model.Track{VideoID: "a"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("perms = %o, want 0600", fi.Mode().Perm())
	}
	// No leftover temp file.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file left behind after atomic write")
	}
}

func id(i int) string {
	if i == 0 {
		return "v0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return "v" + s
}
