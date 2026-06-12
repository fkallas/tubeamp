package ytdata

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/fkallas/tubeamp/internal/model"
)

// TestPlaylistItems_fullPagination asserts the loop follows nextPageToken to
// exhaustion (not just the old ~2-page cap) — three pages of 50 here.
func TestPlaylistItems_fullPagination(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Query().Get("pageToken") {
		case "":
			io.WriteString(w, page(50, "vidA", "P2"))
		case "P2":
			io.WriteString(w, page(50, "vidB", "P3"))
		case "P3":
			io.WriteString(w, page(7, "vidC", ""))
		default:
			t.Errorf("unexpected pageToken %q", r.URL.Query().Get("pageToken"))
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	tracks, err := c.PlaylistTracks(context.Background(), "PLfull")
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	if calls != 3 {
		t.Errorf("server calls = %d, want 3 (pagination stopped early)", calls)
	}
	if len(tracks) != 107 {
		t.Fatalf("got %d tracks, want 107 (50+50+7)", len(tracks))
	}
}

// page renders a playlistItems response with n items (videoIds prefix+index) and
// the given nextPageToken ("" to omit).
func page(n int, prefix, next string) string {
	s := "{"
	if next != "" {
		s += `"nextPageToken":"` + next + `",`
	}
	s += `"items":[`
	for i := 0; i < n; i++ {
		if i > 0 {
			s += ","
		}
		s += `{"snippet":{"title":"T","channelTitle":"C"},"contentDetails":{"videoId":"` + prefix + itoa(i) + `"}}`
	}
	s += `]}`
	return s
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

// TestLibrarySongs_aggregatesAndDedups exercises the whole-library aggregate:
// liked first, then each playlist, deduped by VideoID (first wins), with a failing
// playlist skipped (partial result, not fatal).
func TestLibrarySongs_aggregatesAndDedups(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/playlists":
			io.WriteString(w, `{"items":[
				{"id":"PLgood","snippet":{"title":"Good"},"contentDetails":{"itemCount":2}},
				{"id":"PLbad","snippet":{"title":"Bad"},"contentDetails":{"itemCount":1}},
				{"id":"PLdup","snippet":{"title":"Dup"},"contentDetails":{"itemCount":1}}
			]}`)
		case "/playlistItems":
			switch q.Get("playlistId") {
			case "LL":
				io.WriteString(w, oneItem("liked1")+"")
			case "PLgood":
				io.WriteString(w, twoItems("g1", "g2"))
			case "PLbad":
				w.WriteHeader(http.StatusInternalServerError)
				io.WriteString(w, `{"error":{"code":500}}`)
			case "PLdup":
				// Re-emits liked1 (must be deduped) plus a fresh d1.
				io.WriteString(w, twoItems("liked1", "d1"))
			default:
				t.Errorf("unexpected playlistId %q", q.Get("playlistId"))
			}
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	tracks, err := c.LibrarySongs(context.Background())
	if err != nil {
		t.Fatalf("LibrarySongs: %v", err)
	}

	var ids []string
	for _, tr := range tracks {
		ids = append(ids, tr.VideoID)
	}
	// liked1 first; PLgood's g1,g2; PLbad skipped; PLdup contributes only d1
	// (liked1 already seen).
	want := []string{"liked1", "g1", "g2", "d1"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

// TestLibrarySongs_likedFailureFatal — if the spine call (LikedSongs) fails, the
// whole aggregate fails (not a partial).
func TestLibrarySongs_likedFailureFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"code":401}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.LibrarySongs(context.Background()); err == nil {
		t.Fatal("LibrarySongs: want error when LikedSongs fails")
	}
}

func oneItem(id string) string {
	return `{"items":[{"snippet":{"title":"T","channelTitle":"C"},"contentDetails":{"videoId":"` + id + `"}}]}`
}

func twoItems(a, b string) string {
	return `{"items":[` +
		`{"snippet":{"title":"T","channelTitle":"C"},"contentDetails":{"videoId":"` + a + `"}},` +
		`{"snippet":{"title":"T","channelTitle":"C"},"contentDetails":{"videoId":"` + b + `"}}` +
		`]}`
}

func TestGroupArtists(t *testing.T) {
	tracks := []model.Track{
		{VideoID: "1", Title: "A", Artists: []string{"Queen"}},
		{VideoID: "2", Title: "B", Artists: []string{"ABBA"}},
		{VideoID: "3", Title: "C", Artists: []string{"queen"}}, // case-insensitive merge
		{VideoID: "4", Title: "D", Artists: nil},               // no artist -> skipped
		{VideoID: "5", Title: "E", Artists: []string{"  "}},    // blank -> skipped
		{VideoID: "6", Title: "F", Artists: []string{"ABBA", "Queen"}},
	}
	groups := GroupArtists(tracks)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	// Sorted by name (case-insensitive): ABBA, then Queen.
	if groups[0].Name != "ABBA" || groups[1].Name != "Queen" {
		t.Fatalf("group names = [%q %q], want [ABBA Queen]", groups[0].Name, groups[1].Name)
	}
	// ABBA: tracks 2 and 6, in input order.
	if got := ids(groups[0].Tracks); !reflect.DeepEqual(got, []string{"2", "6"}) {
		t.Errorf("ABBA tracks = %v, want [2 6]", got)
	}
	// Queen: first-seen casing "Queen", tracks 1 and 3.
	if got := ids(groups[1].Tracks); !reflect.DeepEqual(got, []string{"1", "3"}) {
		t.Errorf("Queen tracks = %v, want [1 3]", got)
	}
}

func TestGroupArtists_empty(t *testing.T) {
	if g := GroupArtists(nil); len(g) != 0 {
		t.Errorf("GroupArtists(nil) = %v, want empty", g)
	}
}

func TestAlbumsFromTracks(t *testing.T) {
	tracks := []model.Track{
		{VideoID: "1", Album: "Zoo", AlbumID: "MPREz", Artists: []string{"Z"}, ThumbURL: "z.jpg"},
		{VideoID: "2", Album: "Alpha", AlbumID: "MPREa", Artists: []string{"A"}, ThumbURL: "a.jpg"},
		{VideoID: "3", Album: "Zoo", AlbumID: "MPREz", Artists: []string{"Z"}}, // dup album
		{VideoID: "4", Album: "", AlbumID: ""},                                 // no album -> skipped
	}
	albums := AlbumsFromTracks(tracks)
	if len(albums) != 2 {
		t.Fatalf("got %d albums, want 2", len(albums))
	}
	// Sorted by title: Alpha, then Zoo.
	if albums[0].Title != "Alpha" || albums[0].BrowseID != "MPREa" {
		t.Errorf("albums[0] = %+v, want Alpha/MPREa", albums[0])
	}
	if albums[1].Title != "Zoo" || albums[1].BrowseID != "MPREz" || albums[1].ThumbURL != "z.jpg" {
		t.Errorf("albums[1] = %+v, want Zoo/MPREz/z.jpg", albums[1])
	}
}

func TestAlbumsFromTracks_fillsAcrossRows(t *testing.T) {
	// The first row of an album may lack the thumb; a later row fills it.
	tracks := []model.Track{
		{VideoID: "1", Album: "One", AlbumID: "MPRE1"},
		{VideoID: "2", Album: "One", AlbumID: "MPRE1", Artists: []string{"Solo"}, ThumbURL: "t.jpg"},
	}
	albums := AlbumsFromTracks(tracks)
	if len(albums) != 1 {
		t.Fatalf("got %d albums, want 1", len(albums))
	}
	if albums[0].ThumbURL != "t.jpg" || len(albums[0].Artists) != 1 {
		t.Errorf("album = %+v, want thumb t.jpg + artist filled from row 2", albums[0])
	}
}

func ids(tracks []model.Track) []string {
	out := make([]string, len(tracks))
	for i, t := range tracks {
		out[i] = t.VideoID
	}
	return out
}
