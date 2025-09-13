package fsurls

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectURLs_FromTestFiles(t *testing.T) {
	root := filepath.Join("..", "..", "testdata")

	urls, err := CollectURLs(root, []string{"**/*"}, true)
	if err != nil {
		t.Fatalf("CollectURLs error: %v", err)
	}

	// Spot-check presence of some known URLs
	mustContain := []string{
		"https://example.com",
		"https://en.wikipedia.org/wiki/Main_Page",
		"http://example.com:8080",
		"https://this-domain-does-not-exist-123456789.com",
	}
	for _, u := range mustContain {
		if _, ok := urls[u]; !ok {
			// Show nearby URLs to aid debugging if it fails.
			var sample []string
			for seen := range urls {
				if strings.Contains(seen, "example") {
					sample = append(sample, seen)
				}
			}
			t.Fatalf("expected URL %q to be collected; example URLs seen: %v", u, sample)
		}
	}

	// Ensure sources are recorded for a known URL
	srcs := urls["https://example.com"]
	if len(srcs) == 0 {
		t.Fatalf("expected sources for https://example.com, got none")
	}

	// Verify .slinkignore URL ignores
	if _, ok := urls["https://example.com/this/path/does/not/exist"]; ok {
		t.Fatalf("expected URL ignored by .slinkignore to be absent")
	}
	// Ignore sailpoint api variants
	ignoredAPIs := []string{
		"https://sailpoint.api.identitynow.com/beta",
		"https://sailpoint.api.identitynow.com/v3",
		"https://sailpoint.api.identitynow.com/v2024",
		"https://sailpoint.api.identitynow.com/v2025",
		"https://sailpoint.api.identitynow.com/v2026",
	}
	for _, u := range ignoredAPIs {
		if _, ok := urls[u]; ok {
			t.Fatalf("expected API URL %s to be ignored via .slinkignore", u)
		}
	}
	// URLs matching *acme* should be ignored
	acmeSamples := []string{
		"https://acme.com/logout",
		"http://sub.acme.example/logout",
		"https://docs.acme.dev",
	}
	for _, u := range acmeSamples {
		if _, ok := urls[u]; ok {
			t.Fatalf("expected %s to be ignored via *acme* pattern", u)
		}
	}

	// Verify .slinkignore path ignores: file under ignore-me should not contribute
	for u, files := range urls {
		for _, f := range files {
			if strings.Contains(f, "ignore-me/") || strings.Contains(f, "node_modules/") || strings.HasSuffix(f, "package-lock.json") {
				t.Fatalf("file %s should have been ignored via .slinkignore, but contributed to URL %s", f, u)
			}
		}
	}
}
