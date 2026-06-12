package ytm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLyrics_twoCallFlow exercises the next -> browse InnerTube flow end to end
// against an httptest server that serves the handcrafted next/browse fixtures.
func TestLyrics_twoCallFlow(t *testing.T) {
	nextRaw, err := os.ReadFile("parse/testdata/lyrics_next.json")
	if err != nil {
		t.Fatalf("read next fixture: %v", err)
	}
	browseRaw, err := os.ReadFile("parse/testdata/lyrics_browse.json")
	if err != nil {
		t.Fatalf("read browse fixture: %v", err)
	}

	var gotBrowseBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		switch {
		case strings.HasSuffix(r.URL.Path, "/next"):
			_, _ = w.Write(nextRaw)
		case strings.HasSuffix(r.URL.Path, "/browse"):
			b := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(b)
			gotBrowseBody = string(b)
			_, _ = w.Write(browseRaw)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(nil)
	c.baseURL = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	text, err := c.Lyrics(ctx, "dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("Lyrics: %v", err)
	}
	if !strings.HasPrefix(text, "I been tryin' to do it right") {
		t.Errorf("lyrics = %q, want the fixture text", text)
	}
	// The browse call must carry the lyrics browseId discovered from `next`.
	if !strings.Contains(gotBrowseBody, "MPLYt_abc123def456") {
		t.Errorf("browse payload did not carry the lyrics browseId: %s", gotBrowseBody)
	}
}

// TestLyrics_noLyricsTab confirms a missing lyrics tab surfaces ErrNoLyrics.
func TestLyrics_noLyricsTab(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// `next` with only an Up-next tab => no lyrics tab.
		_, _ = w.Write([]byte(`{"tabs":[{"tabRenderer":{"title":"Up next","endpoint":{}}}]}`))
	}))
	defer srv.Close()

	c := NewClient(nil)
	c.baseURL = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Lyrics(ctx, "vid"); !errors.Is(err, ErrNoLyrics) {
		t.Errorf("err = %v, want ErrNoLyrics", err)
	}
}
