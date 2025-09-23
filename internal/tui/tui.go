package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"

	"slinky/internal/report"
	"slinky/internal/web"
)

type linkResultMsg struct{ res web.Result }
type crawlDoneMsg struct{}
type statsMsg struct{ s web.Stats }
type tickMsg struct{ t time.Time }

type fileScannedMsg struct{ rel string }
type fileChangedMsg struct{ path string }
type watchErrorMsg struct{ err error }

type model struct {
	rootPath  string
	cfg       web.Config
	jsonOut   string
	mdOut     string
	globs     []string
	watchMode bool

	results    chan web.Result
	stats      chan web.Stats
	started    time.Time
	finishedAt time.Time
	done       bool

	spin spinner.Model
	prog progress.Model
	vp   viewport.Model

	lines []string

	total int
	ok    int
	fail  int

	pending       int
	processed     int
	lastProcessed int
	rps           float64
	peakRPS       float64
	lowRPS        float64

	filesScanned int

	allResults []web.Result
	jsonPath   string
	mdPath     string

	showFail bool

	// Context for canceling scans
	scanCtx    context.Context
	scanCancel context.CancelFunc

	// Flag to prevent file counting after scan is done
	scanDone bool

	// Channel to signal when scan is completely done
	scanComplete chan struct{}
}

// Run scans files under rootPath matching globs, extracts URLs, and checks them.
func Run(rootPath string, globs []string, cfg web.Config, jsonOut string, mdOut string, watchMode bool) error {
	m := &model{rootPath: rootPath, cfg: cfg, jsonOut: jsonOut, mdOut: mdOut, globs: globs, watchMode: watchMode}
	p := tea.NewProgram(m, tea.WithAltScreen())
	return p.Start()
}

func (m *model) Init() tea.Cmd {
	m.spin = spinner.New()
	m.spin.Spinner = spinner.Dot
	m.spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	m.prog = progress.New(progress.WithDefaultGradient())
	m.started = time.Now()
	m.lowRPS = -1
	m.scanDone = false
	m.scanComplete = make(chan struct{})
	m.results = make(chan web.Result, 256)
	m.stats = make(chan web.Stats, 64)

	// Start initial scan
	m.startScan()

	// Start file watcher if in watch mode
	if m.watchMode {
		return tea.Batch(m.spin.Tick, m.waitForEvent(), tickCmd(), m.startWatcher())
	}

	return tea.Batch(m.spin.Tick, m.waitForEvent(), tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg{t: t} })
}

func (m *model) startScan() {
	// Cancel previous scan if it exists
	if m.scanCancel != nil {
		m.scanCancel()
	}

	// Create new context for this scan
	m.scanCtx, m.scanCancel = context.WithCancel(context.Background())
	m.scanComplete = make(chan struct{})

	go func() {
		defer func() {
			m.scanCancel()
			// Signal that scan is completely done
			if m.scanComplete != nil {
				close(m.scanComplete)
			}
		}()

		// Phase 1: Complete file scanning first
		select {
		case <-m.scanCtx.Done():
			return
		default:
		}

		m.lines = append(m.lines, "🔍 Scanning files...")
		m.refreshViewport()

		urlsMap, _ := fsCollectProgress(m.rootPath, m.globs, func(rel string) {
			// Check context before processing each file
			select {
			case <-m.scanCtx.Done():
				return
			default:
			}
			m.filesScanned++
			// Emit a short event line per file to show activity
			m.lines = append(m.lines, fmt.Sprintf("📄 %s", rel))
			m.refreshViewport()
		})

		// File scanning is complete - set the flag
		m.scanDone = true
		m.lines = append(m.lines, fmt.Sprintf("✅ File scanning complete: %d files scanned", m.filesScanned))
		m.refreshViewport()

		// Check context before starting URL checking
		select {
		case <-m.scanCtx.Done():
			return
		default:
		}

		// Phase 2: Now start URL checking
		m.lines = append(m.lines, "🌐 Checking URLs...")
		m.refreshViewport()

		var urls []string
		for u := range urlsMap {
			urls = append(urls, u)
		}
		web.CheckURLs(m.scanCtx, urls, urlsMap, m.results, m.stats, m.cfg)
	}()
}

func (m *model) findSlinkyConfig(root string) string {
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

func (m *model) startWatcher() tea.Cmd {
	return func() tea.Msg {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return watchErrorMsg{err: err}
		}
		defer watcher.Close()

		// Add the root path and all subdirectories to the watcher
		err = filepath.Walk(m.rootPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return watcher.Add(path)
			}
			return nil
		})
		if err != nil {
			return watchErrorMsg{err: err}
		}

		// Also watch for .slinkignore files by searching upward from root
		slinkignorePath := m.findSlinkyConfig(m.rootPath)
		if slinkignorePath != "" {
			// Watch the directory containing the .slinkignore file
			slinkignoreDir := filepath.Dir(slinkignorePath)
			if err := watcher.Add(slinkignoreDir); err != nil {
				// If we can't watch the .slinkignore directory, continue without it
				// This is not a critical error
			}
		}

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return nil
				}
				// Only watch for write events (file modifications)
				if event.Op&fsnotify.Write == fsnotify.Write {
					// Check if it's a .slinkignore file change
					if filepath.Base(event.Name) == ".slinkignore" {
						return fileChangedMsg{path: event.Name}
					}

					// Check if the file matches our glob patterns
					rel, err := filepath.Rel(m.rootPath, event.Name)
					if err != nil {
						continue
					}
					rel = filepath.ToSlash(rel)

					// Check if the file matches any of our glob patterns
					matches := false
					if len(m.globs) == 0 {
						matches = true
					} else {
						for _, pattern := range m.globs {
							if matched, _ := doublestar.PathMatch(pattern, rel); matched {
								matches = true
								break
							}
						}
					}

					if matches {
						return fileChangedMsg{path: event.Name}
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return nil
				}
				return watchErrorMsg{err: err}
			}
		}
	}
}

func (m *model) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		if m.results == nil {
			return crawlDoneMsg{}
		}
		select {
		case res, ok := <-m.results:
			if ok {
				return linkResultMsg{res: res}
			}
			return crawlDoneMsg{}
		case s := <-m.stats:
			return statsMsg{s: s}
		}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			// Clean up resources before quitting
			if m.scanCancel != nil {
				m.scanCancel()
			}
			return m, tea.Quit
		case "f":
			m.showFail = !m.showFail
			m.refreshViewport()
			return m, nil
		case "r":
			// Manual rescan - only available when scan is done
			if m.done {
				m.lines = append(m.lines, "🔄 Manual rescan triggered")
				m.refreshViewport()

				// Cancel previous scan and wait for cleanup
				if m.scanCancel != nil {
					m.scanCancel()
					// Wait for the previous scan to completely finish
					if m.scanComplete != nil {
						select {
						case <-m.scanComplete:
							// Previous scan is done
						case <-time.After(2 * time.Second):
							// Timeout after 2 seconds
						}
					}
				}

				// Reset counters and start new scan
				m.total = 0
				m.ok = 0
				m.fail = 0
				m.processed = 0
				m.lastProcessed = 0
				m.filesScanned = 0
				m.allResults = nil
				m.started = time.Now()
				m.finishedAt = time.Time{}
				m.done = false
				m.scanDone = false
				m.results = make(chan web.Result, 256)
				m.stats = make(chan web.Stats, 64)

				// Clear the lines to start fresh (but keep the rescan notification)
				rescanLine := m.lines[len(m.lines)-1] // Keep the last line (the rescan notification)
				m.lines = []string{rescanLine}
				// Reset viewport to prevent slice bounds error
				m.vp.SetContent("")
				m.vp.GotoTop()
				m.startScan()
				return m, m.waitForEvent()
			}
			return m, nil
		}
	case tea.WindowSizeMsg:
		// Reserve space for header (1), stats (1), progress (1), spacer (1), footer (1)
		reserved := 5
		if m.vp.Width == 0 {
			m.vp = viewport.Model{Width: msg.Width, Height: max(msg.Height-reserved, 3)}
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = max(msg.Height-reserved, 3)
		}
		m.prog.Width = max(msg.Width-4, 10)
		m.refreshViewport()
		return m, nil
	case linkResultMsg:
		// Show every event in the log
		prefix := statusEmoji(msg.res.OK, msg.res.Err)
		if msg.res.CacheHit {
			prefix = "🗃"
		}
		line := fmt.Sprintf("%s %3d %s", prefix, msg.res.Status, msg.res.URL)
		m.lines = append(m.lines, line)
		// Only count non-cache-hit in totals and JSON export
		if !msg.res.CacheHit {
			m.total++
			if msg.res.OK && msg.res.Err == nil {
				m.ok++
			} else {
				m.fail++
			}
			m.allResults = append(m.allResults, msg.res)
		}
		m.refreshViewport()
		return m, m.waitForEvent()
	case statsMsg:
		m.pending = msg.s.Pending
		m.processed = msg.s.Processed
		return m, m.waitForEvent()
	case tickMsg:
		// compute requests/sec over the last tick
		delta := m.processed - m.lastProcessed
		m.lastProcessed = m.processed
		m.rps = float64(delta)
		if m.rps > m.peakRPS {
			m.peakRPS = m.rps
		}
		if m.lowRPS < 0 || m.rps < m.lowRPS {
			m.lowRPS = m.rps
		}
		return m, tickCmd()
	case crawlDoneMsg:
		m.done = true
		m.finishedAt = time.Now()
		m.results = nil
		m.writeJSON()
		m.writeMarkdown()
		if m.watchMode {
			// In watch mode, don't quit, just wait for more changes
			return m, m.startWatcher()
		}
		return m, tea.Quit
	case fileChangedMsg:
		// File changed, restart the scan
		fileName := filepath.Base(msg.path)
		if fileName == ".slinkignore" {
			m.lines = append(m.lines, fmt.Sprintf("⚙️  .slinkignore changed: %s", msg.path))
		} else {
			m.lines = append(m.lines, fmt.Sprintf("🔄 File changed: %s", msg.path))
		}
		m.refreshViewport()

		// Cancel previous scan and wait for cleanup
		if m.scanCancel != nil {
			m.scanCancel()
			// Wait for the previous scan to completely finish
			if m.scanComplete != nil {
				select {
				case <-m.scanComplete:
					// Previous scan is done
				case <-time.After(2 * time.Second):
					// Timeout after 2 seconds
				}
			}
		}

		// Reset counters and start new scan
		m.total = 0
		m.ok = 0
		m.fail = 0
		m.processed = 0
		m.lastProcessed = 0
		m.filesScanned = 0
		m.allResults = nil
		m.started = time.Now()
		m.finishedAt = time.Time{}
		m.done = false
		m.scanDone = false
		m.results = make(chan web.Result, 256)
		m.stats = make(chan web.Stats, 64)

		// Clear the lines to start fresh (but keep the change notification)
		changeLine := m.lines[len(m.lines)-1] // Keep the last line (the change notification)
		m.lines = []string{changeLine}
		// Reset viewport to prevent slice bounds error
		m.vp.SetContent("")
		m.vp.GotoTop()
		m.startScan()
		return m, m.waitForEvent()
	case watchErrorMsg:
		m.lines = append(m.lines, fmt.Sprintf("❌ Watch error: %v", msg.err))
		m.refreshViewport()
		return m, nil
	}

	var cmd tea.Cmd
	m.spin, cmd = m.spin.Update(msg)
	return m, cmd
}

func (m *model) refreshViewport() {
	var filtered []string
	if m.showFail {
		for _, l := range m.lines {
			if strings.HasPrefix(l, "❌") {
				filtered = append(filtered, l)
			}
		}
	} else {
		filtered = m.lines
	}
	m.vp.SetContent(strings.Join(filtered, "\n"))
	m.vp.GotoBottom()
}

func (m *model) writeJSON() {
	path := m.jsonOut
	if strings.TrimSpace(path) == "" {
		base := filepath.Base(m.rootPath)
		if strings.TrimSpace(base) == "" || base == "." || base == string(filepath.Separator) {
			base = "results"
		}
		re := regexp.MustCompile(`[^a-zA-Z0-9.-]+`)
		safe := re.ReplaceAllString(strings.ToLower(base), "_")
		path = fmt.Sprintf("%s.json", safe)
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	// Only write failing results
	var fails []web.Result
	for _, r := range m.allResults {
		if !(r.OK && r.Err == nil) {
			fails = append(fails, r)
		}
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	_ = enc.Encode(fails)
	m.jsonPath = path
}

func (m *model) writeMarkdown() {
	// Compute average RPS over entire run
	dur := m.finishedAt.Sub(m.started)
	avg := 0.0
	if dur.Seconds() > 0 {
		avg = float64(m.processed) / dur.Seconds()
	}
	s := report.Summary{
		RootPath:        m.rootPath,
		StartedAt:       m.started,
		FinishedAt:      m.finishedAt,
		Processed:       m.processed,
		OK:              m.ok,
		Fail:            m.fail,
		AvgRPS:          avg,
		PeakRPS:         m.peakRPS,
		LowRPS:          m.lowRPS,
		JSONPath:        m.jsonPath,
		RepoBlobBaseURL: os.Getenv("SLINKY_REPO_BLOB_BASE_URL"),
	}
	// Only include failing results in the markdown report
	var failsMD []web.Result
	for _, r := range m.allResults {
		if !(r.OK && r.Err == nil) {
			failsMD = append(failsMD, r)
		}
	}
	p, err := report.WriteMarkdown(m.mdOut, failsMD, s)
	if err == nil {
		m.mdPath = p
	}
}

func (m *model) View() string {
	headerText := fmt.Sprintf(" Scanning %s ", m.rootPath)
	if m.watchMode {
		headerText = fmt.Sprintf(" Scanning %s (WATCH MODE) ", m.rootPath)
	}
	header := lipgloss.NewStyle().Bold(true).Render(headerText)
	if m.done {
		dur := time.Since(m.started)
		if !m.finishedAt.IsZero() {
			dur = m.finishedAt.Sub(m.started)
		}
		avg := 0.0
		if dur.Seconds() > 0 {
			avg = float64(m.processed) / dur.Seconds()
		}
		summary := []string{
			fmt.Sprintf("Duration: %s", dur.Truncate(time.Millisecond)),
			fmt.Sprintf("Processed: %d  OK:%d  Fail:%d", m.processed, m.ok, m.fail),
			fmt.Sprintf("Rates: avg %.1f/s  peak %.1f/s  low %.1f/s", avg, m.peakRPS, m.lowRPS),
			fmt.Sprintf("Files scanned: %d", m.filesScanned),
		}
		if m.jsonPath != "" {
			summary = append(summary, fmt.Sprintf("JSON: %s", m.jsonPath))
		}
		if m.mdPath != "" {
			summary = append(summary, fmt.Sprintf("Markdown: %s", m.mdPath))
		}
		footerText := "Controls: [q] quit  [f] toggle fails  [r] rescan"
		footer := lipgloss.NewStyle().Faint(true).Render(footerText)
		container := lipgloss.NewStyle().Padding(1)
		return container.Render(strings.Join(append([]string{header}, append(summary, footer)...), "\n"))
	}
	percent := 0.0
	totalWork := m.processed + m.pending
	if totalWork > 0 {
		percent = float64(m.processed) / float64(totalWork)
	}
	progressLine := m.prog.ViewAs(percent)
	stats := fmt.Sprintf("%s  total:%d  ok:%d  fail:%d  pending:%d processed:%d  rps:%.1f/s  files:%d", m.spin.View(), m.total, m.ok, m.fail, m.pending, m.processed, m.rps, m.filesScanned)
	body := m.vp.View()
	footerText := "Controls: [q] quit  [f] toggle fails"
	footer := lipgloss.NewStyle().Faint(true).Render(footerText)
	container := lipgloss.NewStyle().Padding(1)
	return container.Render(strings.Join([]string{header, stats, progressLine, "", body, footer}, "\n"))
}

func statusEmoji(ok bool, err error) string {
	if ok && err == nil {
		return "✅"
	}
	return "❌"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
