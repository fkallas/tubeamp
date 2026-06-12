package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewCache(t *testing.T) {
	// Use a subdirectory that doesn't exist yet
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "cache")
	cache := NewCache(cacheDir)
	if cache == nil {
		t.Fatal("NewCache returned nil")
	}
	if cache.dir != cacheDir {
		t.Errorf("NewCache dir: got %q, want %q", cache.dir, cacheDir)
	}
	// Directory should not be created yet
	if _, err := os.Stat(cacheDir); err == nil || !os.IsNotExist(err) {
		t.Error("NewCache should not create directory yet")
	}
}

func TestPutCreatesDir(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "cache")
	cache := NewCache(cacheDir)

	// Put should create the directory
	err := cache.Put("test-key", []byte("test-data"))
	if err != nil {
		t.Fatalf("Put(): %v", err)
	}

	// Directory should exist now
	fi, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatalf("cache dir stat: %v", err)
	}
	if !fi.IsDir() {
		t.Error("cache path is not a directory")
	}
	if fi.Mode()&0755 != 0755 {
		t.Errorf("cache dir perms: got %o, want 0755", fi.Mode().Perm())
	}
}

func TestPutAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	cache := NewCache(tmpDir)

	key := "test-key"
	data := []byte("test-data")

	// Put
	if err := cache.Put(key, data); err != nil {
		t.Fatalf("Put(): %v", err)
	}

	// Get with no expiry (maxAge <= 0)
	got, ok := cache.Get(key, 0)
	if !ok {
		t.Fatal("Get(): expected hit, got miss")
	}
	if string(got) != string(data) {
		t.Errorf("Get(): got %q, want %q", got, data)
	}
}

func TestGetMissingKey(t *testing.T) {
	tmpDir := t.TempDir()
	cache := NewCache(tmpDir)

	got, ok := cache.Get("nonexistent", 0)
	if ok {
		t.Fatal("Get() nonexistent: expected miss, got hit")
	}
	if got != nil {
		t.Errorf("Get() nonexistent: got data %v, want nil", got)
	}
}

func TestGetExpiry(t *testing.T) {
	tmpDir := t.TempDir()
	cache := NewCache(tmpDir)

	key := "test-key"
	data := []byte("test-data")

	// Put
	if err := cache.Put(key, data); err != nil {
		t.Fatalf("Put(): %v", err)
	}

	// Get without expiry check (maxAge <= 0)
	got, ok := cache.Get(key, 0)
	if !ok {
		t.Fatal("Get(maxAge=0): expected hit, got miss")
	}
	if string(got) != string(data) {
		t.Errorf("Get(maxAge=0): got %q, want %q", got, data)
	}

	// Get with very short expiry
	got, ok = cache.Get(key, 1*time.Nanosecond)
	if ok {
		t.Fatal("Get(very short maxAge): expected miss, got hit")
	}

	// Get with long expiry
	got, ok = cache.Get(key, 1*time.Hour)
	if !ok {
		t.Fatal("Get(long maxAge): expected hit, got miss")
	}
	if string(got) != string(data) {
		t.Errorf("Get(long maxAge): got %q, want %q", got, data)
	}
}

func TestGetExpiredFile(t *testing.T) {
	tmpDir := t.TempDir()
	cache := NewCache(tmpDir)

	key := "test-key"
	data := []byte("test-data")

	// Put
	if err := cache.Put(key, data); err != nil {
		t.Fatalf("Put(): %v", err)
	}

	// Backdate the file to 2 seconds ago
	path := cache.keyPath(key)
	oldTime := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(): %v", err)
	}

	// Get with 1 second maxAge should expire
	got, ok := cache.Get(key, 1*time.Second)
	if ok {
		t.Fatal("Get(expired): expected miss, got hit")
	}
	if got != nil {
		t.Errorf("Get(expired): got data %v, want nil", got)
	}

	// Get with 3 second maxAge should not expire
	got, ok = cache.Get(key, 3*time.Second)
	if !ok {
		t.Fatal("Get(not expired): expected hit, got miss")
	}
	if string(got) != string(data) {
		t.Errorf("Get(not expired): got %q, want %q", got, data)
	}
}

func TestMultipleKeys(t *testing.T) {
	tmpDir := t.TempDir()
	cache := NewCache(tmpDir)

	tests := []struct {
		key  string
		data []byte
	}{
		{"key1", []byte("data1")},
		{"key2", []byte("data2")},
		{"another-key", []byte("another-data")},
	}

	// Put all
	for _, tt := range tests {
		if err := cache.Put(tt.key, tt.data); err != nil {
			t.Fatalf("Put(%q): %v", tt.key, err)
		}
	}

	// Get all
	for _, tt := range tests {
		got, ok := cache.Get(tt.key, 0)
		if !ok {
			t.Fatalf("Get(%q): expected hit, got miss", tt.key)
		}
		if string(got) != string(tt.data) {
			t.Errorf("Get(%q): got %q, want %q", tt.key, got, tt.data)
		}
	}
}

func TestFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	cache := NewCache(tmpDir)

	key := "test-key"
	data := []byte("test-data")

	if err := cache.Put(key, data); err != nil {
		t.Fatalf("Put(): %v", err)
	}

	path := cache.keyPath(key)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(): %v", err)
	}

	if fi.Mode()&0644 != 0644 {
		t.Errorf("file perms: got %o, want 0644", fi.Mode().Perm())
	}
}

func TestKeyPathHashing(t *testing.T) {
	tmpDir := t.TempDir()
	cache := NewCache(tmpDir)

	key1 := "test-key"
	key2 := "test-key"
	key3 := "different-key"

	path1 := cache.keyPath(key1)
	path2 := cache.keyPath(key2)
	path3 := cache.keyPath(key3)

	// Same key should produce same path
	if path1 != path2 {
		t.Errorf("Same key different paths: %q vs %q", path1, path2)
	}

	// Different keys should produce different paths
	if path1 == path3 {
		t.Errorf("Different keys same path: %q", path1)
	}

	// Path should end with .bin
	if !strings.HasSuffix(path1, ".bin") {
		t.Errorf("Path doesn't end with .bin: %q", path1)
	}
}

func TestAtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	cache := NewCache(tmpDir)

	key := "test-key"
	data := []byte("test-data")

	if err := cache.Put(key, data); err != nil {
		t.Fatalf("Put(): %v", err)
	}

	path := cache.keyPath(key)

	// Check that temp file doesn't exist
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); err == nil || !os.IsNotExist(err) {
		t.Error("Temp file should not exist after Put()")
	}

	// Check that actual file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Actual file doesn't exist: %v", err)
	}
}

func TestUpdateExisting(t *testing.T) {
	tmpDir := t.TempDir()
	cache := NewCache(tmpDir)

	key := "test-key"
	data1 := []byte("original-data")
	data2 := []byte("updated-data")

	// Put original
	if err := cache.Put(key, data1); err != nil {
		t.Fatalf("Put(1): %v", err)
	}

	// Verify
	got, ok := cache.Get(key, 0)
	if !ok || string(got) != string(data1) {
		t.Errorf("Get(1): got %q, want %q", got, data1)
	}

	// Update
	if err := cache.Put(key, data2); err != nil {
		t.Fatalf("Put(2): %v", err)
	}

	// Verify update
	got, ok = cache.Get(key, 0)
	if !ok || string(got) != string(data2) {
		t.Errorf("Get(2): got %q, want %q", got, data2)
	}
}

func TestNegativeMaxAge(t *testing.T) {
	tmpDir := t.TempDir()
	cache := NewCache(tmpDir)

	key := "test-key"
	data := []byte("test-data")

	if err := cache.Put(key, data); err != nil {
		t.Fatalf("Put(): %v", err)
	}

	// Negative maxAge should mean no expiry
	got, ok := cache.Get(key, -1*time.Second)
	if !ok {
		t.Fatal("Get(negative maxAge): expected hit, got miss")
	}
	if string(got) != string(data) {
		t.Errorf("Get(negative maxAge): got %q, want %q", got, data)
	}
}

func TestEmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	cache := NewCache(tmpDir)

	key := "empty-key"
	data := []byte{}

	// Put empty data
	if err := cache.Put(key, data); err != nil {
		t.Fatalf("Put(empty): %v", err)
	}

	// Get should return empty slice, not nil
	got, ok := cache.Get(key, 0)
	if !ok {
		t.Fatal("Get(empty): expected hit, got miss")
	}
	if got == nil || len(got) != 0 {
		t.Errorf("Get(empty): got %v (nil=%v), want empty slice", got, got == nil)
	}
}
