package ytm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/ytm/parse"
)

const (
	ytmBase   = "https://music.youtube.com/youtubei/v1"
	ytmOrigin = "https://music.youtube.com"
	ytmUA     = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	// songsFilterParams restricts search results to songs only. Verified
	// against ytmusicapi get_search_params (scope=None, filter="songs",
	// ignore_spelling=False): "EgWKAQ" + "II" + "AWoMEA4QChADEAQQCRAF".
	songsFilterParams = "EgWKAQIIAWoMEA4QChADEAQQCRAF"

	// albumsFilterParams restricts search results to albums only. Same
	// envelope with the type segment "IY" (albums) instead of "II" (songs),
	// per ytmusicapi _get_param2.
	albumsFilterParams = "EgWKAQIYAWoMEA4QChADEAQQCRAF"
)

// Client is an authenticated or unauthenticated InnerTube client for YouTube
// Music. Authenticated requests sign with the Cookie header + a SAPISIDHASH
// Authorization (see Auth); without an Auth the client is anonymous (search
// still works).
//
// InnerTube does NOT accept OAuth bearer tokens: Google's youtubei endpoints
// reject them with HTTP 400 "invalid argument" (confirmed against both tubeamp
// and ytmusicapi), so there is deliberately no OAuth/Bearer path here. OAuth
// powers the official YouTube Data API library client (internal/ytdata) instead;
// the OAuth token store + device flow still live in this package (see oauth.go)
// for that client to use.
type Client struct {
	hc       *http.Client
	auth     *Auth
	authUser int    // X-Goog-AuthUser account index (config auth_user)
	baseURL  string // InnerTube base; overridable in tests
}

// NewClient creates a new Client. a may be nil for unauthenticated access;
// search still works without authentication. The account index defaults to 0;
// override it with SetAuthUser for people logged into multiple Google accounts.
func NewClient(a *Auth) *Client {
	return &Client{
		hc:      &http.Client{Timeout: 10 * time.Second},
		auth:    a,
		baseURL: ytmBase,
	}
}

// SetAuthUser sets the X-Goog-AuthUser account index sent on authenticated
// requests. It selects which of several signed-in Google accounts to act as
// (0 = the first/default account).
func (c *Client) SetAuthUser(n int) {
	if n < 0 {
		n = 0
	}
	c.authUser = n
}

// Authenticated reports whether the client carries cookie credentials (an Auth
// was loaded). It is NOT a live sign-in check (a stale cookie may still resolve
// anonymous on YouTube's side); use AccountInfo to confirm the live sign-in
// state.
func (c *Client) Authenticated() bool {
	return c.auth != nil
}

// post sends a JSON POST to https://music.youtube.com/youtubei/v1/<endpoint>
// and returns the raw response body. Non-2xx responses yield an error that
// includes the status code and a truncated body.
func (c *Client) post(ctx context.Context, endpoint string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("ytm: marshal payload: %w", err)
	}

	url := c.baseURL + "/" + endpoint + "?prettyPrint=false"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ytm: build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ytmUA)
	req.Header.Set("Origin", ytmOrigin)
	req.Header.Set("X-Origin", ytmOrigin)

	if c.auth != nil {
		// Cookie mode. One consistent snapshot: reading the SAPISID and the
		// Cookie header under separate locks would let a concurrent rotation slip
		// between the two and sign the request with a mismatched SAPISID.
		header, sapisid, err := c.auth.headerAndSAPISID()
		if err != nil {
			return nil, fmt.Errorf("ytm: get SAPISID: %w", err)
		}
		req.Header.Set("Cookie", header)
		req.Header.Set("Authorization", sapisidHash(sapisid, ytmOrigin, time.Now()))
		req.Header.Set("X-Goog-AuthUser", strconv.Itoa(c.authUser))
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ytm: HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Absorb any rotated cookies Google attached (SIDCC, __Secure-1PSIDCC, …) so
	// the session stays alive; do this even on non-2xx, since rotation can ride
	// along an error response. Only when we have credentials to refresh.
	if c.auth != nil {
		c.auth.merge(resp.Cookies())
	}

	// Limit reading to 4 MiB to avoid runaway allocations.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("ytm: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := respBody
		if len(snippet) > 256 {
			snippet = snippet[:256]
		}
		return nil, fmt.Errorf("ytm: HTTP %d: %s", resp.StatusCode, snippet)
	}

	return respBody, nil
}

// Search queries YouTube Music for songs matching query and returns the parsed
// track list. It uses the songs-only InnerTube filter params.
func (c *Client) Search(ctx context.Context, query string) ([]model.Track, error) {
	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    "WEB_REMIX",
				"clientVersion": "1.20240101.01.00",
				"hl":            "en",
			},
		},
		"query":  query,
		"params": songsFilterParams,
	}

	raw, err := c.post(ctx, "search", payload)
	if err != nil {
		return nil, fmt.Errorf("ytm.Search: %w", err)
	}

	tracks, err := parse.SearchTracks(raw)
	if err != nil {
		return nil, fmt.Errorf("ytm.Search: %w", err)
	}
	return tracks, nil
}

// SearchWithAlbums runs a single song search and returns both the song tracks
// and the album references derived from those song rows. It exists to bypass the
// degraded album-search vertical YouTube serves to non-browser sessions (only
// self-distributed releases; majors withheld): song search is full-catalog and
// every song result carries its album's MPRE… browseId, so the albums can be
// reconstructed from the songs. The derived albums have BrowseID, Title, Artists
// and ThumbURL set but no Year (GetAlbum fills that on open). It costs one
// request — the same one Search makes.
func (c *Client) SearchWithAlbums(ctx context.Context, query string) ([]model.Track, []model.Album, error) {
	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    "WEB_REMIX",
				"clientVersion": "1.20240101.01.00",
				"hl":            "en",
			},
		},
		"query":  query,
		"params": songsFilterParams,
	}

	raw, err := c.post(ctx, "search", payload)
	if err != nil {
		return nil, nil, fmt.Errorf("ytm.SearchWithAlbums: %w", err)
	}

	res, err := parse.SearchResults(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("ytm.SearchWithAlbums: %w", err)
	}
	return res.Tracks, res.Albums, nil
}

// SearchAlbums queries YouTube Music for albums matching query and returns the
// parsed album list. It uses the albums-only InnerTube filter params.
func (c *Client) SearchAlbums(ctx context.Context, query string) ([]model.Album, error) {
	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    "WEB_REMIX",
				"clientVersion": "1.20240101.01.00",
				"hl":            "en",
			},
		},
		"query":  query,
		"params": albumsFilterParams,
	}

	raw, err := c.post(ctx, "search", payload)
	if err != nil {
		return nil, fmt.Errorf("ytm.SearchAlbums: %w", err)
	}

	albums, err := parse.SearchAlbums(raw)
	if err != nil {
		return nil, fmt.Errorf("ytm.SearchAlbums: %w", err)
	}
	return albums, nil
}

// GetAlbum browses an album page by its browseId (an MPRE… id) and returns the
// album metadata together with its track list. The returned album's BrowseID is
// always set to the requested browseID even if the page header omits it, and so
// is every track's AlbumID — the rows on an album page all belong to that album.
func (c *Client) GetAlbum(ctx context.Context, browseID string) (model.Album, []model.Track, error) {
	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    "WEB_REMIX",
				"clientVersion": "1.20240101.01.00",
				"hl":            "en",
			},
		},
		"browseId": browseID,
	}

	raw, err := c.post(ctx, "browse", payload)
	if err != nil {
		return model.Album{}, nil, fmt.Errorf("ytm.GetAlbum: %w", err)
	}

	album, tracks, err := parse.AlbumPage(raw)
	if err != nil {
		return model.Album{}, nil, fmt.Errorf("ytm.GetAlbum: %w", err)
	}
	album.BrowseID = browseID
	for i := range tracks {
		tracks[i].AlbumID = browseID
	}
	return album, tracks, nil
}

// ErrNoLyrics reports that YouTube Music has no lyrics for a video (no lyrics
// tab on the watch page, or an empty lyrics browse). Aliased from parse so
// errors.Is works on ytm.Client.Lyrics results.
var ErrNoLyrics = parse.ErrNoLyrics

// Lyrics fetches the plain (unsynced) lyrics YouTube Music has for a video, used
// as a fallback when LRCLIB has no synced lyrics. It is a two-call InnerTube
// flow: POST next {videoId} to locate the lyrics-browse tab (parse.LyricsBrowseID),
// then POST browse {browseId} and extract the description text
// (parse.LyricsText). Returns ErrNoLyrics (matchable with errors.Is) when the
// track has no lyrics; transport/HTTP errors are returned wrapped.
func (c *Client) Lyrics(ctx context.Context, videoID string) (string, error) {
	nextPayload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    "WEB_REMIX",
				"clientVersion": "1.20240101.01.00",
				"hl":            "en",
			},
		},
		"videoId": videoID,
	}

	raw, err := c.post(ctx, "next", nextPayload)
	if err != nil {
		return "", fmt.Errorf("ytm.Lyrics: %w", err)
	}
	browseID, err := parse.LyricsBrowseID(raw)
	if err != nil {
		return "", fmt.Errorf("ytm.Lyrics: %w", err)
	}

	browsePayload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    "WEB_REMIX",
				"clientVersion": "1.20240101.01.00",
				"hl":            "en",
			},
		},
		"browseId": browseID,
	}

	raw, err = c.post(ctx, "browse", browsePayload)
	if err != nil {
		return "", fmt.Errorf("ytm.Lyrics: %w", err)
	}
	text, err := parse.LyricsText(raw)
	if err != nil {
		return "", fmt.Errorf("ytm.Lyrics: %w", err)
	}
	return text, nil
}

// Account reports the signed-in account's display name, satisfying the library
// provider seam the UI uses for its one-shot startup sign-in check. It is a thin
// wrapper over AccountInfo: a signed-in session yields its name, a logged-out
// session yields "" (both with a nil error), and a transport/HTTP failure is
// returned as an error. This lets a cookie *ytm.Client serve as the UI's library
// source interchangeably with the OAuth-backed *ytdata.Client.
func (c *Client) Account(ctx context.Context) (string, error) {
	name, signedIn, err := c.AccountInfo(ctx)
	if err != nil {
		return "", err
	}
	if !signedIn {
		return "", nil
	}
	return name, nil
}

// AccountInfo queries the InnerTube account menu (the account/account_menu
// endpoint) and reports the signed-in account. signedIn is true when YouTube
// returns the signed-in menu — an activeAccountHeaderRenderer — and name is its
// account display name. A logged-out menu (no such renderer) returns
// ("", false, nil): that is a valid result, not an error. HTTP and transport
// failures return a non-nil error.
//
// Because Google rotates session cookies, a copied cookie can resolve as
// anonymous (logged-out menu) even though an auth file is present; this method
// is how tubeamp detects that stale-cookie case.
func (c *Client) AccountInfo(ctx context.Context) (name string, signedIn bool, err error) {
	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    "WEB_REMIX",
				"clientVersion": "1.20240101.01.00",
				"hl":            "en",
			},
		},
	}

	raw, err := c.post(ctx, "account/account_menu", payload)
	if err != nil {
		return "", false, fmt.Errorf("ytm.AccountInfo: %w", err)
	}

	name, signedIn, err = parse.AccountInfo(raw)
	if err != nil {
		return "", false, fmt.Errorf("ytm.AccountInfo: %w", err)
	}
	return name, signedIn, nil
}
