package parse

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestLyricsBrowseID_fixture(t *testing.T) {
	data, err := os.ReadFile("testdata/lyrics_next.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	id, err := LyricsBrowseID(data)
	if err != nil {
		t.Fatalf("LyricsBrowseID: %v", err)
	}
	if id != "MPLYt_abc123def456" {
		t.Errorf("browseId = %q, want %q", id, "MPLYt_abc123def456")
	}
}

func TestLyricsBrowseID_byPrefixWhenTitleDiffers(t *testing.T) {
	// No "Lyrics" title, but an MPLYt browseId is still recognised.
	raw := []byte(`{"x":{"tabRenderer":{"title":"Letras","endpoint":{"browseEndpoint":{"browseId":"MPLYtwhatever"}}}}}`)
	id, err := LyricsBrowseID(raw)
	if err != nil {
		t.Fatalf("LyricsBrowseID: %v", err)
	}
	if id != "MPLYtwhatever" {
		t.Errorf("browseId = %q, want MPLYtwhatever", id)
	}
}

func TestLyricsBrowseID_absent(t *testing.T) {
	// A watch response with only an "Up next" tab (no lyrics tab).
	raw := []byte(`{"tabs":[{"tabRenderer":{"title":"Up next","endpoint":{"browseEndpoint":{"browseId":"FEmusic_x"}}}}]}`)
	if _, err := LyricsBrowseID(raw); !errors.Is(err, ErrNoLyrics) {
		t.Errorf("err = %v, want ErrNoLyrics", err)
	}
}

func TestLyricsBrowseID_invalidJSON(t *testing.T) {
	_, err := LyricsBrowseID([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if errors.Is(err, ErrNoLyrics) {
		t.Error("invalid JSON must not surface as ErrNoLyrics")
	}
}

func TestLyricsText_fixture(t *testing.T) {
	data, err := os.ReadFile("testdata/lyrics_browse.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	text, err := LyricsText(data)
	if err != nil {
		t.Fatalf("LyricsText: %v", err)
	}
	if !strings.HasPrefix(text, "I been tryin' to do it right") {
		t.Errorf("text = %q, want it to start with the first lyric line", text)
	}
	if !strings.Contains(text, "\n") {
		t.Error("expected multi-line lyrics joined with newlines")
	}
}

func TestLyricsText_simpleText(t *testing.T) {
	raw := []byte(`{"musicDescriptionShelfRenderer":{"description":{"simpleText":"plain words"}}}`)
	text, err := LyricsText(raw)
	if err != nil {
		t.Fatalf("LyricsText: %v", err)
	}
	if text != "plain words" {
		t.Errorf("text = %q, want %q", text, "plain words")
	}
}

func TestLyricsText_absent(t *testing.T) {
	raw := []byte(`{"contents":{"messageRenderer":{"text":{"runs":[{"text":"Lyrics not available"}]}}}}`)
	if _, err := LyricsText(raw); !errors.Is(err, ErrNoLyrics) {
		t.Errorf("err = %v, want ErrNoLyrics", err)
	}
}

func TestLyricsText_invalidJSON(t *testing.T) {
	if _, err := LyricsText([]byte(`{`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
