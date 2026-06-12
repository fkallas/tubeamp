// Package parse contains all response JSON parsing for the YTM InnerTube API.
// All functions are defensive: missing keys return zero values and never panic.
package parse

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fkallas/tubeamp/internal/model"
)

// SearchTracks parses the raw JSON response from InnerTube search and returns
// the song tracks found. Items without a videoId are silently skipped. The
// function never panics on malformed input.
func SearchTracks(raw []byte) ([]model.Track, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse.SearchTracks: %w", err)
	}

	// Navigate: contents -> tabbedSearchResultsRenderer -> tabs[0] ->
	// tabRenderer -> content -> sectionListRenderer -> contents -> find
	// musicShelfRenderer -> contents -> musicResponsiveListItemRenderer items.
	tabs := getSlice(getPath(root,
		"contents",
		"tabbedSearchResultsRenderer",
		"tabs",
	))

	var shelfContents []any
	for _, tab := range tabs {
		tabMap := asMap(tab)
		slContents := getSlice(getPath(tabMap,
			"tabRenderer", "content", "sectionListRenderer", "contents"))
		for _, section := range slContents {
			sectionMap := asMap(section)
			shelf := getPath(sectionMap, "musicShelfRenderer")
			if shelf == nil {
				continue
			}
			contents := getSlice(getPath(asMap(shelf), "contents"))
			if len(contents) > 0 {
				shelfContents = contents
				break
			}
		}
		if len(shelfContents) > 0 {
			break
		}
	}

	var tracks []model.Track
	for _, item := range shelfContents {
		itemMap := asMap(item)
		renderer := asMap(getPath(itemMap, "musicResponsiveListItemRenderer"))
		if renderer == nil {
			continue
		}
		t, ok := parseItem(renderer)
		if !ok {
			continue
		}
		tracks = append(tracks, t)
	}
	return tracks, nil
}

// parseItem extracts a model.Track from a musicResponsiveListItemRenderer map.
// Returns (track, true) on success, (zero, false) when the item lacks a videoId.
func parseItem(r map[string]any) (model.Track, bool) {
	videoID := extractVideoID(r)
	if videoID == "" {
		return model.Track{}, false
	}

	flexCols := getSlice(getPath(r, "flexColumns"))

	title := extractTitle(flexCols)
	artists, album := extractArtistsAlbum(flexCols)
	duration := extractDuration(r, flexCols)
	thumb := extractThumbnail(r)

	return model.Track{
		VideoID:  videoID,
		Title:    title,
		Artists:  artists,
		Album:    album,
		Duration: duration,
		ThumbURL: thumb,
	}, true
}

// extractVideoID tries playlistItemData.videoId first, then the overlay watch
// endpoint, then the first flex-column title run watch endpoint.
func extractVideoID(r map[string]any) string {
	// 1. playlistItemData.videoId
	if v := str(getPath(r, "playlistItemData", "videoId")); v != "" {
		return v
	}
	// 2. overlay -> musicItemThumbnailOverlayRenderer -> content ->
	//    musicPlayButtonRenderer -> playNavigationEndpoint -> watchEndpoint -> videoId
	if v := str(getPath(r,
		"overlay",
		"musicItemThumbnailOverlayRenderer",
		"content",
		"musicPlayButtonRenderer",
		"playNavigationEndpoint",
		"watchEndpoint",
		"videoId",
	)); v != "" {
		return v
	}
	// 3. flexColumns[0] title run watchEndpoint
	flexCols := getSlice(getPath(r, "flexColumns"))
	if len(flexCols) > 0 {
		runs := getSlice(getPath(asMap(flexCols[0]),
			"musicResponsiveListItemFlexColumnRenderer", "text", "runs"))
		for _, run := range runs {
			if v := str(getPath(asMap(run), "navigationEndpoint", "watchEndpoint", "videoId")); v != "" {
				return v
			}
		}
	}
	return ""
}

// extractTitle returns the concatenated text from flexColumn 0 runs.
func extractTitle(flexCols []any) string {
	if len(flexCols) == 0 {
		return ""
	}
	runs := getSlice(getPath(asMap(flexCols[0]),
		"musicResponsiveListItemFlexColumnRenderer", "text", "runs"))
	var parts []string
	for _, run := range runs {
		if t := str(getPath(asMap(run), "text")); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "")
}

// extractArtistsAlbum parses flexColumn 1 runs.
// Runs whose browseEndpoint pageType is MUSIC_PAGE_TYPE_ALBUM → album.
// Runs with any other browseEndpoint → artist names.
// Separator runs (" • " etc.) are ignored.
func extractArtistsAlbum(flexCols []any) (artists []string, album string) {
	if len(flexCols) < 2 {
		return nil, ""
	}
	runs := getSlice(getPath(asMap(flexCols[1]),
		"musicResponsiveListItemFlexColumnRenderer", "text", "runs"))
	for _, run := range runs {
		runMap := asMap(run)
		text := str(getPath(runMap, "text"))
		if text == "" || strings.TrimSpace(text) == "•" || strings.TrimSpace(text) == "" {
			continue
		}

		// Check if there is a browseEndpoint
		browseEP := getPath(runMap, "navigationEndpoint", "browseEndpoint")
		if browseEP == nil {
			// No navigation → separator or plain text, skip
			continue
		}

		pageType := str(getPath(asMap(browseEP),
			"browseEndpointContextSupportedConfigs",
			"browseEndpointContextMusicConfig",
			"pageType"))

		if pageType == "MUSIC_PAGE_TYPE_ALBUM" {
			if album == "" {
				album = text
			}
		} else {
			// Treat as artist (MUSIC_PAGE_TYPE_ARTIST or untyped browse)
			artists = append(artists, text)
		}
	}
	return artists, album
}

// extractDuration parses duration from fixedColumns[0] text first; falls back
// to checking the last flex-column run for a time-like string.
func extractDuration(r map[string]any, flexCols []any) time.Duration {
	// 1. fixedColumns[0]
	fixedCols := getSlice(getPath(r, "fixedColumns"))
	if len(fixedCols) > 0 {
		runs := getSlice(getPath(asMap(fixedCols[0]),
			"musicResponsiveListItemFixedColumnRenderer", "text", "runs"))
		for _, run := range runs {
			if d, ok := parseDuration(str(getPath(asMap(run), "text"))); ok {
				return d
			}
		}
	}
	// 2. Last run of any flexColumn that looks like a duration
	for i := len(flexCols) - 1; i >= 0; i-- {
		runs := getSlice(getPath(asMap(flexCols[i]),
			"musicResponsiveListItemFlexColumnRenderer", "text", "runs"))
		for j := len(runs) - 1; j >= 0; j-- {
			if d, ok := parseDuration(str(getPath(asMap(runs[j]), "text"))); ok {
				return d
			}
		}
	}
	return 0
}

// parseDuration parses "3:47" or "1:02:33" into a time.Duration.
func parseDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 2:
		m, err1 := strconv.Atoi(parts[0])
		sec, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return 0, false
		}
		return time.Duration(m)*time.Minute + time.Duration(sec)*time.Second, true
	case 3:
		h, err1 := strconv.Atoi(parts[0])
		m, err2 := strconv.Atoi(parts[1])
		sec, err3 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || err3 != nil {
			return 0, false
		}
		return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second, true
	}
	return 0, false
}

// extractThumbnail returns the URL of the largest thumbnail in
// musicThumbnailRenderer.thumbnail.thumbnails (highest width).
func extractThumbnail(r map[string]any) string {
	thumbnails := getSlice(getPath(r,
		"thumbnail",
		"musicThumbnailRenderer",
		"thumbnail",
		"thumbnails",
	))
	best := ""
	bestW := -1
	for _, thumb := range thumbnails {
		thumbMap := asMap(thumb)
		u := str(getPath(thumbMap, "url"))
		if u == "" {
			continue
		}
		w := 0
		if wv, ok := thumbMap["width"]; ok {
			switch v := wv.(type) {
			case float64:
				w = int(v)
			case int:
				w = v
			}
		}
		if w > bestW {
			bestW = w
			best = u
		}
	}
	return best
}

// ---- map navigation helpers ----

// getPath walks a chain of string keys through nested map[string]any values.
// A numeric string key can index into a []any slice.
// Returns nil if any key is missing or the intermediate value is not a map/slice.
func getPath(v any, keys ...string) any {
	cur := v
	for _, k := range keys {
		switch m := cur.(type) {
		case map[string]any:
			cur = m[k]
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(m) {
				return nil
			}
			cur = m[i]
		default:
			return nil
		}
	}
	return cur
}

// getSlice asserts v is []any; returns nil otherwise.
func getSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// asMap asserts v is map[string]any; returns nil otherwise.
func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// str asserts v is a string; returns "" otherwise.
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
