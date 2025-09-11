package report

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"slinky/internal/web"
)

// Summary captures high-level run details for the report.
type Summary struct {
	RootPath        string
	StartedAt       time.Time
	FinishedAt      time.Time
	Processed       int
	OK              int
	Fail            int
	AvgRPS          float64
	PeakRPS         float64
	LowRPS          float64
	JSONPath        string
	RepoBlobBaseURL string // e.g. https://github.com/owner/repo/blob/<sha>
}

// WriteMarkdown writes a GitHub-flavored Markdown report to path. If path is empty,
// it derives a safe filename from s.RootPath.
func WriteMarkdown(path string, results []web.Result, s Summary) (string, error) {
	if strings.TrimSpace(path) == "" {
		base := filepath.Base(s.RootPath)
		if strings.TrimSpace(base) == "" || base == "." || base == string(filepath.Separator) {
			base = "results"
		}
		var b strings.Builder
		for _, r := range strings.ToLower(base) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
				b.WriteRune(r)
			} else {
				b.WriteByte('_')
			}
		}
		path = fmt.Sprintf("%s.md", b.String())
	}

	var buf bytes.Buffer
	// Title and summary
	buf.WriteString("## Slinky Test Report\n\n")
	buf.WriteString(fmt.Sprintf("- **Root**: %s\n", escapeMD(s.RootPath)))
	buf.WriteString(fmt.Sprintf("- **Started**: %s\n", s.StartedAt.Format("2006-01-02 15:04:05 MST")))
	buf.WriteString(fmt.Sprintf("- **Finished**: %s\n", s.FinishedAt.Format("2006-01-02 15:04:05 MST")))
	buf.WriteString(fmt.Sprintf("- **Processed**: %d  •  **OK**: %d  •  **Fail**: %d\n", s.Processed, s.OK, s.Fail))
	buf.WriteString(fmt.Sprintf("- **Rates**: avg %.1f/s  •  peak %.1f/s  •  low %.1f/s\n", s.AvgRPS, s.PeakRPS, s.LowRPS))
	if s.JSONPath != "" {
		base := filepath.Base(s.JSONPath)
		buf.WriteString(fmt.Sprintf("- **JSON**: %s\n", escapeMD(base)))
	}
	buf.WriteString("\n")

	// Failures by URL
	buf.WriteString("### Failures by URL\n\n")

	// Gather issues per URL with list of files
	type fileRef struct {
		Path string
	}
	type urlIssue struct {
		Status int
		Method string
		ErrMsg string
		Files  []fileRef
	}
	byURL := make(map[string]*urlIssue)
	for _, r := range results {
		ui, ok := byURL[r.URL]
		if !ok {
			ui = &urlIssue{Status: r.Status, Method: r.Method, ErrMsg: r.ErrMsg}
			byURL[r.URL] = ui
		}
		for _, src := range r.Sources {
			ui.Files = append(ui.Files, fileRef{Path: src})
		}
	}

	// Sort URLs
	var urls []string
	for u := range byURL {
		urls = append(urls, u)
	}
	sort.Strings(urls)

	for _, u := range urls {
		ui := byURL[u]
		// Header line for URL
		if ui.Status > 0 {
			buf.WriteString(fmt.Sprintf("- %d %s `%s` — %s\n", ui.Status, escapeMD(ui.Method), escapeMD(u), escapeMD(ui.ErrMsg)))
		} else {
			buf.WriteString(fmt.Sprintf("- %s `%s` — %s\n", escapeMD(ui.Method), escapeMD(u), escapeMD(ui.ErrMsg)))
		}
		// Files list (collapsible)
		buf.WriteString("  <details><summary>files</summary>\n\n")
		// Deduplicate and sort file paths
		seen := make(map[string]struct{})
		var files []string
		for _, fr := range ui.Files {
			if _, ok := seen[fr.Path]; ok {
				continue
			}
			seen[fr.Path] = struct{}{}
			files = append(files, fr.Path)
		}
		sort.Strings(files)
		for _, fn := range files {
			if strings.TrimSpace(s.RepoBlobBaseURL) != "" {
				buf.WriteString(fmt.Sprintf("  - [%s](%s/%s)\n", escapeMD(fn), strings.TrimRight(s.RepoBlobBaseURL, "/"), escapeLinkPath(fn)))
			} else {
				buf.WriteString(fmt.Sprintf("  - [%s](./%s)\n", escapeMD(fn), escapeLinkPath(fn)))
			}
		}
		buf.WriteString("\n  </details>\n\n")
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(buf.Bytes()); err != nil {
		return "", err
	}
	return path, nil
}

func escapeMD(s string) string {
	// Basic HTML escape to be safe in GitHub Markdown table cells
	return html.EscapeString(s)
}

// formatSourcesList renders a list of file paths as an HTML unordered list suitable
// for inclusion in a Markdown table cell. Individual entries are escaped.
func formatSourcesList(srcs []string) string {
	if len(srcs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<ul>\n")
	for _, s := range srcs {
		b.WriteString("  <li><code>")
		b.WriteString(escapeMD(s))
		b.WriteString("</code></li>\n")
	}
	b.WriteString("</ul>")
	return b.String()
}

// escapeLinkPath escapes a relative path for inclusion in a Markdown link URL.
// We keep it simple and only escape parentheses and spaces.
func escapeLinkPath(p string) string {
	// Replace spaces with %20 and parentheses with encoded forms
	p = strings.ReplaceAll(p, " ", "%20")
	p = strings.ReplaceAll(p, "(", "%28")
	p = strings.ReplaceAll(p, ")", "%29")
	return p
}
