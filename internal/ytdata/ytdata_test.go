package ytdata

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/fkallas/tubeamp/internal/ytm"
)

// newTestClient builds a Client pointed at srv with a valid (non-expiring) token
// whose refresh would fail the test if ever invoked.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	tok := &ytm.OAuthToken{AccessToken: "tok-abc", TokenType: "Bearer", ExpiresAt: time.Now().Unix() + 3600}
	c := NewClient(tok, ytm.OAuthCreds{ClientID: "cid", ClientSecret: "sec"}, "")
	c.baseURL = srv.URL
	c.refreshFn = func(context.Context, *ytm.OAuthToken, ytm.OAuthCreds) error {
		t.Error("refreshFn called for a valid token")
		return nil
	}
	return c
}

func TestAccount_parses(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if got := r.URL.Query().Get("mine"); got != "true" {
			t.Errorf("mine = %q, want true", got)
		}
		_, _ = io.WriteString(w, `{"items":[{"snippet":{"title":"Ada Lovelace"}}]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	name, err := c.Account(context.Background())
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if name != "Ada Lovelace" {
		t.Errorf("name = %q, want Ada Lovelace", name)
	}
	if gotAuth != "Bearer tok-abc" {
		t.Errorf("Authorization = %q, want Bearer tok-abc", gotAuth)
	}
}

func TestAccount_emptyItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	name, err := c.Account(context.Background())
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if name != "" {
		t.Errorf("name = %q, want empty for no channel", name)
	}
}

func TestAccount_httpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":401}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.Account(context.Background()); err == nil {
		t.Fatal("Account: want error on HTTP 401")
	}
}

// TestAccountInfo_signedInStates asserts AccountInfo's OAuth semantics: ANY
// successful channels response — with a channel or with empty items (a Google
// account that never created a YouTube channel) — is a live session
// (signedIn=true); only a request failure reports (.., false, err).
func TestAccountInfo_signedInStates(t *testing.T) {
	cases := []struct {
		label    string
		status   int
		body     string
		wantName string
		wantOK   bool
		wantErr  bool
	}{
		{"channel", http.StatusOK, `{"items":[{"snippet":{"title":"Ada Lovelace"}}]}`, "Ada Lovelace", true, false},
		{"no channel", http.StatusOK, `{"items":[]}`, "", true, false},
		{"http error", http.StatusUnauthorized, `{"error":{"code":401}}`, "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			c := newTestClient(t, srv)
			name, signedIn, err := c.AccountInfo(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("AccountInfo err = %v, wantErr %v", err, tc.wantErr)
			}
			if name != tc.wantName || signedIn != tc.wantOK {
				t.Errorf("AccountInfo = (%q, %v), want (%q, %v)", name, signedIn, tc.wantName, tc.wantOK)
			}
		})
	}
}

func TestLibraryPlaylists_paginates(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.URL.Query().Get("part"); got != "snippet,contentDetails" {
			t.Errorf("part = %q", got)
		}
		switch r.URL.Query().Get("pageToken") {
		case "":
			_, _ = io.WriteString(w, `{
				"nextPageToken":"PAGE2",
				"items":[
					{"id":"PL1","snippet":{"title":"Roadtrip"},"contentDetails":{"itemCount":12}},
					{"id":"PL2","snippet":{"title":"Focus"},"contentDetails":{"itemCount":40}}
				]}`)
		case "PAGE2":
			_, _ = io.WriteString(w, `{
				"items":[
					{"id":"PL3","snippet":{"title":"Sleep"},"contentDetails":{"itemCount":7}}
				]}`)
		default:
			t.Errorf("unexpected pageToken %q", r.URL.Query().Get("pageToken"))
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	pls, err := c.LibraryPlaylists(context.Background())
	if err != nil {
		t.Fatalf("LibraryPlaylists: %v", err)
	}
	if calls != 2 {
		t.Errorf("server calls = %d, want 2 (pagination did not follow nextPageToken)", calls)
	}
	if len(pls) != 3 {
		t.Fatalf("got %d playlists, want 3", len(pls))
	}
	if pls[0].ID != "PL1" || pls[0].Title != "Roadtrip" || pls[0].TrackCount != 12 {
		t.Errorf("pls[0] = %+v", pls[0])
	}
	if pls[2].ID != "PL3" || pls[2].TrackCount != 7 {
		t.Errorf("pls[2] = %+v", pls[2])
	}
}

func TestPlaylistTracks_mapsSkipsAndPaginates(t *testing.T) {
	var calls int
	var gotPlaylistIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotPlaylistIDs = append(gotPlaylistIDs, r.URL.Query().Get("playlistId"))
		switch r.URL.Query().Get("pageToken") {
		case "":
			// A "- Topic" channel track (clean artist+title), and a deleted item
			// with no videoId that must be skipped.
			_, _ = io.WriteString(w, `{
				"nextPageToken":"P2",
				"items":[
					{
						"snippet":{
							"title":"Clair de Lune",
							"channelTitle":"Some Uploader",
							"videoOwnerChannelTitle":"Claude Debussy - Topic",
							"thumbnails":{
								"default":{"url":"d.jpg","width":120,"height":90},
								"high":{"url":"h.jpg","width":480,"height":360}
							}
						},
						"contentDetails":{"videoId":"vid1"}
					},
					{
						"snippet":{"title":"Deleted video"},
						"contentDetails":{}
					}
				]}`)
		case "P2":
			// A regular (non-Topic) channel track: artist is the channel title.
			_, _ = io.WriteString(w, `{
				"items":[
					{
						"snippet":{
							"title":"Live at Wembley",
							"channelTitle":"Queen Official",
							"thumbnails":{"medium":{"url":"m.jpg","width":320,"height":180}}
						},
						"contentDetails":{"videoId":"vid2"}
					}
				]}`)
		default:
			t.Errorf("unexpected pageToken %q", r.URL.Query().Get("pageToken"))
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	tracks, err := c.PlaylistTracks(context.Background(), "PLxyz")
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	if calls != 2 {
		t.Errorf("server calls = %d, want 2", calls)
	}
	for _, id := range gotPlaylistIDs {
		if id != "PLxyz" {
			t.Errorf("playlistId = %q, want PLxyz", id)
		}
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2 (deleted item should be skipped)", len(tracks))
	}

	// Topic-channel track: artist recovered from "<Artist> - Topic", thumb is the
	// largest (high).
	if tracks[0].VideoID != "vid1" || tracks[0].Title != "Clair de Lune" {
		t.Errorf("tracks[0] = %+v", tracks[0])
	}
	if len(tracks[0].Artists) != 1 || tracks[0].Artists[0] != "Claude Debussy" {
		t.Errorf("tracks[0].Artists = %v, want [Claude Debussy]", tracks[0].Artists)
	}
	if tracks[0].ThumbURL != "h.jpg" {
		t.Errorf("tracks[0].ThumbURL = %q, want h.jpg (largest)", tracks[0].ThumbURL)
	}
	if tracks[0].Album != "" || tracks[0].AlbumID != "" || tracks[0].Duration != 0 {
		t.Errorf("tracks[0] album/duration not empty: %+v", tracks[0])
	}

	// Regular channel track: artist is the channel title.
	if tracks[1].VideoID != "vid2" {
		t.Errorf("tracks[1].VideoID = %q, want vid2", tracks[1].VideoID)
	}
	if len(tracks[1].Artists) != 1 || tracks[1].Artists[0] != "Queen Official" {
		t.Errorf("tracks[1].Artists = %v, want [Queen Official]", tracks[1].Artists)
	}
	if tracks[1].ThumbURL != "m.jpg" {
		t.Errorf("tracks[1].ThumbURL = %q, want m.jpg", tracks[1].ThumbURL)
	}
}

func TestLikedSongs_usesLL(t *testing.T) {
	var gotPlaylistID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPlaylistID = r.URL.Query().Get("playlistId")
		_, _ = io.WriteString(w, `{"items":[{"snippet":{"title":"Liked One","channelTitle":"Artist - Topic","videoOwnerChannelTitle":"Artist - Topic"},"contentDetails":{"videoId":"lk1"}}]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	tracks, err := c.LikedSongs(context.Background())
	if err != nil {
		t.Fatalf("LikedSongs: %v", err)
	}
	if gotPlaylistID != "LL" {
		t.Errorf("playlistId = %q, want LL", gotPlaylistID)
	}
	if len(tracks) != 1 || tracks[0].VideoID != "lk1" {
		t.Fatalf("tracks = %+v", tracks)
	}
	if len(tracks[0].Artists) != 1 || tracks[0].Artists[0] != "Artist" {
		t.Errorf("tracks[0].Artists = %v, want [Artist]", tracks[0].Artists)
	}
}

// TestClient_refreshesExpiredTokenBeforeRequest exercises the refresh seam: an
// expired token triggers a refresh (here pointed at a fake token endpoint via the
// injected seam) BEFORE the Data API call, and the refreshed token is persisted.
func TestClient_refreshesExpiredTokenBeforeRequest(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", r.PostForm.Get("grant_type"))
		}
		if r.PostForm.Get("refresh_token") != "ref-1" {
			t.Errorf("refresh_token = %q, want ref-1", r.PostForm.Get("refresh_token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"fresh-access","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer tokenSrv.Close()

	var gotAuth string
	dataSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"items":[{"snippet":{"title":"Me"}}]}`)
	}))
	defer dataSrv.Close()

	tok := &ytm.OAuthToken{AccessToken: "stale", RefreshToken: "ref-1", TokenType: "Bearer", ExpiresAt: time.Now().Unix() - 100}
	path := filepath.Join(t.TempDir(), "oauth.json")
	c := NewClient(tok, ytm.OAuthCreds{ClientID: "cid", ClientSecret: "sec"}, path)
	c.baseURL = dataSrv.URL

	var refreshed bool
	c.refreshFn = func(ctx context.Context, tk *ytm.OAuthToken, creds ytm.OAuthCreds) error {
		refreshed = true
		form := url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {tk.RefreshToken},
			"client_id":     {creds.ClientID},
			"client_secret": {creds.ClientSecret},
		}
		resp, err := http.PostForm(tokenSrv.URL, form)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		var tr struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int    `json:"expires_in"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
			return err
		}
		tk.AccessToken = tr.AccessToken
		tk.ExpiresAt = time.Now().Unix() + int64(tr.ExpiresIn)
		return nil
	}

	name, err := c.Account(context.Background())
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if !refreshed {
		t.Error("expired token did not trigger a refresh")
	}
	if name != "Me" {
		t.Errorf("name = %q, want Me", name)
	}
	if gotAuth != "Bearer fresh-access" {
		t.Errorf("Authorization = %q, want Bearer fresh-access (refreshed before request)", gotAuth)
	}
	// The refreshed token was persisted to tokenPath.
	saved, err := ytm.LoadOAuthToken(path)
	if err != nil {
		t.Fatalf("LoadOAuthToken: %v", err)
	}
	if saved.AccessToken != "fresh-access" {
		t.Errorf("persisted AccessToken = %q, want fresh-access", saved.AccessToken)
	}
}

func TestThumbnails_best(t *testing.T) {
	// Largest by area wins.
	th := thumbnails{
		"default": {URL: "d", Width: 120, Height: 90},
		"high":    {URL: "h", Width: 480, Height: 360},
		"medium":  {URL: "m", Width: 320, Height: 180},
	}
	if got := th.best(); got != "h" {
		t.Errorf("best = %q, want h", got)
	}
	// No dimensions: preference order favours high.
	th2 := thumbnails{
		"default": {URL: "d"},
		"high":    {URL: "h"},
	}
	if got := th2.best(); got != "h" {
		t.Errorf("best (no dims) = %q, want h", got)
	}
	// Empty.
	if got := (thumbnails{}).best(); got != "" {
		t.Errorf("best (empty) = %q, want empty", got)
	}
}
