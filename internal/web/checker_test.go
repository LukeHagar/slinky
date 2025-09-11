package web

import (
	"context"
	"testing"
	"time"
)

// This test exercises CheckURLs with a mix of known-good and invalid URLs.
// It does real network calls; keep timeouts short to avoid long CI runs.
func TestCheckURLs_Basic(t *testing.T) {
	urls := []string{
		"https://example.com",                              // should be OK
		"https://en.wikipedia.org/wiki/Main_Page",          // should be OK
		"http://example..com",                              // invalid hostname
		"https://this-domain-does-not-exist-123456789.com", // NXDOMAIN/nonexistent
	}

	sources := map[string][]string{
		"https://example.com":                              {"test files/test2.txt"},
		"https://en.wikipedia.org/wiki/Main_Page":          {"test files/test5.html"},
		"http://example..com":                              {"test files/test5.html"},
		"https://this-domain-does-not-exist-123456789.com": {"test files/test5.html"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan Result, 16)
	cfg := Config{MaxConcurrency: 8, RequestTimeout: 5 * time.Second}

	go CheckURLs(ctx, urls, sources, out, nil, cfg)

	seen := 0
	var okCount, failCount int
	for r := range out {
		seen++
		if r.OK {
			okCount++
		} else {
			failCount++
		}
	}

	if seen != len(urls) {
		t.Fatalf("expected %d results, got %d", len(urls), seen)
	}
	if okCount == 0 {
		t.Fatalf("expected at least one OK result")
	}
	if failCount == 0 {
		t.Fatalf("expected at least one failure result")
	}
}
