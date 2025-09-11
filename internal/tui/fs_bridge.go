package tui

import (
	"slinky/internal/fsurls"
)

// fsCollect is a tiny bridge to avoid importing fsurls directly in tui.go
func fsCollect(root string, globs []string) (map[string][]string, error) {
	return fsurls.CollectURLs(root, globs)
}
