package lyrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// withServer points the package baseURL at a test server for the duration of a
// test (restored afterward). Tests must not run in parallel while it is set.
func withServer(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	prev := baseURL
	baseURL = srv.URL
	t.Cleanup(func() {
		baseURL = prev
		srv.Close()
	})
}

func TestFetchLRCLIB_synced(t *testing.T) {
	var gotPath, gotQuery, gotUA string
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotUA = r.URL.Path, r.URL.RawQuery, r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"syncedLyrics":"[00:01.00]one\n[00:02.00]two","plainLyrics":"one\ntwo"}`))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ly, err := FetchLRCLIB(ctx, "Artist", "Title", "Album", 200*time.Second)
	if err != nil {
		t.Fatalf("FetchLRCLIB: %v", err)
	}
	if !ly.Synced {
		t.Error("Synced = false, want true for syncedLyrics")
	}
	if len(ly.Lines) != 2 {
		t.Fatalf("len(Lines) = %d, want 2", len(ly.Lines))
	}
	if ly.Lines[0].At != time.Second || ly.Lines[0].Text != "one" {
		t.Errorf("Lines[0] = %+v, want {1s one}", ly.Lines[0])
	}
	if ly.Plain != "one\ntwo" {
		t.Errorf("Plain = %q, want the plain text alongside synced", ly.Plain)
	}
	if ly.Source != SourceLRCLIB {
		t.Errorf("Source = %q, want %q", ly.Source, SourceLRCLIB)
	}
	if gotPath != "/api/get" {
		t.Errorf("path = %q, want /api/get", gotPath)
	}
	if !strings.Contains(gotQuery, "duration=200") {
		t.Errorf("query = %q, want duration=200", gotQuery)
	}
	if !strings.Contains(gotQuery, "album_name=Album") {
		t.Errorf("query = %q, want album_name=Album", gotQuery)
	}
	if !strings.Contains(gotUA, "tubeamp") {
		t.Errorf("User-Agent = %q, want it to identify tubeamp", gotUA)
	}
}

func TestFetchLRCLIB_plainOnly(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"syncedLyrics":"","plainLyrics":"just plain words\nsecond line"}`))
	})

	ly, err := FetchLRCLIB(context.Background(), "A", "T", "", 0)
	if err != nil {
		t.Fatalf("FetchLRCLIB: %v", err)
	}
	if ly.Synced {
		t.Error("Synced = true, want false for plain-only")
	}
	if len(ly.Lines) != 0 {
		t.Errorf("Lines = %v, want none for plain-only", ly.Lines)
	}
	if ly.Plain != "just plain words\nsecond line" {
		t.Errorf("Plain = %q", ly.Plain)
	}
	if ly.Source != SourceLRCLIB {
		t.Errorf("Source = %q, want %q", ly.Source, SourceLRCLIB)
	}
}

func TestFetchLRCLIB_404ThenSearch(t *testing.T) {
	var paths []string
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/get":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"name":"TrackNotFound"}`))
		case "/api/search":
			w.WriteHeader(http.StatusOK)
			// First hit has only plain; second hit has synced — synced wins.
			_, _ = w.Write([]byte(`[
				{"syncedLyrics":"","plainLyrics":"plain from first"},
				{"syncedLyrics":"[00:03.00]synced line","plainLyrics":"synced fallback"}
			]`))
		default:
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	})

	ly, err := FetchLRCLIB(context.Background(), "Artist Name", "Song", "", 0)
	if err != nil {
		t.Fatalf("FetchLRCLIB: %v", err)
	}
	if !ly.Synced || len(ly.Lines) != 1 || ly.Lines[0].Text != "synced line" {
		t.Errorf("got %+v, want a synced result with one line", ly)
	}
	if len(paths) != 2 || paths[0] != "/api/get" || paths[1] != "/api/search" {
		t.Errorf("request paths = %v, want [/api/get /api/search]", paths)
	}
}

func TestFetchLRCLIB_searchPlainFallback(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/get" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"syncedLyrics":"","plainLyrics":"only plain here"}]`))
	})

	ly, err := FetchLRCLIB(context.Background(), "A", "T", "", 0)
	if err != nil {
		t.Fatalf("FetchLRCLIB: %v", err)
	}
	if ly.Synced || ly.Plain != "only plain here" {
		t.Errorf("got %+v, want plain fallback from search", ly)
	}
}

func TestFetchLRCLIB_noMatch(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/get" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`)) // empty search results
	})

	_, err := FetchLRCLIB(context.Background(), "Nobody", "Nothing", "", 0)
	if !errors.Is(err, ErrNoLyrics) {
		t.Errorf("err = %v, want ErrNoLyrics", err)
	}
}

func TestFetchLRCLIB_getEmptyIsNoLyrics(t *testing.T) {
	// A 200 with an instrumental/empty body yields ErrNoLyrics (no search hop).
	var calls int
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"instrumental":true,"syncedLyrics":"","plainLyrics":""}`))
	})

	_, err := FetchLRCLIB(context.Background(), "A", "T", "", 0)
	if !errors.Is(err, ErrNoLyrics) {
		t.Errorf("err = %v, want ErrNoLyrics", err)
	}
	if calls != 1 {
		t.Errorf("server calls = %d, want 1 (no search fallback after a 200)", calls)
	}
}

func TestFetchLRCLIB_serverError(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := FetchLRCLIB(context.Background(), "A", "T", "", 0)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if errors.Is(err, ErrNoLyrics) {
		t.Error("a 500 must not be reported as ErrNoLyrics")
	}
}

// TestFetchLRCLIB_liveSkipped is a placeholder for a real-network check, gated
// off by default so the suite never hits lrclib.net.
func TestFetchLRCLIB_liveSkipped(t *testing.T) {
	t.Skip("live LRCLIB test disabled; set TUBEAMP_LIVE=1 and write a real query to enable")
}
