package panels

import (
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/theme"
)

// albumMinMainWidth is the smallest main-view panel width (outer columns) that
// still shows the ALBUM column. Below it the album is dropped so TITLE and
// ARTIST keep their room.
const albumMinMainWidth = 80

// MainView renders the contextual main panel as a track table with TITLE,
// ARTIST, ALBUM and duration columns. Its border title is the current context
// (e.g. "Liked Songs", a playlist name, or a search query). The track at
// playingVideoID (if non-empty) is marked with a music note. The ALBUM column
// (muted) is hidden when the panel is narrower than albumMinMainWidth; when
// space is tight columns truncate in priority order TITLE > ARTIST > ALBUM.
func MainView(th *theme.Theme, title string, tracks []model.Track, cursor int, playingVideoID string, w, h int, focused bool) string {
	innerW := w - 2
	visible := h - 2

	if len(tracks) == 0 {
		body := []string{th.Muted().Render("  nothing here")}
		return Box(th, title, body, w, h, focused)
	}

	// rest = inner width minus cursor (2) and marker (2) columns.
	rest := innerW - 4
	if rest < 0 {
		rest = 0
	}

	start, end := visibleWindow(cursor, len(tracks), visible)
	lay := trackLayout(tracks[start:end], rest, w >= albumMinMainWidth)

	body := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		t := tracks[i]
		plain, styled := lay.row(th, t)
		body = append(body, renderListRow(th, plain, innerW, rowOpts{
			selected:    i == cursor,
			focused:     focused,
			playing:     playingVideoID != "" && t.VideoID == playingVideoID,
			showMarker:  true,
			styledField: styled,
		}))
	}
	return Box(th, title, body, w, h, focused)
}

// trackCols holds the fixed column widths of a track table, shared by every row
// so the columns line up. A width of 0 hides that column entirely.
type trackCols struct {
	title, artist, album, dur int
}

// trackLayout computes the column widths for a block of rows that together must
// fit in rest display columns. showAlbum drops the album column when false; the
// duration column auto-sizes to the widest visible duration.
func trackLayout(rows []model.Track, rest int, showAlbum bool) trackCols {
	durW := 0
	for _, t := range rows {
		if dw := lipgloss.Width(fmtDuration(t.Duration)); dw > durW {
			durW = dw
		}
	}

	text := rest // columns available to TITLE/ARTIST/ALBUM (excludes duration).
	if durW > 0 {
		text -= durW + 1 // duration cell plus its leading gap.
	}
	if text < 0 {
		text = 0
	}

	c := trackCols{dur: durW}
	if showAlbum {
		c.artist = clampInt(text/4, 6, 28)
		c.album = clampInt(text/4, 6, 24)
		c.title = text - c.artist - c.album - 2 // two single-column gaps.
	} else {
		c.artist = clampInt(text/3, 6, 30)
		c.title = text - c.artist - 1 // one gap.
	}
	// Degrade gracefully if the panel is too narrow to honour the minimums:
	// drop the album, then the artist, keeping TITLE last (highest priority).
	if c.title < 1 {
		c.album = 0
		c.artist = clampInt(text/3, 0, 30)
		c.title = text - c.artist
		if c.artist > 0 {
			c.title--
		}
	}
	if c.title < 1 {
		c.artist = 0
		c.title = text
	}
	if c.title < 0 {
		c.title = 0
	}
	return c
}

// row renders one track to its plain (unstyled, exactly rest-wide) form and a
// parallel styled form with the album column in Muted and the rest in Primary.
func (c trackCols) row(th *theme.Theme, t model.Track) (plain, styled string) {
	titleCell := PadPlain(Clip(t.Title, c.title), c.title)
	primary := titleCell

	if c.artist > 0 {
		artistCell := PadPlain(Clip(t.ArtistLine(), c.artist), c.artist)
		primary += " " + artistCell
	}

	plain = primary
	styled = th.Primary().Render(primary)

	if c.album > 0 {
		albumCell := PadPlain(Clip(t.Album, c.album), c.album)
		plain += " " + albumCell
		styled += " " + th.Muted().Render(albumCell)
	}

	if c.dur > 0 {
		durCell := padLeft(fmtDuration(t.Duration), c.dur)
		plain += " " + durCell
		styled += th.Primary().Render(" " + durCell)
	}
	return plain, styled
}

// fmtDuration renders a track duration as "m:ss".
func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return FormatSeconds(d.Seconds())
}

// clampInt returns v constrained to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
