package panels

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/theme"
)

// artCols / artRows are the fixed cell dimensions of the player-bar cover art.
const (
	artCols = 8
	artRows = 4
	artGap  = 2 // columns between art and text
)

// PlayerState is the immutable snapshot the player bar renders.
type PlayerState struct {
	Track    model.Track
	HasTrack bool
	Paused   bool
	Muted    bool
	Volume   int
	Elapsed  float64 // seconds
	Total    float64 // seconds
}

// PlayerBar renders the full-width player bar: an 8×4 cover-art block on the
// left and four text lines (title, artists/album, progress, transport state).
// artBlock is a pre-rendered art string (placeholder or real cover); when empty
// the art area is blank.
func PlayerBar(th *theme.Theme, st PlayerState, artBlock string, w int) string {
	innerW := w - 2
	textW := innerW - artCols - artGap
	if textW < 1 {
		textW = 1
	}

	art := artRowsOf(artBlock)

	var lines [artRows]string
	if st.HasTrack {
		lines[0] = th.PlayingStyle().Render(Clip(st.Track.Title, textW))
		sub := st.Track.ArtistLine()
		if st.Track.Album != "" {
			if sub != "" {
				sub += "  —  "
			}
			sub += st.Track.Album
		}
		lines[1] = th.Muted().Render(Clip(sub, textW))
		lines[2] = progressLine(th, st.Elapsed, st.Total, textW)
		lines[3] = stateLine(th, st, textW)
	} else {
		lines[0] = th.Muted().Render("nothing playing")
	}

	body := make([]string, artRows)
	for i := 0; i < artRows; i++ {
		body[i] = art[i] + strings.Repeat(" ", artGap) + lines[i]
	}
	return Box(th, "Player", body, w, artRows+2, false)
}

// artRowsOf splits a rendered art block into exactly artRows lines, each padded
// to artCols columns (blank lines when the block is empty or short).
func artRowsOf(s string) []string {
	out := make([]string, artRows)
	var lines []string
	if s != "" {
		lines = strings.Split(s, "\n")
	}
	for i := 0; i < artRows; i++ {
		if i < len(lines) {
			out[i] = PadPlain(lines[i], artCols)
		} else {
			out[i] = strings.Repeat(" ", artCols)
		}
	}
	return out
}

// progressLine renders "m:ss ━━●──── m:ss": elapsed time, a position bar with a
// dot at the playhead (Accent before it, Muted after), and total time.
func progressLine(th *theme.Theme, elapsed, total float64, w int) string {
	el := FormatSeconds(elapsed)
	tot := FormatSeconds(total)
	barW := w - lipgloss.Width(el) - lipgloss.Width(tot) - 2
	if barW < 1 {
		return th.Muted().Render(Clip(el+" / "+tot, w))
	}

	pos := 0
	if total > 0 {
		pos = int(float64(barW-1) * (elapsed / total))
	}
	if pos < 0 {
		pos = 0
	}
	if pos > barW-1 {
		pos = barW - 1
	}

	var b strings.Builder
	for i := 0; i < barW; i++ {
		switch {
		case i < pos:
			b.WriteString(th.AccentStyle().Render("━"))
		case i == pos:
			b.WriteString(th.AccentStyle().Render("●"))
		default:
			b.WriteString(th.Muted().Render("━"))
		}
	}
	return th.Muted().Render(el) + " " + b.String() + " " + th.Muted().Render(tot)
}

// stateLine renders the transport glyph and volume readout.
func stateLine(th *theme.Theme, st PlayerState, w int) string {
	glyph := "▶"
	if st.Paused {
		glyph = "⏸"
	}
	vol := fmt.Sprintf("vol %d%%", st.Volume)
	if st.Muted {
		vol = "muted"
	}
	return th.Primary().Render(glyph) + "  " + th.Muted().Render(Clip(vol, w-3))
}

// FormatSeconds renders a duration in seconds as "m:ss".
func FormatSeconds(sec float64) string {
	if sec < 0 || math.IsNaN(sec) || math.IsInf(sec, 0) {
		sec = 0
	}
	s := int(sec)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
