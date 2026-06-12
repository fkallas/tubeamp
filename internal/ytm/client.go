package ytm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
type Client struct {
	hc   *http.Client
	auth *Auth
}

// NewClient creates a new Client. a may be nil for unauthenticated access;
// search still works without authentication.
func NewClient(a *Auth) *Client {
	return &Client{
		hc:   &http.Client{Timeout: 10 * time.Second},
		auth: a,
	}
}

// post sends a JSON POST to https://music.youtube.com/youtubei/v1/<endpoint>
// and returns the raw response body. Non-2xx responses yield an error that
// includes the status code and a truncated body.
func (c *Client) post(ctx context.Context, endpoint string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("ytm: marshal payload: %w", err)
	}

	url := ytmBase + "/" + endpoint + "?prettyPrint=false"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ytm: build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ytmUA)
	req.Header.Set("Origin", ytmOrigin)
	req.Header.Set("X-Origin", ytmOrigin)

	if c.auth != nil {
		sapisid, err := c.auth.SAPISID()
		if err != nil {
			return nil, fmt.Errorf("ytm: get SAPISID: %w", err)
		}
		req.Header.Set("Cookie", c.auth.Cookie)
		req.Header.Set("Authorization", sapisidHash(sapisid, ytmOrigin, time.Now()))
		req.Header.Set("X-Goog-AuthUser", "0")
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ytm: HTTP request: %w", err)
	}
	defer resp.Body.Close()

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
// always set to the requested browseID even if the page header omits it.
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
	return album, tracks, nil
}
