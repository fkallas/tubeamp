package ytm

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestSearch_live performs a real network call to YouTube Music (unauthenticated).
// Skipped unless TUBEAMP_LIVE=1.
func TestSearch_live(t *testing.T) {
	if os.Getenv("TUBEAMP_LIVE") != "1" {
		t.Skip("set TUBEAMP_LIVE=1 to run live network tests")
	}

	c := NewClient(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tracks, err := c.Search(ctx, "never gonna give you up")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(tracks) == 0 {
		t.Fatal("expected >= 1 result, got 0")
	}
	t.Logf("live search returned %d tracks; first: %q by %v", len(tracks), tracks[0].Title, tracks[0].Artists)
}
