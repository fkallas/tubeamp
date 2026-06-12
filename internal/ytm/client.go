package ytm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
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

// Client is an authenticated or unauthenticated InnerTube client for YouTube Music.
//
// It supports two authentication modes. The default cookie mode signs requests
// with the Cookie header + a SAPISIDHASH Authorization (see Auth). OAuth mode
// (UseOAuth) instead sends an Authorization: Bearer <access_token> with the OAuth
// client User-Agent, refreshing the access token when it expires. OAuth, when
// configured, takes precedence over a cookie Auth.
type Client struct {
	hc       *http.Client
	auth     *Auth
	authUser int    // X-Goog-AuthUser account index (config auth_user)
	baseURL  string // InnerTube base; overridable in tests

	// OAuth mode (set by UseOAuth). When oauth is non-nil, post() uses Bearer
	// auth instead of the cookie/SAPISIDHASH path. oauthMu guards the
	// refresh-and-persist of the token across concurrent requests.
	oauthMu    sync.Mutex
	oauth      *OAuthToken
	oauthCreds OAuthCreds
	oauthPath  string // where refreshed tokens are persisted; "" = memory only
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

// UseOAuth switches the client into OAuth Bearer mode using tok and creds.
// Requests then carry Authorization: Bearer <access_token> (and the OAuth client
// User-Agent) instead of the cookie/SAPISIDHASH headers. When the access token
// is expired it is refreshed before the request and, if tokenPath is non-empty,
// the refreshed token is persisted there atomically (0600). OAuth takes
// precedence over any cookie Auth passed to NewClient.
func (c *Client) UseOAuth(tok *OAuthToken, creds OAuthCreds, tokenPath string) {
	c.oauthMu.Lock()
	defer c.oauthMu.Unlock()
	c.oauth = tok
	c.oauthCreds = creds
	c.oauthPath = tokenPath
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

// Authenticated reports whether the client carries credentials — a cookie Auth
// was loaded, or OAuth mode is configured. It is NOT a live sign-in check (a
// stale cookie or revoked token may still resolve anonymous on YouTube's side);
// use AccountInfo to confirm the live sign-in state.
func (c *Client) Authenticated() bool {
	return c.auth != nil || c.UsingOAuth()
}

// UsingOAuth reports whether the client is in OAuth Bearer mode (UseOAuth was
// called). The read takes the same mutex UseOAuth writes under, so callers —
// including post()'s mode dispatch and Authenticated() — are safe against a
// concurrent UseOAuth on a live client.
func (c *Client) UsingOAuth() bool {
	c.oauthMu.Lock()
	defer c.oauthMu.Unlock()
	return c.oauth != nil
}

// oauthAuthorization returns the Authorization header value for OAuth mode,
// refreshing the access token first when it has expired and persisting the
// refreshed token (best-effort) when a path is configured. The lock serializes
// concurrent requests so only one refresh runs at a time.
func (c *Client) oauthAuthorization(ctx context.Context) (string, error) {
	c.oauthMu.Lock()
	defer c.oauthMu.Unlock()
	if c.oauth.IsExpired(time.Now()) {
		if err := c.oauth.Refresh(ctx, c.oauthCreds); err != nil {
			return "", fmt.Errorf("ytm: refresh oauth token: %w", err)
		}
		if c.oauthPath != "" {
			// Best-effort: the refreshed token is already live in memory, so a
			// persistence failure must not break the request.
			_ = c.oauth.Save(c.oauthPath)
		}
	}
	return c.oauth.Authorization(), nil
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

	switch {
	case c.UsingOAuth():
		// OAuth mode: a Bearer access token (refreshed when expired) and the
		// OAuth client User-Agent — no Cookie, no SAPISIDHASH. The InnerTube
		// payload still carries the WEB_REMIX context (ytmusicapi does the same).
		authz, err := c.oauthAuthorization(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", oauthInnerTubeUA)
		req.Header.Set("Authorization", authz)
		req.Header.Set("X-Goog-Request-Time", strconv.FormatInt(time.Now().Unix(), 10))
	case c.auth != nil:
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
