package fsurls

import (
	"path/filepath"
	"testing"
)

func TestCollectURLs_FromCodeFiles(t *testing.T) {
	root := filepath.Join("..", "..", "testdata")
	urls, err := CollectURLs(root, []string{"**/*"}, true)
	if err != nil {
		t.Fatalf("CollectURLs error: %v", err)
	}

	// Valid URLs from various languages should be present (including a known nonexistent-but-well-formed)
	valids := []string{
		"https://example.com",
		"https://en.wikipedia.org/wiki/Main_Page",
		"https://developer.mozilla.org",
		"https://svelte.dev",
		"https://go.dev/doc/",
		"https://this-domain-does-not-exist-123456789.com",
	}
	for _, u := range valids {
		if _, ok := urls[u]; !ok {
			t.Fatalf("expected valid URL %q to be collected", u)
		}
	}

	// Ensure sanitizer trims emphasis and punctuation
	if _, ok := urls["https://sailpoint.api.identitynow.com/v2024"]; !ok {
		t.Fatalf("expected sanitized emphasized URL to be collected without trailing *")
	}
	if _, ok := urls["https://example.com/path"]; !ok {
		t.Fatalf("expected URL with trailing ) to be trimmed")
	}
	if _, ok := urls["https://example.com/foo"]; !ok {
		t.Fatalf("expected URL with trailing , to be trimmed")
	}

	// Balanced parens should be preserved
	if _, ok := urls["https://example.com/q?(x)"]; !ok {
		t.Fatalf("expected URL with balanced parentheses to be preserved")
	}

	// Placeholder patterns should be excluded by strict validation
	placeholders := []string{
		"https://[tenant].api.identitynow.com",
		"https://{tenant}.api.identitynow.com",
		"https://[tenant].[domain].com",
		"https://{tenant}.api.ideidentitynow.com/v3/transforms",
	}
	for _, u := range placeholders {
		if _, ok := urls[u]; ok {
			t.Fatalf("did not expect placeholder URL %q to be collected", u)
		}
	}
}
