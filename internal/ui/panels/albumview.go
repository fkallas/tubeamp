package panels

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/theme"
)

// AlbumCoverCols / AlbumCoverRows are the fixed cell dimensions of the large
// album-view cover. The art pipeline renders cols×(2·rows) pixels, so a 16×8
// block carries a 16×16-pixel cover. Callers fetching/rendering the cover MUST
// use these dimensions so the cached render lines up with the layout here.
const (
	AlbumCoverCols = 16
	AlbumCoverRows = 8
	albumCoverGap  = 2 // columns between the cover and the metadata block
)

// AlbumView renders the album detail view: a header row with a large pixel-art
// cover on the left and the album title (accent), artists, and year + track
// count (muted) to its right, then the album's track list (number, title,
// duration — no album column). title is the panel's border title; a supplies the
// header metadata; cover is the pre-rendered AlbumCoverCols×AlbumCoverRows art
// block (placeholder while loading). The track at playingVideoID (if any) is
// marked with a note.
func AlbumView(th *theme.Theme, title string, a model.Album, tracks []model.Track, cursor int, cover, playingVideoID string, w, h int, focused bool) string {
	innerW := w - 2
	body := make([]string, 0, h-2)

	// Header: cover on the left, metadata on the right.
	coverRows := coverLines(cover)
	metaW := innerW - AlbumCoverCols - albumCoverGap
	if metaW < 0 {
		metaW = 0
	}
	meta := albumMeta(th, a, len(tracks), metaW)
	for i := 0; i < AlbumCoverRows; i++ {
		body = append(body, coverRows[i]+strings.Repeat(" ", albumCoverGap)+meta[i])
	}
	body = append(body, "") // blank separator before the track list.

	// Track list fills whatever vertical space is left under the header.
	visible := (h - 2) - AlbumCoverRows - 1
	if visible > 0 && len(tracks) > 0 {
		rest := innerW - 4 // minus the cursor (2) and marker (2) columns.
		if rest < 0 {
			rest = 0
		}
		lay := albumTrackLayout(tracks, rest)
		start, end := visibleWindow(cursor, len(tracks), visible)
		for i := start; i < end; i++ {
			t := tracks[i]
			plain, styled := lay.row(th, i+1, t)
			body = append(body, renderListRow(th, plain, innerW, rowOpts{
				selected:    i == cursor,
				focused:     focused,
				playing:     playingVideoID != "" && t.VideoID == playingVideoID,
				showMarker:  true,
				styledField: styled,
			}))
		}
	}

	return Box(th, title, body, w, h, focused)
}

// coverLines splits a rendered cover block into exactly AlbumCoverRows lines,
// each padded to AlbumCoverCols columns (blank lines when the block is empty or
// short).
func coverLines(s string) []string {
	out := make([]string, AlbumCoverRows)
	var lines []string
	if s != "" {
		lines = strings.Split(s, "\n")
	}
	for i := 0; i < AlbumCoverRows; i++ {
		if i < len(lines) {
			out[i] = PadPlain(lines[i], AlbumCoverCols)
		} else {
			out[i] = strings.Repeat(" ", AlbumCoverCols)
		}
	}
	return out
}

// albumMeta builds the AlbumCoverRows-tall metadata block shown right of the
// cover: title (accent), artists (primary), year + track count (muted), each
// exactly w columns wide so the header rows align.
func albumMeta(th *theme.Theme, a model.Album, count, w int) []string {
	out := make([]string, AlbumCoverRows)
	for i := range out {
		out[i] = strings.Repeat(" ", maxInt(w, 0))
	}
	if w <= 0 {
		return out
	}
	out[0] = PadPlain(th.AccentStyle().Render(Clip(a.Title, w)), w)
	if AlbumCoverRows > 1 {
		out[1] = PadPlain(th.Primary().Render(Clip(strings.Join(a.Artists, ", "), w)), w)
	}
	if AlbumCoverRows > 2 {
		out[2] = PadPlain(th.Muted().Render(Clip(albumSubtitle(a, count), w)), w)
	}
	return out
}

// albumSubtitle renders the muted "year · N tracks" line under the album title.
func albumSubtitle(a model.Album, count int) string {
	var parts []string
	if a.Year != "" {
		parts = append(parts, a.Year)
	}
	unit := "tracks"
	if count == 1 {
		unit = "track"
	}
	parts = append(parts, fmt.Sprintf("%d %s", count, unit))
	return strings.Join(parts, " · ")
}

// albumCols holds the column widths of an album-view track row. A width of 0
// hides that column.
type albumCols struct {
	num, title, dur int
}

// albumTrackLayout computes the album track-row column widths for a block of
// rows that together must fit in rest display columns. The number column is
// sized to the largest track number; duration auto-sizes to the widest value.
func albumTrackLayout(rows []model.Track, rest int) albumCols {
	durW := 0
	for _, t := range rows {
		if dw := lipgloss.Width(fmtDuration(t.Duration)); dw > durW {
			durW = dw
		}
	}
	numW := len(strconv.Itoa(len(rows)))
	if numW < 1 {
		numW = 1
	}
	text := rest - numW - 1 // number cell plus its trailing gap.
	if durW > 0 {
		text -= durW + 1 // duration cell plus its leading gap.
	}
	if text < 0 {
		text = 0
	}
	return albumCols{num: numW, title: text, dur: durW}
}

// row renders one album track to its plain (exactly rest-wide) form and a
// parallel styled form: number muted, title primary, duration muted.
func (c albumCols) row(th *theme.Theme, num int, t model.Track) (plain, styled string) {
	numCell := padLeft(strconv.Itoa(num), c.num)
	plain = numCell
	styled = th.Muted().Render(numCell)

	titleCell := PadPlain(Clip(t.Title, c.title), c.title)
	plain += " " + titleCell
	styled += " " + th.Primary().Render(titleCell)

	if c.dur > 0 {
		durCell := padLeft(fmtDuration(t.Duration), c.dur)
		plain += " " + durCell
		styled += " " + th.Muted().Render(durCell)
	}
	return plain, styled
}

// maxInt returns the larger of a and b.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
