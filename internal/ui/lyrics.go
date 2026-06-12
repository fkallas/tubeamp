// Package ui — lyrics panel logic: the per-track fetch (LRCLIB with a YouTube
// Music plain-text fallback), the videoID-keyed cache that keeps replays and
// seeks from refetching, the layout/visibility helpers the View uses, and the
// focused-panel scroll behaviour.
//
// The panel is shown only while a track is playing (m.hasNow) and the terminal
// is tall enough to fit it without starving the main view (see lyricsBandHeight).
// It is FOCUSABLE only while visible (focusLyrics, reachable via the h/l cycle —
// no number key). When unfocused it is pure display: synced auto-follows the
// live line (driven off m.timePos via lyrics.CurrentLine, no extra event wiring)
// and unsynced shows from the top. When focused, j/k + ctrl+u/d + g/G scroll it:
// unsynced moves a plain top-line offset; synced PEEK-scrolls, temporarily
// detaching auto-follow (see scrollLyrics). A detached peek re-engages on esc or
// after lyricsFollowResumeSec of idle playback (maybeResumeLyrics, piggybacked
// on EvTimePos — no extra ticker).
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

// lyricsFollowResumeSec is how long (in playback seconds) a synced lyrics peek
// stays detached with no further scroll before auto-follow re-engages. The idle
// timer piggybacks on the frequent EvTimePos events (no extra ticker): each
// scroll arms a resume deadline at timePos+lyricsFollowResumeSec, and a later
// EvTimePos past it re-engages follow (see maybeResumeLyrics).
const lyricsFollowResumeSec = 5.0

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

// currentLyric returns the cached lyrics result for the now-playing track.
func (m Model) currentLyric() (lyricResult, bool) {
	if !m.hasNow {
		return lyricResult{}, false
	}
	r, ok := m.lyricsCache[m.nowPlaying.VideoID]
	return r, ok
}

// lyricsVisibleRows is the number of lyric content rows currently shown (the
// lyrics box height minus its two border rows), or 0 when the panel is hidden.
func (m Model) lyricsVisibleRows() int {
	h := m.lyricsBandHeight(m.topAreaHeight())
	if h < 2 {
		return 0
	}
	return h - 2
}

// lyricsVisible reports whether the lyrics panel is currently on screen, matching
// the View's layout math (a track is playing and the top area is tall enough to
// fit the band without starving the main view). It gates focusLyrics: the panel
// is focusable only while visible.
func (m Model) lyricsVisible() bool {
	if m.width < minWidth || m.height < minHeight {
		return false
	}
	return m.lyricsBandHeight(m.topAreaHeight()) > 0
}

// lyricsMaxScroll clamps the unsynced scroll offset so the last line never
// scrolls above the top of the window.
func (m Model) lyricsMaxScroll(plain string) int {
	total := len(strings.Split(plain, "\n"))
	if mx := total - m.lyricsVisibleRows(); mx > 0 {
		return mx
	}
	return 0
}

// scrollLyrics applies a relative scroll to the focused lyrics panel. Unsynced:
// it moves the plain top-line offset, clamped to content. Synced: it PEEK-scrolls
// — detaching auto-follow (initialising the peek at the live line the first
// time) and (re)arming the auto-follow resume deadline. A no-op when the panel
// has no scrollable lyrics (loading / none).
func (m *Model) scrollLyrics(delta int) {
	r, ok := m.currentLyric()
	if !ok || r.status != lyricResolved || !r.found {
		return
	}
	if r.ly.Synced {
		m.beginPeek(r.ly.Lines)
		m.lyricsScroll = clamp(m.lyricsScroll+delta, 0, len(r.ly.Lines)-1)
	} else {
		m.lyricsScroll = clamp(m.lyricsScroll+delta, 0, m.lyricsMaxScroll(r.ly.Plain))
	}
}

// scrollLyricsEdge jumps the focused lyrics panel to the top (g) or bottom (G).
// Synced jumps detach auto-follow exactly like a relative peek-scroll.
func (m *Model) scrollLyricsEdge(top bool) {
	r, ok := m.currentLyric()
	if !ok || r.status != lyricResolved || !r.found {
		return
	}
	switch {
	case r.ly.Synced:
		m.beginPeek(r.ly.Lines)
		if top {
			m.lyricsScroll = 0
		} else {
			m.lyricsScroll = len(r.ly.Lines) - 1
		}
	case top:
		m.lyricsScroll = 0
	default:
		m.lyricsScroll = m.lyricsMaxScroll(r.ly.Plain)
	}
}

// beginPeek detaches synced auto-follow (initialising the peek index at the live
// line the first time) and (re)arms the auto-follow resume deadline at the
// current playback position + lyricsFollowResumeSec.
func (m *Model) beginPeek(lines []lyrics.Line) {
	if !m.lyricsDetached {
		pos := time.Duration(m.timePos * float64(time.Second))
		if cur := lyrics.CurrentLine(lines, pos); cur > 0 {
			m.lyricsScroll = cur
		} else {
			m.lyricsScroll = 0
		}
		m.lyricsDetached = true
	}
	m.lyricsResumeAt = m.timePos + lyricsFollowResumeSec
}

// reengageLyrics clears any synced peek-scroll detachment, snapping the panel
// back to auto-follow at the live line and resetting the scroll offset. It also
// resets the unsynced scroll offset (re-engaged on track change / leaving focus).
func (m *Model) reengageLyrics() {
	m.lyricsDetached = false
	m.lyricsScroll = 0
}

// maybeResumeLyrics re-engages synced auto-follow once a detached peek has been
// idle (no further scroll) for lyricsFollowResumeSec of playback. Called on each
// EvTimePos so no extra ticker is needed; detached is only ever set while
// focusLyrics is active.
func (m *Model) maybeResumeLyrics() {
	if m.lyricsDetached && m.timePos >= m.lyricsResumeAt {
		m.reengageLyrics()
	}
}
