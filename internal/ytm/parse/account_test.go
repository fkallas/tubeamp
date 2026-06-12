package parse

import (
	"os"
	"testing"
)

func TestAccountInfo_signedIn(t *testing.T) {
	data, err := os.ReadFile("testdata/account_signed_in.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	name, signedIn, err := AccountInfo(data)
	if err != nil {
		t.Fatalf("AccountInfo: %v", err)
	}
	if !signedIn {
		t.Errorf("signedIn = false, want true (activeAccountHeaderRenderer present)")
	}
	if name != "Felipe Kallas" {
		t.Errorf("name = %q, want %q", name, "Felipe Kallas")
	}
}

func TestAccountInfo_loggedOut(t *testing.T) {
	data, err := os.ReadFile("testdata/account_logged_out.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	name, signedIn, err := AccountInfo(data)
	if err != nil {
		t.Fatalf("AccountInfo on logged-out menu must not error: %v", err)
	}
	if signedIn {
		t.Errorf("signedIn = true, want false (no activeAccountHeaderRenderer)")
	}
	if name != "" {
		t.Errorf("name = %q, want empty for the logged-out menu", name)
	}
}

func TestAccountInfo_empty(t *testing.T) {
	// An empty object has no account header => logged-out result, no error.
	name, signedIn, err := AccountInfo([]byte(`{}`))
	if err != nil {
		t.Fatalf("AccountInfo on empty object: %v", err)
	}
	if signedIn || name != "" {
		t.Errorf("empty object: got (%q, %v), want (\"\", false)", name, signedIn)
	}
}

func TestAccountInfo_invalidJSON(t *testing.T) {
	if _, _, err := AccountInfo([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
