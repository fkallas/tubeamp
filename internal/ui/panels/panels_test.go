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
