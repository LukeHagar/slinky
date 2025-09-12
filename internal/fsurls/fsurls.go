package fsurls

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	ignore "github.com/sabhiram/go-gitignore"
)

// URL patterns from various contexts
var bareURLRegex = regexp.MustCompile(`(?i)\bhttps?://[^\s<>\[\]{}"']+`)
var mdLinkRegex = regexp.MustCompile(`(?is)!?\[[^\]]*\]\((.*?)\)`) // captures (url)
var angleURLRegex = regexp.MustCompile(`(?i)<(https?://[^>\s]+)>`)
var quotedURLRegex = regexp.MustCompile(`(?i)"(https?://[^"\s]+)"|'(https?://[^'\s]+)'`)
var htmlHrefRegex = regexp.MustCompile(`(?i)href\s*=\s*"([^"]+)"|href\s*=\s*'([^']+)'`)
var htmlSrcRegex = regexp.MustCompile(`(?i)src\s*=\s*"([^"]+)"|src\s*=\s*'([^']+)'`)

// Markdown code sections to ignore when extracting autolinks
var mdFencedCodeRegex = regexp.MustCompile("(?s)```[\\s\\S]*?```")
var mdInlineCodeRegex = regexp.MustCompile("`[^`]+`")

// Strict hostname validation: labels 1-63 chars, alnum & hyphen, not start/end hyphen, at least one dot, simple TLD
var hostnameRegex = regexp.MustCompile(`^(?i)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

func isDebugEnv() bool {
	if os.Getenv("SLINKY_DEBUG") == "1" {
		return true
	}
	if strings.EqualFold(os.Getenv("ACTIONS_STEP_DEBUG"), "true") {
		return true
	}
	if os.Getenv("RUNNER_DEBUG") == "1" {
		return true
	}
	return false
}

// CollectURLs walks the directory tree rooted at rootPath and collects URLs found in
// text-based files matching any of the provided glob patterns (doublestar ** supported).
// If globs is empty, all files are considered. Respects .gitignore if present and respectGitignore=true.
// Returns a map from URL -> sorted unique list of file paths that contained it.
func CollectURLs(rootPath string, globs []string, respectGitignore bool) (map[string][]string, error) {
	if strings.TrimSpace(rootPath) == "" {
		rootPath = "."
	}
	cleanRoot := filepath.Clean(rootPath)

	st, _ := os.Stat(cleanRoot)
	isFileRoot := st != nil && !st.IsDir()

	var ign *ignore.GitIgnore
	if !isFileRoot && respectGitignore {
		ign = loadGitIgnore(cleanRoot)
	}

	var patterns []string
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		patterns = append(patterns, g)
	}

	shouldInclude := func(rel string) bool {
		if len(patterns) == 0 {
			return true
		}
		for _, p := range patterns {
			ok, _ := doublestar.PathMatch(p, rel)
			if ok {
				return true
			}
		}
		return false
	}

	urlToFiles := make(map[string]map[string]struct{})

	// 2 MiB max file size to avoid huge/binary files
	const maxSize = 2 * 1024 * 1024

	// Walk the filesystem
	walkFn := func(path string, d os.DirEntry, err error) error {
		if isDebugEnv() {
			fmt.Printf("::debug:: Walking path: %s\n", path)
		}

		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(cleanRoot, path)
		if rerr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			base := filepath.Base(path)
			if base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if ign != nil && ign.MatchesPath(rel) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if info.Size() > maxSize {
			return nil
		}
		if isFileRoot && rel == "." {
			rel = filepath.ToSlash(filepath.Base(path))
		}
		if !shouldInclude(rel) {
			return nil
		}

		// Debug: announce file being parsed; GitHub shows ::debug only in debug runs
		if isDebugEnv() {
			fmt.Printf("::debug:: Scanned File: %s\n", rel)
		}

		f, ferr := os.Open(path)
		if ferr != nil {
			return nil
		}
		defer f.Close()
		br := bufio.NewReader(f)
		// Read up to maxSize bytes
		var b strings.Builder
		read := int64(0)
		for {
			chunk, cerr := br.ReadString('\n')
			b.WriteString(chunk)
			read += int64(len(chunk))
			if cerr == io.EOF || read > maxSize {
				break
			}
			if cerr != nil {
				break
			}
		}
		content := b.String()
		// Skip if likely binary (NUL present)
		if strings.IndexByte(content, '\x00') >= 0 {
			return nil
		}

		candidates := extractCandidates(rel, content)
		if len(candidates) == 0 {
			return nil
		}
		for _, raw := range candidates {
			u := sanitizeURLToken(raw)
			if u == "" {
				continue
			}
			fileSet, ok := urlToFiles[u]
			if !ok {
				fileSet = make(map[string]struct{})
				urlToFiles[u] = fileSet
			}
			fileSet[rel] = struct{}{}
		}
		return nil
	}

	_ = filepath.WalkDir(cleanRoot, walkFn)

	// Convert to sorted slices
	result := make(map[string][]string, len(urlToFiles))
	for u, files := range urlToFiles {
		var list []string
		for fp := range files {
			list = append(list, fp)
		}
		sort.Strings(list)
		result[u] = list
	}
	return result, nil
}

// CollectURLsProgress is like CollectURLs but invokes onFile(relPath) for each included file.
func CollectURLsProgress(rootPath string, globs []string, respectGitignore bool, onFile func(string)) (map[string][]string, error) {
	if strings.TrimSpace(rootPath) == "" {
		rootPath = "."
	}
	cleanRoot := filepath.Clean(rootPath)

	st, _ := os.Stat(cleanRoot)
	isFileRoot := st != nil && !st.IsDir()

	var ign *ignore.GitIgnore
	if !isFileRoot && respectGitignore {
		ign = loadGitIgnore(cleanRoot)
	}

	var patterns []string
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		patterns = append(patterns, g)
	}

	shouldInclude := func(rel string) bool {
		if len(patterns) == 0 {
			return true
		}
		for _, p := range patterns {
			ok, _ := doublestar.PathMatch(p, rel)
			if ok {
				return true
			}
		}
		return false
	}

	urlToFiles := make(map[string]map[string]struct{})

	// 2 MiB max file size to avoid huge/binary files
	const maxSize = 2 * 1024 * 1024

	walkFn := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(cleanRoot, path)
		if rerr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			base := filepath.Base(path)
			if base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if ign != nil && ign.MatchesPath(rel) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if info.Size() > maxSize {
			return nil
		}
		if isFileRoot && rel == "." {
			rel = filepath.ToSlash(filepath.Base(path))
		}
		if !shouldInclude(rel) {
			return nil
		}

		if onFile != nil {
			onFile(rel)
		}

		f, ferr := os.Open(path)
		if ferr != nil {
			return nil
		}
		defer f.Close()
		br := bufio.NewReader(f)
		var b strings.Builder
		read := int64(0)
		for {
			chunk, cerr := br.ReadString('\n')
			b.WriteString(chunk)
			read += int64(len(chunk))
			if cerr == io.EOF || read > maxSize {
				break
			}
			if cerr != nil {
				break
			}
		}
		content := b.String()
		if strings.IndexByte(content, '\x00') >= 0 {
			return nil
		}

		candidates := extractCandidates(rel, content)
		if len(candidates) == 0 {
			return nil
		}
		for _, raw := range candidates {
			u := sanitizeURLToken(raw)
			if u == "" {
				continue
			}
			fileSet, ok := urlToFiles[u]
			if !ok {
				fileSet = make(map[string]struct{})
				urlToFiles[u] = fileSet
			}
			fileSet[rel] = struct{}{}
		}
		return nil
	}

	_ = filepath.WalkDir(cleanRoot, walkFn)

	result := make(map[string][]string, len(urlToFiles))
	for u, files := range urlToFiles {
		var list []string
		for fp := range files {
			list = append(list, fp)
		}
		sort.Strings(list)
		result[u] = list
	}
	return result, nil
}

func sanitizeURLToken(s string) string {
	s = strings.TrimSpace(s)
	// Strip surrounding angle brackets or quotes
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "<"), ">")
	}
	if (strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"")) || (strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'")) {
		s = strings.TrimSuffix(strings.TrimPrefix(s, string(s[0])), string(s[0]))
	}
	// Trim obvious invalid chars at both ends and balance brackets/parentheses
	s = trimDelimiters(s)
	low := strings.ToLower(s)
	if !(strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://")) {
		return ""
	}
	// Parse and validate hostname strictly
	u, err := url.Parse(s)
	if err != nil || u == nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	// Reject placeholders like [tenant] or {tenant}
	if strings.ContainsAny(host, "[]{}") {
		return ""
	}
	// Must match strict hostname rules
	if !hostnameRegex.MatchString(host) {
		return ""
	}
	return s
}

func trimTrailingDelimiters(s string) string {
	for {
		if s == "" {
			return s
		}
		last := s[len(s)-1]
		// Preserve closing brackets/parens if balanced; only strip if unmatched
		switch last {
		case ')':
			open := strings.Count(s, "(")
			close := strings.Count(s, ")")
			if close > open {
				s = s[:len(s)-1]
				continue
			}
		case ']':
			open := strings.Count(s, "[")
			close := strings.Count(s, "]")
			if close > open {
				s = s[:len(s)-1]
				continue
			}
		case '}':
			open := strings.Count(s, "{")
			close := strings.Count(s, "}")
			if close > open {
				s = s[:len(s)-1]
				continue
			}
		case '>':
			open := strings.Count(s, "<")
			close := strings.Count(s, ">")
			if close > open {
				s = s[:len(s)-1]
				continue
			}
		default:
			// Common trailing punctuation and markdown emphasis markers that are not part of URLs
			if strings.ContainsRune(",.;:!?]'\"*_~`", rune(last)) {
				s = s[:len(s)-1]
				continue
			}
		}
		return s
	}
}

func trimLeadingDelimiters(s string) string {
	for {
		if s == "" {
			return s
		}
		first := s[0]
		// Strip common leading punctuation/formatting not valid at URL start
		if strings.ContainsRune("'\"*_~`,;:!?)]}.", rune(first)) {
			s = s[1:]
			continue
		}
		// If starts with unmatched opening bracket, drop it
		switch first {
		case '(':
			open := strings.Count(s, "(")
			close := strings.Count(s, ")")
			if open > close {
				s = s[1:]
				continue
			}
		case '[':
			open := strings.Count(s, "[")
			close := strings.Count(s, "]")
			if open > close {
				s = s[1:]
				continue
			}
		case '{':
			open := strings.Count(s, "{")
			close := strings.Count(s, "}")
			if open > close {
				s = s[1:]
				continue
			}
		case '<':
			open := strings.Count(s, "<")
			close := strings.Count(s, ">")
			if open > close {
				s = s[1:]
				continue
			}
		}
		return s
	}
}

// trimDelimiters trims invalid leading/trailing delimiters until the string stabilizes.
func trimDelimiters(s string) string {
	prev := ""
	for s != prev {
		prev = s
		s = trimLeadingDelimiters(s)
		s = trimTrailingDelimiters(s)
	}
	return s
}

func extractCandidates(rel string, content string) []string {
	var out []string

	lowerRel := strings.ToLower(rel)
	ext := strings.ToLower(filepath.Ext(lowerRel))

	appendFromDual := func(matches [][]string) {
		for _, m := range matches {
			if len(m) > 2 {
				if m[1] != "" {
					out = append(out, m[1])
				} else if m[2] != "" {
					out = append(out, m[2])
				}
			}
		}
	}

	isMarkdown := ext == ".md" || ext == ".markdown" || ext == ".mdx"
	isHTML := ext == ".html" || ext == ".htm" || ext == ".xhtml"

	switch {
	case isMarkdown:
		// Remove fenced and inline code before scanning for URLs
		withoutFences := mdFencedCodeRegex.ReplaceAllString(content, "")
		withoutInline := mdInlineCodeRegex.ReplaceAllString(withoutFences, "")

		for _, m := range mdLinkRegex.FindAllStringSubmatch(withoutInline, -1) {
			if len(m) > 1 {
				out = append(out, m[1])
			}
		}
		for _, m := range angleURLRegex.FindAllStringSubmatch(withoutInline, -1) {
			if len(m) > 1 {
				out = append(out, m[1])
			}
		}
		for _, m := range quotedURLRegex.FindAllStringSubmatch(withoutInline, -1) {
			if len(m) > 2 {
				if m[1] != "" {
					out = append(out, m[1])
				} else if m[2] != "" {
					out = append(out, m[2])
				}
			}
		}
		out = append(out, bareURLRegex.FindAllString(withoutInline, -1)...)

	case isHTML:
		appendFromDual(htmlHrefRegex.FindAllStringSubmatch(content, -1))
		appendFromDual(htmlSrcRegex.FindAllStringSubmatch(content, -1))

	default:
		for _, m := range angleURLRegex.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				out = append(out, m[1])
			}
		}
		for _, m := range quotedURLRegex.FindAllStringSubmatch(content, -1) {
			if len(m) > 2 {
				if m[1] != "" {
					out = append(out, m[1])
				} else if m[2] != "" {
					out = append(out, m[2])
				}
			}
		}
		out = append(out, bareURLRegex.FindAllString(content, -1)...)
	}

	return out
}

func loadGitIgnore(root string) *ignore.GitIgnore {
	var lines []string
	gi := filepath.Join(root, ".gitignore")
	if b, err := os.ReadFile(gi); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			lines = append(lines, ln)
		}
	}
	ge := filepath.Join(root, ".git", "info", "exclude")
	if b, err := os.ReadFile(ge); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			lines = append(lines, ln)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return ignore.CompileIgnoreLines(lines...)
}
