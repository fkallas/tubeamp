package ytm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---- sapisidHash tests ----

func TestSapisidHash(t *testing.T) {
	// Pre-computed vector:
	// echo -n '1700000000 abc123_xyz https://music.youtube.com' | shasum -a 1
	// => f891507e4dfb08c5d4d41f37cb3996062c5e2260
	ts := time.Unix(1700000000, 0)
	got := sapisidHash("abc123_xyz", "https://music.youtube.com", ts)
	want := "SAPISIDHASH 1700000000_f891507e4dfb08c5d4d41f37cb3996062c5e2260"
	if got != want {
		t.Errorf("sapisidHash = %q, want %q", got, want)
	}
}

// ---- SAPISID cookie parsing tests ----

func TestSAPISID_direct(t *testing.T) {
	a := &Auth{Cookie: "FOO=bar; SAPISID=my_sapisid_value; BAZ=qux"}
	got, err := a.SAPISID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "my_sapisid_value" {
		t.Errorf("SAPISID() = %q, want %q", got, "my_sapisid_value")
	}
}

func TestSAPISID_fallback(t *testing.T) {
	// No SAPISID, only __Secure-3PAPISID → should fall back.
	a := &Auth{Cookie: "FOO=bar; __Secure-3PAPISID=fallback_value; BAZ=qux"}
	got, err := a.SAPISID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fallback_value" {
		t.Errorf("SAPISID() = %q, want %q", got, "fallback_value")
	}
}

func TestSAPISID_preferSAPISID(t *testing.T) {
	// Both present — SAPISID wins.
	a := &Auth{Cookie: "__Secure-3PAPISID=fallback_value; SAPISID=primary_value"}
	got, err := a.SAPISID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "primary_value" {
		t.Errorf("SAPISID() = %q, want %q", got, "primary_value")
	}
}

func TestSAPISID_missing(t *testing.T) {
	a := &Auth{Cookie: "FOO=bar; BAZ=qux"}
	_, err := a.SAPISID()
	if err == nil {
		t.Fatal("expected error for missing SAPISID, got nil")
	}
}

// ---- LoadAuth tests ----

func TestLoadAuth_ok(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth")
	if err := os.WriteFile(path, []byte("  SID=abc; SAPISID=xyz  \n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := LoadAuth(path)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if a.Cookie != "SID=abc; SAPISID=xyz" {
		t.Errorf("Cookie = %q, want trimmed value", a.Cookie)
	}
}

func TestLoadAuth_missing(t *testing.T) {
	_, err := LoadAuth(filepath.Join(t.TempDir(), "nonexistent"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadAuth_empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth")
	if err := os.WriteFile(path, []byte("   \n  "), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadAuth(path)
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}
