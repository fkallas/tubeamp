package panels

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/theme"
)

// albumTrack is a single mock track whose album name ("Nevermind") is short
// enough to survive truncation in the album column / suffix.
func albumTrack() model.Track {
	return model.Track{
		VideoID:  "vid",
		Title:    "Smells Like Teen Spirit",
		Artists:  []string{"Nirvana"},
		Album:    "Nevermind",
		Duration: 5 * time.Minute,
	}
}

// TestMainViewAlbumColumn verifies the ALBUM column appears in the track table
// when the panel is wide (>= albumMinMainWidth) and is dropped below it.
func TestMainViewAlbumColumn(t *testing.T) {
	th := theme.Default()
	tracks := []model.Track{albumTrack()}

	wide := ansi.Strip(MainView(th, "Liked", tracks, 0, "", 100, 8, false))
	if !strings.Contains(wide, "Nevermind") {
		t.Errorf("wide main view should show the album column; \"Nevermind\" not found in:\n%s", wide)
	}

	narrow := ansi.Strip(MainView(th, "Liked", tracks, 0, "", 60, 8, false))
	if strings.Contains(narrow, "Nevermind") {
		t.Errorf("narrow main view should hide the album column; \"Nevermind\" unexpectedly present:\n%s", narrow)
	}
	if !strings.Contains(narrow, "Smells Like Teen Spirit") {
		t.Errorf("narrow main view dropped the track row; title missing:\n%s", narrow)
	}
}

// TestQueueAlbumSuffix verifies the muted album suffix is appended in a wide
// queue panel and omitted in a narrow one.
func TestQueueAlbumSuffix(t *testing.T) {
	th := theme.Default()
	tracks := []model.Track{albumTrack()}

	wide := ansi.Strip(Queue(th, tracks, 0, -1, 40, 6, false))
	if !strings.Contains(wide, "Nevermind") {
		t.Errorf("wide queue should append the album suffix; \"Nevermind\" not found in:\n%s", wide)
	}

	narrow := ansi.Strip(Queue(th, tracks, 0, -1, 30, 6, false))
	if strings.Contains(narrow, "Nevermind") {
		t.Errorf("narrow queue should not append the album suffix; \"Nevermind\" unexpectedly present:\n%s", narrow)
	}
	// The narrow queue is unchanged (title clipped, artist right-flushed); the
	// row is still present.
	if !strings.Contains(narrow, "Smells Like") {
		t.Errorf("narrow queue dropped the track row; title missing:\n%s", narrow)
	}
}

// TestSearchViewSections verifies the search view renders both the "Songs" and
// "Albums" section headers along with a song row and an album row.
func TestSearchViewSections(t *testing.T) {
	th := theme.Default()
	tracks := []model.Track{albumTrack()}
	albums := []model.Album{{BrowseID: "MPRE_x", Title: "Some Record", Artists: []string{"A Band"}, Year: "2019"}}

	v := ansi.Strip(SearchView(th, "Search", tracks, albums, 0, "", 100, 14, true))
	for _, want := range []string{"Songs", "Albums", "Smells Like Teen Spirit", "Some Record", "2019", "▤"} {
		if !strings.Contains(v, want) {
			t.Errorf("SearchView missing %q in:\n%s", want, v)
		}
	}
}

// TestAlbumViewHeaderAndTracks verifies the album view renders the album title,
// year, and a track row from its track list.
func TestAlbumViewHeaderAndTracks(t *testing.T) {
	th := theme.Default()
	a := model.Album{BrowseID: "MPRE_y", Title: "Great Album", Artists: []string{"The Band"}, Year: "2008"}
	tracks := []model.Track{albumTrack()}

	v := ansi.Strip(AlbumView(th, "4 Great Album", a, tracks, 0, "", "", 100, 18, true))
	for _, want := range []string{"Great Album", "2008", "Smells Like Teen Spirit"} {
		if !strings.Contains(v, want) {
			t.Errorf("AlbumView missing %q in:\n%s", want, v)
		}
	}
}
