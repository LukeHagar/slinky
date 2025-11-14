package web

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// CacheEntry represents a cached URL check result
type CacheEntry struct {
	URL     string    `json:"url"`
	OK      bool      `json:"ok"`
	Status  int       `json:"status"`
	ErrMsg  string    `json:"error,omitempty"`
	Checked time.Time `json:"checked"`
}

// URLCache manages URL result caching
type URLCache struct {
	entries map[string]CacheEntry
	ttl     time.Duration
	path    string
}

// NewURLCache creates a new URL cache with optional file path
func NewURLCache(cachePath string, ttlHours int) *URLCache {
	ttl := time.Duration(ttlHours) * time.Hour
	if ttl <= 0 {
		ttl = 24 * time.Hour // Default 24 hours
	}
	return &URLCache{
		entries: make(map[string]CacheEntry),
		ttl:     ttl,
		path:    cachePath,
	}
}

// Load loads cache entries from file if path is set
func (c *URLCache) Load() error {
	if c.path == "" {
		return nil // No cache file specified
	}
	
	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Cache file doesn't exist yet, that's OK
		}
		return fmt.Errorf("failed to read cache file: %w", err)
	}
	
	var entries []CacheEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		// Non-critical: if cache is corrupted, start fresh
		return nil
	}
	
	now := time.Now()
	c.entries = make(map[string]CacheEntry, len(entries))
	for _, entry := range entries {
		// Only load entries that haven't expired
		if now.Sub(entry.Checked) < c.ttl {
			c.entries[entry.URL] = entry
		}
	}
	
	return nil
}

// Get retrieves a cached result for a URL
func (c *URLCache) Get(url string) (CacheEntry, bool) {
	entry, ok := c.entries[url]
	if !ok {
		return CacheEntry{}, false
	}
	
	// Check if entry has expired
	if time.Since(entry.Checked) >= c.ttl {
		delete(c.entries, url)
		return CacheEntry{}, false
	}
	
	return entry, true
}

// Set stores a result in the cache
func (c *URLCache) Set(url string, ok bool, status int, errMsg string) {
	c.entries[url] = CacheEntry{
		URL:     url,
		OK:      ok,
		Status:  status,
		ErrMsg:  errMsg,
		Checked: time.Now(),
	}
}

// Save saves cache entries to file if path is set
func (c *URLCache) Save() error {
	if c.path == "" {
		return nil // No cache file specified
	}
	
	// Convert map to slice for JSON serialization
	entries := make([]CacheEntry, 0, len(c.entries))
	for _, entry := range c.entries {
		entries = append(entries, entry)
	}
	
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}
	
	// Write to temp file first, then rename (atomic write)
	tmpPath := c.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}
	
	if err := os.Rename(tmpPath, c.path); err != nil {
		return fmt.Errorf("failed to rename cache file: %w", err)
	}
	
	return nil
}

// Clear removes all entries from the cache
func (c *URLCache) Clear() {
	c.entries = make(map[string]CacheEntry)
}

