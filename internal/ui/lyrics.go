// Package ui — synced-lyrics panel logic: the per-track fetch (LRCLIB with a
// YouTube Music plain-text fallback), the videoID-keyed cache that keeps replays
// and seeks from refetching, and the layout/visibility helpers the View uses.
//
// The panel is PURE DISPLAY: it is never a focusArea, never receives keys, and
// the 1/2/3/4 + h/l focus controls never reach it. It is shown only while a
// track is playing (m.hasNow) and the terminal is tall enough to fit it without
// starving the main view (see lyricsBandHeight). The highlighted line is driven
// off the same playback position the progress bar uses (m.timePos), via
// lyrics.CurrentLine — no extra event wiring.
package ui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fkallas/tubeamp/internal/lyrics"
	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/ui/panels"
	"github.com/fkallas/tubeamp/internal/ytm"
)

// Lyrics-panel layout. The band is a fixed share of the top area, clamped so the
// main view always keeps at least lyricsMainMinH rows; when even the smallest
// band would starve the main view the panel is hidden and the main view reclaims
// the rows.
const (
	lyricsBandRows = 10 // preferred lyrics box height (8 content rows)
	lyricsBandMin  = 6  // smallest lyrics box worth showing (4 content rows)
	lyricsMainMinH = 8  // the main view keeps at least this many box rows when lyrics show
)

// lyricStatus tracks one track's lyrics-lookup lifecycle in the cache.
type lyricStatus int

const (
	lyricLoading  lyricStatus = iota // a fetch is in flight
	lyricResolved                    // the lookup finished (see found)
)

// lyricResult is a cached lyrics lookup for one track (keyed by videoID). found
// is false when the lookup resolved with nothing usable.
type lyricResult struct {
	status lyricStatus
	ly     lyrics.Lyrics
	found  bool
}

// lyricsMsg carries an async lyrics-lookup result back into Update. videoID
// keys the cache so replays/seeks reuse it. No generation guard is needed: the
// cache is keyed by videoID and only the CURRENT track's entry is ever
// displayed, so a late result is always valid for its own key.
type lyricsMsg struct {
	videoID string
	ly      lyrics.Lyrics
	found   bool
}

// lyricsFetch is the lyrics-lookup seam: LRCLIB first, then YouTube Music's
// plain-text fallback when a client is present. It is a package var so tests can
// replace it with a stub that never touches the network. found is false when
// nothing usable was resolved (or the lookup failed — lyrics are best-effort and
// never retried).
var lyricsFetch = fetchLyrics

func fetchLyrics(ctx context.Context, c *ytm.Client, t model.Track) (lyrics.Lyrics, bool) {
	ly, err := lyrics.FetchLRCLIB(ctx, t.ArtistLine(), t.Title, t.Album, t.Duration)
	if err == nil {
		return ly, true
	}
	// LRCLIB has nothing: fall back to YouTube Music's plain lyrics when a client
	// is available. A nil client (degraded mode) simply yields "none" — never a
	// nil dereference.
	if errors.Is(err, lyrics.ErrNoLyrics) && c != nil {
		if plain, perr := c.Lyrics(ctx, t.VideoID); perr == nil && strings.TrimSpace(plain) != "" {
			return lyrics.Lyrics{Plain: plain, Source: lyrics.SourceYTMusic}, true
		}
	}
	return lyrics.Lyrics{}, false
}

// fetchLyricsCmd looks up the track's lyrics off the Update goroutine, bounded by
// an ~8s context, and reports the result as a lyricsMsg.
func fetchLyricsCmd(c *ytm.Client, t model.Track) tea.Cmd {
	vid := t.VideoID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		ly, found := lyricsFetch(ctx, c, t)
		return lyricsMsg{videoID: vid, ly: ly, found: found}
	}
}

// ensureLyrics makes sure the now-playing track's lyrics are loading or loaded.
// A track already in the cache (resolved or in flight) is reused — replays and
// seeks never refetch. Only a genuinely new track dispatches a fetch (the
// loading cache entry itself dedupes in-flight lookups, so at most one fetch
// per videoID ever runs).
func (m *Model) ensureLyrics() tea.Cmd {
	if !m.hasNow {
		return nil
	}
	vid := m.nowPlaying.VideoID
	if _, ok := m.lyricsCache[vid]; ok {
		return nil
	}
	m.lyricsCache[vid] = lyricResult{status: lyricLoading}
	return fetchLyricsCmd(m.c, m.nowPlaying)
}

// applyLyrics folds a lyrics result into the cache, replacing the loading entry
// ensureLyrics seeded, so a replay/seek reuses it. The result is ALWAYS
// recorded — even for a track we have moved past — because the cache is keyed
// by videoID and only the current track's entry is displayed: a late result is
// always valid for its own key, and dropping it would leave that key stuck on
// the loading entry forever (ensureLyrics would treat it as a hit and never
// refetch, sticking the panel on "searching for lyrics…" when the user returns
// to the track).
func (m *Model) applyLyrics(msg lyricsMsg) {
	m.lyricsCache[msg.videoID] = lyricResult{status: lyricResolved, ly: msg.ly, found: msg.found}
}

// lyricsBandHeight returns the lyrics panel's box height for a top area of topH
// rows, or 0 when the panel is hidden (no track playing, or too short to fit
// without starving the main view). When shown it is at most lyricsBandRows and
// always leaves the main view >= lyricsMainMinH rows.
func (m Model) lyricsBandHeight(topH int) int {
	if !m.hasNow {
		return 0
	}
	avail := topH - lyricsMainMinH
	if avail < lyricsBandMin {
		return 0
	}
	if avail < lyricsBandRows {
		return avail
	}
	return lyricsBandRows
}

// lyricsForView resolves the current track's cache entry into the arguments
// panels.LyricsView needs. The highlighted line is computed from the same
// playback position the progress bar uses (m.timePos). Only called when the
// panel is visible, so m.nowPlaying is valid.
func (m Model) lyricsForView() (panels.LyricsState, []lyrics.Line, string, int) {
	r, ok := m.lyricsCache[m.nowPlaying.VideoID]
	if !ok || r.status == lyricLoading {
		return panels.LyricsLoading, nil, "", -1
	}
	if !r.found {
		return panels.LyricsNone, nil, "", -1
	}
	if r.ly.Synced {
		pos := time.Duration(m.timePos * float64(time.Second))
		return panels.LyricsSynced, r.ly.Lines, "", lyrics.CurrentLine(r.ly.Lines, pos)
	}
	return panels.LyricsUnsynced, nil, r.ly.Plain, -1
}
