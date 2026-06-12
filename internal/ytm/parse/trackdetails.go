package parse

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TrackDetails extracts the album (name + MPRE… browseId) and the duration of a
// song from an InnerTube `next` (watch) response. The Data API exposes neither
// field, so tubeamp enriches Data-API library tracks by POSTing `next {videoId}`
// to the anonymous InnerTube endpoint and reading the now-playing queue item.
//
// It walks the playlistPanelVideoRenderer queue items: when videoID is non-empty
// the matching item is used, otherwise the first (now-playing) item. The album
// name + browseId come from the item's longByline album run (a browseEndpoint with
// pageType MUSIC_PAGE_TYPE_ALBUM / an MPRE… id), falling back to the row menu's
// "Go to album" navigation item for the id when the byline omits it. The duration
// comes from lengthText ("3:47") or, failing that, a numeric lengthSeconds.
//
// Any field may come back zero (a single with no album, an item with no length);
// that is a valid result, not an error. Only malformed JSON, or a response with no
// queue item at all, returns a non-nil error. Defensive throughout: missing keys
// are skipped, never panicking.
//
// TODO: verify against ytmusicapi — the playlistPanelVideoRenderer / longBylineText
// / lengthText / menu "Go to album" field paths are taken from observed responses
// and ytmusicapi's get_watch_playlist, not a documented contract.
func TrackDetails(raw []byte, videoID string) (album, albumID string, dur time.Duration, err error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", "", 0, fmt.Errorf("parse.TrackDetails: %w", err)
	}

	renderers := findAll(root, "playlistPanelVideoRenderer")
	if len(renderers) == 0 {
		return "", "", 0, fmt.Errorf("parse.TrackDetails: no queue item in response")
	}
	r := renderers[0]
	if videoID != "" {
		for _, cand := range renderers {
			if str(getPath(cand, "videoId")) == videoID {
				r = cand
				break
			}
		}
	}

	album, albumID = albumFromQueueItem(r)
	dur = durationFromQueueItem(r)
	return album, albumID, dur, nil
}

// albumFromQueueItem pulls the album name + MPRE… browseId from a
// playlistPanelVideoRenderer. It prefers the longByline album run (which carries
// the album NAME); when only the row menu's "Go to album" item is present it
// recovers the browseId there (album name then stays "").
func albumFromQueueItem(r map[string]any) (album, albumID string) {
	for _, run := range getSlice(getPath(r, "longBylineText", "runs")) {
		rm := asMap(run)
		be := asMap(getPath(rm, "navigationEndpoint", "browseEndpoint"))
		if be == nil {
			continue
		}
		id := str(getPath(be, "browseId"))
		if isAlbumBrowse(be, id) {
			return str(getPath(rm, "text")), id
		}
	}

	// Fallback: the row's overflow menu "Go to album" navigation item.
	for _, item := range getSlice(getPath(r, "menu", "menuRenderer", "items")) {
		be := asMap(getPath(asMap(item),
			"menuNavigationItemRenderer", "navigationEndpoint", "browseEndpoint"))
		if be == nil {
			continue
		}
		id := str(getPath(be, "browseId"))
		if strings.HasPrefix(id, "MPRE") {
			return "", id
		}
	}
	return "", ""
}

// durationFromQueueItem reads the song length: the formatted lengthText
// ("3:47" / "1:02:33") first, then a numeric lengthSeconds string.
func durationFromQueueItem(r map[string]any) time.Duration {
	for _, run := range getSlice(getPath(r, "lengthText", "runs")) {
		if d, ok := parseDuration(str(getPath(asMap(run), "text"))); ok {
			return d
		}
	}
	if d, ok := parseDuration(str(getPath(r, "lengthText", "simpleText"))); ok {
		return d
	}
	if secs := str(getPath(r, "lengthSeconds")); secs != "" {
		if n, err := strconv.Atoi(secs); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 0
}
