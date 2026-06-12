// Package ytdata is a YouTube Data API v3 client for the signed-in user's
// library, driven by the OAuth token tubeamp obtains via the device flow.
//
// Google's private InnerTube API (music.youtube.com/youtubei) rejects OAuth
// bearer tokens, but the official YouTube Data API v3
// (https://www.googleapis.com/youtube/v3) accepts the same token and returns the
// user's real playlists and liked videos. This client is therefore the
// OAuth-backed source of library data; InnerTube auth stays cookie/anonymous.
//
// The client holds the OAuth token, the client credentials, and the token file
// path. Before each request it refreshes the access token when it has expired
// (serialized by a mutex) and persists the refreshed token to disk when a path is
// configured. Requests are plain HTTPS GETs with an Authorization: Bearer header;
// no extra dependency beyond net/http and encoding/json.
package ytdata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/ytm"
)

const (
	// dataAPIBase is the YouTube Data API v3 root.
	dataAPIBase = "https://www.googleapis.com/youtube/v3"

	// pageSize is the per-request maxResults (the Data API caps it at 50).
	pageSize = 50

	// playlistsItemCap bounds LibraryPlaylists pagination (~4 pages).
	playlistsItemCap = 200

	// tracksItemCap and tracksPageCap bound the track listings to the first
	// ~2 pages (~100 items). Full pagination is left as a TODO on playlistItems.
	tracksItemCap = 100
	tracksPageCap = 2

	// respLimit caps how much of a response body is read, to avoid runaway
	// allocations on an unexpected payload.
	respLimit = 4 << 20

	// topicSuffix marks the auto-generated "<Artist> - Topic" channels YouTube
	// Music tracks live on; stripping it recovers the artist name.
	topicSuffix = " - Topic"
)

// Client is a YouTube Data API v3 client backed by an OAuth token. It is safe
// for concurrent use: the token refresh-and-persist is serialized by a mutex.
type Client struct {
	hc        *http.Client
	tok       *ytm.OAuthToken
	creds     ytm.OAuthCreds
	tokenPath string // where refreshed tokens are persisted; "" => memory only
	baseURL   string // Data API root; overridable in tests

	mu sync.Mutex // serializes the refresh+persist of tok across requests
	// refreshFn refreshes tok in place. It defaults to tok.Refresh and is a
	// field so tests can inject a refresh seam without hitting Google.
	refreshFn func(ctx context.Context, tok *ytm.OAuthToken, creds ytm.OAuthCreds) error
}

// NewClient creates a Data API client driven by the OAuth token tok and the
// user-supplied client creds. Refreshed tokens are persisted to tokenPath
// (pass "" to keep the refreshed token in memory only). tok must be non-nil.
func NewClient(tok *ytm.OAuthToken, creds ytm.OAuthCreds, tokenPath string) *Client {
	return &Client{
		hc:        &http.Client{Timeout: 10 * time.Second},
		tok:       tok,
		creds:     creds,
		tokenPath: tokenPath,
		baseURL:   dataAPIBase,
		refreshFn: func(ctx context.Context, tok *ytm.OAuthToken, creds ytm.OAuthCreds) error {
			return tok.Refresh(ctx, creds)
		},
	}
}

// Account returns the signed-in channel's display name via
// channels?part=snippet&mine=true (items[0].snippet.title). It confirms the
// sign-in and supplies the account name. Empty items (no channel on the account)
// yields ("", nil), a valid result; HTTP/transport failures yield an error.
func (c *Client) Account(ctx context.Context) (string, error) {
	q := url.Values{"part": {"snippet"}, "mine": {"true"}}
	raw, err := c.get(ctx, "channels", q)
	if err != nil {
		return "", fmt.Errorf("ytdata.Account: %w", err)
	}
	var resp channelListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("ytdata.Account: parse response: %w", err)
	}
	if len(resp.Items) == 0 {
		return "", nil
	}
	return resp.Items[0].Snippet.Title, nil
}

// AccountInfo reports the OAuth session state for the UI's library-provider
// seam, mirroring ytm.Client.AccountInfo's shape. Unlike a cookie session — where
// an empty account menu means "anonymous" — ANY successful channels?mine=true
// response proves the OAuth token is live, so signedIn is true even when the
// Google account has no YouTube channel (empty items ⇒ name ""). Only an
// HTTP/transport/refresh failure returns an error (session state unknown).
func (c *Client) AccountInfo(ctx context.Context) (name string, signedIn bool, err error) {
	name, err = c.Account(ctx)
	if err != nil {
		return "", false, err
	}
	return name, true, nil
}

// LibraryPlaylists returns the playlists the user OWNS via
// playlists?part=snippet,contentDetails&mine=true, following nextPageToken until
// exhausted or the ~200 item cap is reached. NOTE: unlike the cookie path's
// FEmusic_liked_playlists browse, mine=true does not include saved/followed
// playlists from other channels — the Data API exposes no equivalent.
func (c *Client) LibraryPlaylists(ctx context.Context) ([]model.Playlist, error) {
	var out []model.Playlist
	pageToken := ""
	for {
		q := url.Values{
			"part":       {"snippet,contentDetails"},
			"mine":       {"true"},
			"maxResults": {strconv.Itoa(pageSize)},
		}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		raw, err := c.get(ctx, "playlists", q)
		if err != nil {
			return nil, fmt.Errorf("ytdata.LibraryPlaylists: %w", err)
		}
		var resp playlistListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("ytdata.LibraryPlaylists: parse response: %w", err)
		}
		for _, it := range resp.Items {
			out = append(out, model.Playlist{
				ID:         it.ID,
				Title:      it.Snippet.Title,
				TrackCount: it.ContentDetails.ItemCount,
			})
		}
		pageToken = resp.NextPageToken
		if pageToken == "" || len(out) >= playlistsItemCap {
			break
		}
	}
	return out, nil
}

// PlaylistTracks returns the tracks of the playlist with the given id via
// playlistItems?part=snippet,contentDetails. Only the first ~2 pages (~100
// tracks) are fetched; items with no videoId (deleted/private) are skipped.
func (c *Client) PlaylistTracks(ctx context.Context, playlistID string) ([]model.Track, error) {
	tracks, err := c.playlistItems(ctx, playlistID)
	if err != nil {
		return nil, fmt.Errorf("ytdata.PlaylistTracks: %w", err)
	}
	return tracks, nil
}

// LikedSongs returns the user's liked videos via the "LL" auto-playlist, the
// same shape as PlaylistTracks (first ~2 pages / ~100 tracks). NOTE: "LL" is
// YouTube's all-liked-VIDEOS list (music or not) — broader than YT Music's
// Liked Music (the cookie path's "VLLM" browse), which the Data API does not
// expose.
func (c *Client) LikedSongs(ctx context.Context) ([]model.Track, error) {
	tracks, err := c.playlistItems(ctx, "LL")
	if err != nil {
		return nil, fmt.Errorf("ytdata.LikedSongs: %w", err)
	}
	return tracks, nil
}

// playlistItems fetches and maps the items of a playlist, following
// nextPageToken up to the (small) track caps.
func (c *Client) playlistItems(ctx context.Context, playlistID string) ([]model.Track, error) {
	var out []model.Track
	pageToken := ""
	// TODO: full pagination — follow nextPageToken to exhaustion for complete
	// playlists. The UI currently only needs the first ~2 pages (~100 tracks).
	for page := 0; page < tracksPageCap; page++ {
		q := url.Values{
			"part":       {"snippet,contentDetails"},
			"playlistId": {playlistID},
			"maxResults": {strconv.Itoa(pageSize)},
		}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		raw, err := c.get(ctx, "playlistItems", q)
		if err != nil {
			return nil, err
		}
		var resp playlistItemListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
		for _, it := range resp.Items {
			t := mapVideo(it.Snippet, it.ContentDetails)
			if t.VideoID == "" {
				continue // deleted/private item
			}
			out = append(out, t)
		}
		pageToken = resp.NextPageToken
		if pageToken == "" || len(out) >= tracksItemCap {
			break
		}
	}
	return out, nil
}

// mapVideo maps a playlistItems snippet+contentDetails into a model.Track.
// YouTube Music tracks are published on "<Artist> - Topic" channels, so when the
// owner channel title carries that suffix the artist is recovered from it and the
// (already clean) snippet title is used; otherwise the channel title is the
// artist. Album/AlbumID are left empty (the Data API does not expose album) and
// Duration is 0 (not present on playlistItems).
func mapVideo(sn videoSnippet, cd playlistItemContentDetails) model.Track {
	owner := sn.VideoOwnerChannelTitle
	if owner == "" {
		owner = sn.ChannelTitle
	}
	var artists []string
	if name := strings.TrimSuffix(owner, topicSuffix); name != owner {
		if name != "" {
			artists = []string{name}
		}
	} else if sn.ChannelTitle != "" {
		artists = []string{sn.ChannelTitle}
	}
	return model.Track{
		VideoID: cd.VideoID,
		Title:   sn.Title,
		Artists: artists,
		// Album and AlbumID stay "" — the Data API does not expose album metadata.
		// Duration stays 0.
		// NOTE: a videos?part=contentDetails batch could fill durations later.
		ThumbURL: sn.Thumbnails.best(),
	}
}

// get performs an authorized GET to <baseURL>/<endpoint>?<q> and returns the raw
// body. It refreshes the OAuth token first when it has expired. Non-2xx responses
// yield an error carrying the status code and a truncated body.
func (c *Client) get(ctx context.Context, endpoint string, q url.Values) ([]byte, error) {
	authz, err := c.authorization(ctx)
	if err != nil {
		return nil, err
	}
	u := c.baseURL + "/" + endpoint
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", authz)
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, respLimit))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := body
		if len(snippet) > 256 {
			snippet = snippet[:256]
		}
		return nil, fmt.Errorf("data API HTTP %d: %s", resp.StatusCode, snippet)
	}
	return body, nil
}

// authorization returns the Authorization header value, refreshing (and
// persisting, best-effort) the OAuth token first when it has expired. The lock
// serializes concurrent requests so only one refresh runs at a time.
func (c *Client) authorization(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tok == nil {
		return "", fmt.Errorf("ytdata: no OAuth token")
	}
	if c.tok.IsExpired(time.Now()) {
		if err := c.refreshFn(ctx, c.tok, c.creds); err != nil {
			return "", fmt.Errorf("ytdata: refresh oauth token: %w", err)
		}
		if c.tokenPath != "" {
			// Best-effort: the refreshed token is already live in memory, so a
			// persistence failure must not break the request.
			_ = c.tok.Save(c.tokenPath)
		}
	}
	return c.tok.Authorization(), nil
}

// --- Data API response shapes (only the fields tubeamp consumes) ---

type channelListResponse struct {
	Items []struct {
		Snippet struct {
			Title string `json:"title"`
		} `json:"snippet"`
	} `json:"items"`
}

type playlistListResponse struct {
	NextPageToken string `json:"nextPageToken"`
	Items         []struct {
		ID      string `json:"id"`
		Snippet struct {
			Title string `json:"title"`
		} `json:"snippet"`
		ContentDetails struct {
			ItemCount int `json:"itemCount"`
		} `json:"contentDetails"`
	} `json:"items"`
}

type playlistItemListResponse struct {
	NextPageToken string `json:"nextPageToken"`
	Items         []struct {
		Snippet        videoSnippet               `json:"snippet"`
		ContentDetails playlistItemContentDetails `json:"contentDetails"`
	} `json:"items"`
}

type videoSnippet struct {
	Title                  string     `json:"title"`
	ChannelTitle           string     `json:"channelTitle"`
	VideoOwnerChannelTitle string     `json:"videoOwnerChannelTitle"`
	Thumbnails             thumbnails `json:"thumbnails"`
}

type playlistItemContentDetails struct {
	VideoID string `json:"videoId"`
}

// thumbnail is one entry of a snippet.thumbnails map.
type thumbnail struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// thumbnails is the snippet.thumbnails object keyed by size name
// (default/medium/high/standard/maxres).
type thumbnails map[string]thumbnail

// best returns the URL of the largest thumbnail by pixel area. When no entry
// carries dimensions it falls back to a fixed preference order favouring
// high/medium. Returns "" when there are no thumbnails.
func (t thumbnails) best() string {
	var bestURL string
	bestArea := 0
	for _, th := range t {
		if th.URL == "" {
			continue
		}
		if area := th.Width * th.Height; area > bestArea {
			bestArea = area
			bestURL = th.URL
		}
	}
	if bestArea > 0 {
		return bestURL
	}
	for _, key := range []string{"high", "medium", "default", "standard", "maxres"} {
		if th, ok := t[key]; ok && th.URL != "" {
			return th.URL
		}
	}
	return bestURL
}
