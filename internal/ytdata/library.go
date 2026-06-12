package ytdata

import (
	"context"
	"log"
	"sort"
	"strings"

	"github.com/fkallas/tubeamp/internal/model"
)

// LibrarySongs returns the user's whole library as a single aggregated track
// list: the Liked Songs ("LL") first, then every owned playlist's tracks (in
// LibraryPlaylists order), deduped by VideoID with the first occurrence winning
// (so a song's liked-list position is preserved over a later playlist copy).
//
// It is deliberately resilient: a single playlist fetch that fails is logged and
// skipped, yielding a partial-but-usable aggregate rather than a hard error. Only
// a LikedSongs or LibraryPlaylists failure (the spine of the aggregate) is fatal.
// Be aware this fans out one playlistItems pagination per playlist, so it is the
// most quota- and time-expensive call in this package; callers should cache the
// result and avoid calling it on a hot path.
func (c *Client) LibrarySongs(ctx context.Context) ([]model.Track, error) {
	liked, err := c.LikedSongs(ctx)
	if err != nil {
		return nil, err
	}

	playlists, err := c.LibraryPlaylists(ctx)
	if err != nil {
		return nil, err
	}

	var out []model.Track
	seen := make(map[string]bool)
	add := func(tracks []model.Track) {
		for _, t := range tracks {
			if t.VideoID == "" || seen[t.VideoID] {
				continue
			}
			seen[t.VideoID] = true
			out = append(out, t)
		}
	}

	add(liked)
	for _, pl := range playlists {
		tracks, err := c.PlaylistTracks(ctx, pl.ID)
		if err != nil {
			// Log-skip: a single bad playlist must not sink the whole aggregate.
			log.Printf("ytdata.LibrarySongs: skipping playlist %q (%s): %v", pl.ID, pl.Title, err)
			continue
		}
		add(tracks)
	}
	return out, nil
}

// ArtistGroup is a primary artist together with the library tracks attributed to
// them. It backs the Library "Artists" section: because OAuth cannot reach the
// InnerTube artist-browse pages, grouping the aggregated library by its tracks'
// primary artist is the honest, working substitute.
type ArtistGroup struct {
	Name   string
	Tracks []model.Track
}

// GroupArtists groups tracks by their primary artist (Artists[0]). Tracks with no
// artist are skipped. Grouping is case-insensitive (so "Queen" and "queen" land
// together) but the display Name keeps the first-seen casing. The returned groups
// are sorted by Name (case-insensitive); within each group the tracks keep their
// input order. Pure — it never touches the network.
func GroupArtists(tracks []model.Track) []ArtistGroup {
	byKey := make(map[string]*ArtistGroup)
	var order []string
	for _, t := range tracks {
		if len(t.Artists) == 0 {
			continue
		}
		name := strings.TrimSpace(t.Artists[0])
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		g, ok := byKey[key]
		if !ok {
			g = &ArtistGroup{Name: name}
			byKey[key] = g
			order = append(order, key)
		}
		g.Tracks = append(g.Tracks, t)
	}

	groups := make([]ArtistGroup, 0, len(order))
	for _, key := range order {
		groups = append(groups, *byKey[key])
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name)
	})
	return groups
}

// AlbumsFromTracks builds the Library "Albums" section from (ideally enriched)
// tracks: it groups them by AlbumID, emitting one model.Album per distinct album.
// Tracks with no AlbumID (e.g. un-enriched Data-API rows) are skipped, so this is
// only meaningful after enrichment has filled the album refs. Each album takes its
// Title/Artists/ThumbURL from the first track carrying them and BrowseID = AlbumID
// (so the existing GetAlbum flow can open it). Albums are deduped by AlbumID and
// returned sorted by Title (case-insensitive). Pure.
func AlbumsFromTracks(tracks []model.Track) []model.Album {
	byID := make(map[string]*model.Album)
	var order []string
	for _, t := range tracks {
		if t.AlbumID == "" {
			continue
		}
		a, ok := byID[t.AlbumID]
		if !ok {
			a = &model.Album{BrowseID: t.AlbumID}
			byID[t.AlbumID] = a
			order = append(order, t.AlbumID)
		}
		// Fill any field still missing from this track (first non-empty wins).
		if a.Title == "" {
			a.Title = t.Album
		}
		if len(a.Artists) == 0 {
			a.Artists = t.Artists
		}
		if a.ThumbURL == "" {
			a.ThumbURL = t.ThumbURL
		}
	}

	albums := make([]model.Album, 0, len(order))
	for _, id := range order {
		albums = append(albums, *byID[id])
	}
	sort.SliceStable(albums, func(i, j int) bool {
		return strings.ToLower(albums[i].Title) < strings.ToLower(albums[j].Title)
	})
	return albums
}
