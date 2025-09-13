package fsurls

import (
	"bufio"
	"encoding/json"
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
	// Load optional .slinkignore config
	slPathIgnore, slURLPatterns := loadSlinkyIgnore(cleanRoot)

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
		// if isDebugEnv() {
		// 	fmt.Printf("::debug:: Walking path: %s\n", path)
		// }

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
		if (ign != nil && ign.MatchesPath(rel)) || (slPathIgnore != nil && slPathIgnore.MatchesPath(rel)) {
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

		matches := extractCandidateMatches(content)
		if len(matches) == 0 {
			return nil
		}
		for _, m := range matches {
			u := sanitizeURLToken(m.URL)
			if u == "" {
				continue
			}
			if isURLIgnored(u, slURLPatterns) {
				continue
			}
			line, col := computeLineCol(content, m.Offset)
			source := fmt.Sprintf("%s|%d|%d", rel, line, col)
			fileSet, ok := urlToFiles[u]
			if !ok {
				fileSet = make(map[string]struct{})
				urlToFiles[u] = fileSet
			}
			fileSet[source] = struct{}{}
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
	slPathIgnore, slURLPatterns := loadSlinkyIgnore(cleanRoot)

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
		if (ign != nil && ign.MatchesPath(rel)) || (slPathIgnore != nil && slPathIgnore.MatchesPath(rel)) {
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

		matches := extractCandidateMatches(content)
		if len(matches) == 0 {
			return nil
		}
		for _, m := range matches {
			u := sanitizeURLToken(m.URL)
			if u == "" {
				continue
			}
			if isURLIgnored(u, slURLPatterns) {
				continue
			}
			line, col := computeLineCol(content, m.Offset)
			source := fmt.Sprintf("%s|%d|%d", rel, line, col)
			fileSet, ok := urlToFiles[u]
			if !ok {
				fileSet = make(map[string]struct{})
				urlToFiles[u] = fileSet
			}
			fileSet[source] = struct{}{}
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

// matchCandidate holds a URL and its byte offset within the content
type matchCandidate struct {
	URL    string
	Offset int
}

// computeLineCol returns 1-based line and column given a byte offset
func computeLineCol(content string, offset int) (int, int) {
	if offset < 0 {
		return 1, 1
	}
	if offset > len(content) {
		offset = len(content)
	}
	line := 1
	col := 1
	for i := 0; i < offset; i++ {
		if content[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// extractCandidateMatches finds URL-like tokens with their offsets for line/col mapping
func extractCandidateMatches(content string) []matchCandidate {
	var out []matchCandidate
	// Markdown links: capture group 1 is the URL inside (...)
	if subs := mdLinkRegex.FindAllStringSubmatchIndex(content, -1); len(subs) > 0 {
		for _, idx := range subs {
			if len(idx) >= 4 && idx[2] >= 0 && idx[3] >= 0 {
				url := content[idx[2]:idx[3]]
				out = append(out, matchCandidate{URL: url, Offset: idx[2]})
			}
		}
	}
	// HTML href
	if subs := htmlHrefRegex.FindAllStringSubmatchIndex(content, -1); len(subs) > 0 {
		for _, idx := range subs {
			// groups 1 and 2 are alternatives
			if len(idx) >= 4 && idx[2] >= 0 && idx[3] >= 0 {
				url := content[idx[2]:idx[3]]
				out = append(out, matchCandidate{URL: url, Offset: idx[2]})
			} else if len(idx) >= 6 && idx[4] >= 0 && idx[5] >= 0 {
				url := content[idx[4]:idx[5]]
				out = append(out, matchCandidate{URL: url, Offset: idx[4]})
			}
		}
	}
	// HTML src
	if subs := htmlSrcRegex.FindAllStringSubmatchIndex(content, -1); len(subs) > 0 {
		for _, idx := range subs {
			if len(idx) >= 4 && idx[2] >= 0 && idx[3] >= 0 {
				url := content[idx[2]:idx[3]]
				out = append(out, matchCandidate{URL: url, Offset: idx[2]})
			} else if len(idx) >= 6 && idx[4] >= 0 && idx[5] >= 0 {
				url := content[idx[4]:idx[5]]
				out = append(out, matchCandidate{URL: url, Offset: idx[4]})
			}
		}
	}
	// Angle autolinks <http://...>
	if subs := angleURLRegex.FindAllStringSubmatchIndex(content, -1); len(subs) > 0 {
		for _, idx := range subs {
			if len(idx) >= 4 && idx[2] >= 0 && idx[3] >= 0 {
				url := content[idx[2]:idx[3]]
				out = append(out, matchCandidate{URL: url, Offset: idx[2]})
			}
		}
	}
	// Quoted URLs
	if subs := quotedURLRegex.FindAllStringSubmatchIndex(content, -1); len(subs) > 0 {
		for _, idx := range subs {
			if len(idx) >= 4 && idx[2] >= 0 && idx[3] >= 0 {
				url := content[idx[2]:idx[3]]
				out = append(out, matchCandidate{URL: url, Offset: idx[2]})
			} else if len(idx) >= 6 && idx[4] >= 0 && idx[5] >= 0 {
				url := content[idx[4]:idx[5]]
				out = append(out, matchCandidate{URL: url, Offset: idx[4]})
			}
		}
	}
	// Bare URLs
	if spans := bareURLRegex.FindAllStringIndex(content, -1); len(spans) > 0 {
		for _, sp := range spans {
			url := content[sp[0]:sp[1]]
			out = append(out, matchCandidate{URL: url, Offset: sp[0]})
		}
	}
	return out
}

func loadGitIgnore(root string) *ignore.GitIgnore {
	var lines []string
	gi := filepath.Join(root, ".gitignore")
	if isDebugEnv() {
		fmt.Printf("::debug:: Checking for .gitignore at: %s\n", gi)
	}
	if _, err := os.Stat(gi); err != nil {
		if isDebugEnv() {
			fmt.Printf("::debug:: .gitignore not found at: %s\n", gi)
		}
		return nil
	}
	if isDebugEnv() {
		fmt.Printf("::debug:: Reading .gitignore from: %s\n", gi)
	}
	if b, err := os.ReadFile(gi); err == nil {
		for ln := range strings.SplitSeq(string(b), "\n") {
			lines = append(lines, ln)
		}
	}
	ge := filepath.Join(root, ".git", "info", "exclude")
	if isDebugEnv() {
		fmt.Printf("::debug:: Checking for .git/info/exclude at: %s\n", ge)
	}
	if _, err := os.Stat(ge); err != nil {
		if isDebugEnv() {
			fmt.Printf("::debug:: .git/info/exclude not found at: %s\n", ge)
		}
		return nil
	}
	if isDebugEnv() {
		fmt.Printf("::debug:: Reading .git/info/exclude from: %s\n", ge)
	}
	if b, err := os.ReadFile(ge); err == nil {
		for ln := range strings.SplitSeq(string(b), "\n") {
			lines = append(lines, ln)
		}
	}
	if len(lines) == 0 {
		if isDebugEnv() {
			fmt.Printf("::debug:: .gitignore or .git/info/exclude is empty\n")
		}
		return nil
	}
	if isDebugEnv() {
		fmt.Printf("::debug:: Compiling .gitignore and .git/info/exclude\n")
	}
	return ignore.CompileIgnoreLines(lines...)
}

// .slinkignore support
type slinkyIgnore struct {
	IgnorePaths []string `json:"ignorePaths" optional:"true"`
	IgnoreURLs  []string `json:"ignoreURLs" optional:"true"`
}

func loadSlinkyIgnore(root string) (*ignore.GitIgnore, []string) {
	cfgPath := findSlinkyConfig(root)
	if cfgPath == "" {
		return nil, nil
	}
	b, err := os.ReadFile(cfgPath)
	if err != nil || len(b) == 0 {
		return nil, nil
	}
	var cfg slinkyIgnore
	// First attempt strict JSON
	if jerr := json.Unmarshal(b, &cfg); jerr != nil {
		// Try a relaxed pass: strip trailing commas before ] or }
		relaxed := regexp.MustCompile(`,\s*([}\]])`).ReplaceAll(b, []byte("$1"))
		if jerr2 := json.Unmarshal(relaxed, &cfg); jerr2 != nil {
			// Emit a GitHub Actions warning so users see misconfigurations
			fmt.Printf("::warning:: Failed to parse .slinkignore at %s: %v\n", cfgPath, jerr)
			return nil, nil
		}
	}
	if isDebugEnv() {
		fmt.Println("::debug:: Loaded .slinkignore")
		fmt.Printf("::debug:: IgnorePaths: %v\n", cfg.IgnorePaths)
		fmt.Printf("::debug:: IgnoreURLs: %v\n", cfg.IgnoreURLs)
	}
	var ign *ignore.GitIgnore
	if len(cfg.IgnorePaths) > 0 {
		var lines []string
		for _, p := range cfg.IgnorePaths {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			lines = append(lines, p)
			// Add a recursive variant to match anywhere
			if !strings.HasPrefix(p, "**/") {
				lines = append(lines, "**/"+p)
			}
			// If likely a directory name, add a catch-all under it
			base := strings.TrimSuffix(p, "/")
			if base != "" && !strings.ContainsAny(base, "*?[]") {
				// Heuristic: directory-like if it has no '.' in the last segment or explicitly ends with '/'
				last := filepath.Base(base)
				if strings.HasSuffix(p, "/") || !strings.Contains(last, ".") {
					lines = append(lines, "**/"+base+"/**")
				}
			}
		}
		ign = ignore.CompileIgnoreLines(lines...)
	}
	var urlPatterns []string
	for _, p := range cfg.IgnoreURLs {
		p = strings.TrimSpace(p)
		if p != "" {
			urlPatterns = append(urlPatterns, p)
		}
	}
	return ign, urlPatterns
}

// findSlinkyConfig searches upward from root for a .slinkignore file
func findSlinkyConfig(root string) string {
	cur := root
	for {
		cfg := filepath.Join(cur, ".slinkignore")
		if st, err := os.Stat(cfg); err == nil && !st.IsDir() {
			return cfg
		}
		parent := filepath.Dir(cur)
		if parent == cur || strings.TrimSpace(parent) == "" {
			break
		}
		cur = parent
	}
	return ""
}

func isURLIgnored(u string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	for _, raw := range patterns {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		// No wildcards: exact or substring match
		if !strings.ContainsAny(p, "*?") {
			if u == p || strings.Contains(u, p) {
				return true
			}
			continue
		}
		// Glob-style: allow '*' to span slashes by converting '*' -> '**'
		dsPat := strings.ReplaceAll(p, "*", "**")
		if ok, _ := doublestar.PathMatch(dsPat, u); ok {
			return true
		}
		// Regex fallback: '*' -> '.*', '?' -> '.'
		if re, err := wildcardToRegex(p); err == nil && re.MatchString(u) {
			return true
		}
	}
	return false
}

func wildcardToRegex(pattern string) (*regexp.Regexp, error) {
	escaped := regexp.QuoteMeta(pattern)
	escaped = strings.ReplaceAll(escaped, "\\*", ".*")
	escaped = strings.ReplaceAll(escaped, "\\?", ".")
	return regexp.Compile("^" + escaped + "$")
}
