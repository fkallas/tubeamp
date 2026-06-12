// Package store provides a simple file-backed byte cache with expiry.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Cache is a tiny file-backed byte cache where filenames are derived from key hashes.
type Cache struct {
	dir string
}

// NewCache creates a new cache backed by the given directory.
// The directory is not created until the first Put.
func NewCache(dir string) *Cache {
	return &Cache{dir: dir}
}

// Get retrieves a cached value by key, checking expiry against maxAge.
// maxAge <= 0 means no expiry (the value never expires).
// Returns (data, true) on hit, (nil, false) on miss (absent or expired).
func (c *Cache) Get(key string, maxAge time.Duration) ([]byte, bool) {
	path := c.keyPath(key)
	fi, err := os.Stat(path)
	if err != nil {
		// File doesn't exist or can't be stat'd
		return nil, false
	}

	// Check expiry if maxAge > 0
	if maxAge > 0 {
		age := time.Since(fi.ModTime())
		if age > maxAge {
			return nil, false
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	return data, true
}

// Put stores a value in the cache under the given key, writing atomically.
func (c *Cache) Put(key string, data []byte) error {
	// Create directory if needed
	if err := os.MkdirAll(c.dir, 0755); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}

	path := c.keyPath(key)

	// Write atomically: temp file + rename
	tmpFile := path + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("writing cache file: %w", err)
	}
	if err := os.Rename(tmpFile, path); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("moving cache file: %w", err)
	}

	return nil
}

// keyPath derives the file path for a given key using sha256 hex encoding.
func (c *Cache) keyPath(key string) string {
	hash := sha256.Sum256([]byte(key))
	filename := hex.EncodeToString(hash[:]) + ".bin"
	return filepath.Join(c.dir, filename)
}
