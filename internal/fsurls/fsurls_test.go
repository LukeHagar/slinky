package fsurls

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectURLs_FromTestFiles(t *testing.T) {
	root := filepath.Join("..", "..", "test files")

	urls, err := CollectURLs(root, []string{"**/*"})
	if err != nil {
		t.Fatalf("CollectURLs error: %v", err)
	}

	// Spot-check presence of some known URLs
	mustContain := []string{
		"https://example.com",
		"https://en.wikipedia.org/wiki/Main_Page",
		"http://example.com:8080",
		"http://example..com", // appears in multiple files
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
}
