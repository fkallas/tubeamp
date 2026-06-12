package ytm

import (
	"context"
	"fmt"
	"strings"

	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/ytm/parse"
)

// ErrNotSignedIn is returned by the library and playlist browse methods when the
// InnerTube response is the logged-out page (the expected renderer is absent)
// rather than a real — possibly empty — library result. These browses require a
// live signed-in session; a missing or stale (anonymous) cookie triggers it. It
// aliases parse.ErrNotSignedIn, so callers can match it with errors.Is even
// through the methods' fmt.Errorf wrapping.
var ErrNotSignedIn = parse.ErrNotSignedIn

// browse POSTs a standard WEB_REMIX browse request for browseID and returns the
// raw response body.
func (c *Client) browse(ctx context.Context, browseID string) ([]byte, error) {
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
	return c.post(ctx, "browse", payload)
}

// LibraryPlaylists browses the signed-in user's playlists (browseId
// FEmusic_liked_playlists) and returns them. The synthetic "New playlist" tile is
// skipped and each playlist id has its "VL" prefix stripped. It requires
// authentication: an anonymous session returns ErrNotSignedIn.
func (c *Client) LibraryPlaylists(ctx context.Context) ([]model.Playlist, error) {
	raw, err := c.browse(ctx, "FEmusic_liked_playlists")
	if err != nil {
		return nil, fmt.Errorf("ytm.LibraryPlaylists: %w", err)
	}
	pls, err := parse.LibraryPlaylists(raw)
	if err != nil {
		return nil, fmt.Errorf("ytm.LibraryPlaylists: %w", err)
	}
	return pls, nil
}

// LikedSongs browses the Liked Songs auto-playlist (browseId "VLLM") and returns
// its tracks. It requires authentication: an anonymous session returns
// ErrNotSignedIn.
//
// TODO(continuation): only the first page (~100 tracks) is fetched; following the
// shelf continuation would load the rest.
func (c *Client) LikedSongs(ctx context.Context) ([]model.Track, error) {
	raw, err := c.browse(ctx, "VLLM")
	if err != nil {
		return nil, fmt.Errorf("ytm.LikedSongs: %w", err)
	}
	ts, err := parse.PlaylistTracks(raw)
	if err != nil {
		return nil, fmt.Errorf("ytm.LikedSongs: %w", err)
	}
	return ts, nil
}

// PlaylistTracks browses a playlist by id (browseId "VL"+playlistID, accepting an
// id that already carries the prefix) and returns its tracks. It requires
// authentication for private/library playlists: an anonymous session returns
// ErrNotSignedIn.
//
// TODO(continuation): only the first page (~100 tracks) is fetched.
func (c *Client) PlaylistTracks(ctx context.Context, playlistID string) ([]model.Track, error) {
	browseID := playlistID
	if !strings.HasPrefix(browseID, "VL") {
		browseID = "VL" + browseID
	}
	raw, err := c.browse(ctx, browseID)
	if err != nil {
		return nil, fmt.Errorf("ytm.PlaylistTracks: %w", err)
	}
	ts, err := parse.PlaylistTracks(raw)
	if err != nil {
		return nil, fmt.Errorf("ytm.PlaylistTracks: %w", err)
	}
	return ts, nil
}
