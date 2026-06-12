package ytm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestTrackDetails_next drives the InnerTube `next` enrichment flow against an
// httptest server serving the handcrafted track-details fixture.
func TestTrackDetails_next(t *testing.T) {
	nextRaw, err := os.ReadFile("parse/testdata/trackdetails_next.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/next") {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(nextRaw)
	}))
	defer srv.Close()

	c := NewClient(nil)
	c.baseURL = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	album, albumID, dur, err := c.TrackDetails(ctx, "fJ9rUzIMcZQ")
	if err != nil {
		t.Fatalf("TrackDetails: %v", err)
	}
	if !strings.Contains(gotBody, "fJ9rUzIMcZQ") {
		t.Errorf("next payload did not carry the videoId: %s", gotBody)
	}
	if album != "A Night at the Opera" || albumID != "MPREb_nightAtTheOpera" {
		t.Errorf("album = %q / %q, want A Night at the Opera / MPREb_nightAtTheOpera", album, albumID)
	}
	if dur != 5*time.Minute+55*time.Second {
		t.Errorf("dur = %v, want 5:55", dur)
	}
}

func TestTrackDetails_httpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":500}}`))
	}))
	defer srv.Close()

	c := NewClient(nil)
	c.baseURL = srv.URL
	if _, _, _, err := c.TrackDetails(context.Background(), "vid"); err == nil {
		t.Fatal("TrackDetails: want error on HTTP 500")
	}
}
