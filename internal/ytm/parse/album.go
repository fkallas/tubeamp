package parse

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fkallas/tubeamp/internal/model"
)

// SearchAlbums parses the raw JSON response from an albums-filtered InnerTube
// search and returns the albums found. It walks the standard search shelf
// (musicShelfRenderer) and, when present, the top-result card
// (musicCardShelfRenderer). Items without an album browseId are silently
// skipped and duplicates (same browseId) are collapsed. Never panics.
func SearchAlbums(raw []byte) ([]model.Album, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse.SearchAlbums: %w", err)
	}

	var albums []model.Album
	seen := map[string]bool{}
	addAlbum := func(a model.Album, ok bool) {
		if !ok || a.BrowseID == "" || seen[a.BrowseID] {
			return
		}
		seen[a.BrowseID] = true
		albums = append(albums, a)
	}

	tabs := getSlice(getPath(root,
		"contents", "tabbedSearchResultsRenderer", "tabs"))
	for _, tab := range tabs {
		sections := getSlice(getPath(asMap(tab),
			"tabRenderer", "content", "sectionListRenderer", "contents"))
		for _, section := range sections {
			sm := asMap(section)
			// Top-result card shelf (e.g. an unfiltered search's "Top result").
			if card := asMap(getPath(sm, "musicCardShelfRenderer")); card != nil {
				addAlbum(parseAlbumCard(card))
			}
			// Standard list shelf of albums.
			if shelf := asMap(getPath(sm, "musicShelfRenderer")); shelf != nil {
				for _, item := range getSlice(getPath(shelf, "contents")) {
					r := asMap(getPath(asMap(item), "musicResponsiveListItemRenderer"))
					if r == nil {
						continue
					}
					addAlbum(parseSearchAlbumItem(r))
				}
			}
		}
	}
	return albums, nil
}

// parseSearchAlbumItem extracts a model.Album from a musicResponsiveListItemRenderer.
// Returns ok=false when the item has no album browseId.
func parseSearchAlbumItem(r map[string]any) (model.Album, bool) {
	be := asMap(getPath(r, "navigationEndpoint", "browseEndpoint"))
	browseID := str(getPath(be, "browseId"))
	if browseID == "" || !isAlbumBrowse(be, browseID) {
		return model.Album{}, false
	}

	flexCols := getSlice(getPath(r, "flexColumns"))
	artists, year := parseSubtitleRuns(getSlice(getPath(asMap(getAt(flexCols, 1)),
		"musicResponsiveListItemFlexColumnRenderer", "text", "runs")))

	return model.Album{
		BrowseID: browseID,
		Title:    extractTitle(flexCols),
		Artists:  artists,
		Year:     year,
		ThumbURL: extractThumbnail(r),
	}, true
}

// parseAlbumCard extracts a model.Album from a top-result musicCardShelfRenderer.
// Returns ok=false when no album browseId is reachable.
func parseAlbumCard(card map[string]any) (model.Album, bool) {
	titleRuns := getSlice(getPath(card, "title", "runs"))
	browseID := ""
	for _, run := range titleRuns {
		if id := str(getPath(asMap(run), "navigationEndpoint", "browseEndpoint", "browseId")); id != "" {
			browseID = id
			break
		}
	}
	if browseID == "" {
		browseID = str(getPath(card, "onTap", "browseEndpoint", "browseId"))
	}
	if browseID == "" || !strings.HasPrefix(browseID, "MPRE") {
		return model.Album{}, false
	}

	artists, year := parseSubtitleRuns(getSlice(getPath(card, "subtitle", "runs")))
	return model.Album{
		BrowseID: browseID,
		Title:    joinRuns(titleRuns),
		Artists:  artists,
		Year:     year,
		ThumbURL: largestThumb(getPath(card, "thumbnail")),
	}, true
}

// AlbumPage parses the raw JSON response from a browse of an album page and
// returns the album metadata and its track list. It handles both the older
// musicDetailHeaderRenderer and the newer musicResponsiveHeaderRenderer header
// shapes. Per-track artists fall back to the album artists; each track's Album
// is the album title and ThumbURL the album cover. Malformed tracks are skipped.
// Never panics. The returned Album has no BrowseID (the caller knows it).
func AlbumPage(raw []byte) (model.Album, []model.Track, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return model.Album{}, nil, fmt.Errorf("parse.AlbumPage: %w", err)
	}
	album := parseAlbumHeader(root)
	tracks := parseAlbumTracks(root, album)
	return album, tracks, nil
}

// parseAlbumHeader reads the album metadata from whichever header renderer is
// present. Both shapes nest the cover under the header's "thumbnail" key (which
// excludes the artist's straplineThumbnail), the title under "title.runs", and
// type/artist/year runs across "subtitle" and "straplineTextOne".
func parseAlbumHeader(root map[string]any) model.Album {
	var hdr map[string]any
	if hs := findAll(root, "musicDetailHeaderRenderer"); len(hs) > 0 {
		hdr = hs[0]
	} else if hs := findAll(root, "musicResponsiveHeaderRenderer"); len(hs) > 0 {
		hdr = hs[0]
	}
	if hdr == nil {
		return model.Album{}
	}

	a1, y1 := parseSubtitleRuns(getSlice(getPath(hdr, "subtitle", "runs")))
	a2, y2 := parseSubtitleRuns(getSlice(getPath(hdr, "straplineTextOne", "runs")))
	year := y1
	if year == "" {
		year = y2
	}
	return model.Album{
		Title:    joinRuns(getSlice(getPath(hdr, "title", "runs"))),
		Artists:  append(a1, a2...),
		Year:     year,
		ThumbURL: largestThumb(getPath(hdr, "thumbnail")),
	}
}

// parseAlbumTracks walks the first track-bearing musicShelfRenderer.
func parseAlbumTracks(root map[string]any, album model.Album) []model.Track {
	var tracks []model.Track
	for _, shelf := range findAll(root, "musicShelfRenderer") {
		for _, item := range getSlice(getPath(shelf, "contents")) {
			r := asMap(getPath(asMap(item), "musicResponsiveListItemRenderer"))
			if r == nil {
				continue
			}
			t, ok := parseAlbumTrack(r, album)
			if !ok {
				continue
			}
			tracks = append(tracks, t)
		}
		if len(tracks) > 0 {
			break
		}
	}
	return tracks
}

// parseAlbumTrack extracts a track from an album shelf item. Per-track artists
// fall back to the album artists when the item has none.
func parseAlbumTrack(r map[string]any, album model.Album) (model.Track, bool) {
	videoID := extractVideoID(r)
	if videoID == "" {
		return model.Track{}, false
	}
	flexCols := getSlice(getPath(r, "flexColumns"))
	artists, _ := extractArtistsAlbum(flexCols)
	if len(artists) == 0 {
		artists = album.Artists
	}
	return model.Track{
		VideoID:  videoID,
		Title:    extractTitle(flexCols),
		Artists:  artists,
		Album:    album.Title,
		Duration: extractDuration(r, flexCols),
		ThumbURL: album.ThumbURL,
	}, true
}

// ---- album-specific helpers ----

// parseSubtitleRuns scans a run list of the form "Type • Artist • Year" (or
// "Artist & Artist"), returning artist names (runs with a non-album
// browseEndpoint) and the first 4-digit year run. Separator and type runs are
// ignored.
func parseSubtitleRuns(runs []any) (artists []string, year string) {
	for _, run := range runs {
		rm := asMap(run)
		text := strings.TrimSpace(str(getPath(rm, "text")))
		if text == "" || text == "•" || text == "&" {
			continue
		}
		if year == "" && isYear(text) {
			year = text
			continue
		}
		be := asMap(getPath(rm, "navigationEndpoint", "browseEndpoint"))
		if be == nil {
			// Plain text like the album type ("Album"/"Single"/"EP"); skip.
			continue
		}
		if str(getPath(be,
			"browseEndpointContextSupportedConfigs",
			"browseEndpointContextMusicConfig",
			"pageType")) == "MUSIC_PAGE_TYPE_ALBUM" {
			continue
		}
		artists = append(artists, text)
	}
	return artists, year
}

// isAlbumBrowse reports whether a browseEndpoint points at an album page.
func isAlbumBrowse(be map[string]any, browseID string) bool {
	pageType := str(getPath(be,
		"browseEndpointContextSupportedConfigs",
		"browseEndpointContextMusicConfig",
		"pageType"))
	return pageType == "MUSIC_PAGE_TYPE_ALBUM" || strings.HasPrefix(browseID, "MPRE")
}

// isYear reports whether s is a plausible 4-digit release year.
func isYear(s string) bool {
	if len(s) != 4 || (s[0] != '1' && s[0] != '2') {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// joinRuns concatenates the text of every run in the slice.
func joinRuns(runs []any) string {
	var parts []string
	for _, run := range runs {
		if t := str(getPath(asMap(run), "text")); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "")
}

// getAt returns s[i] or nil when out of range.
func getAt(s []any, i int) any {
	if i < 0 || i >= len(s) {
		return nil
	}
	return s[i]
}

// largestThumb walks node (a subtree) and returns the URL of the widest object
// carrying a "url"+"width" pair. Used for thumbnails, which differ in nesting
// between header shapes (musicThumbnailRenderer vs croppedSquareThumbnailRenderer).
func largestThumb(node any) string {
	best := ""
	bestW := -1
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if u, ok := t["url"].(string); ok && u != "" {
				if w := toInt(t["width"]); w > bestW {
					bestW = w
					best = u
				}
			}
			for _, k := range sortedKeys(t) {
				walk(t[k])
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(node)
	return best
}

// findAll returns every map value stored under key anywhere in node, in a
// deterministic (sorted-key) depth-first order.
func findAll(node any, key string) []map[string]any {
	var out []map[string]any
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for _, k := range sortedKeys(t) {
				if k == key {
					if m := asMap(t[k]); m != nil {
						out = append(out, m)
					}
				}
				walk(t[k])
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(node)
	return out
}

// sortedKeys returns m's keys sorted, for deterministic traversal.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// toInt coerces a JSON number (float64) or int to int.
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
