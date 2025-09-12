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

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"slinky/internal/report"
	"slinky/internal/web"
)

type linkResultMsg struct{ res web.Result }
type crawlDoneMsg struct{}
type statsMsg struct{ s web.Stats }
type tickMsg struct{ t time.Time }

type fileScannedMsg struct{ rel string }

type model struct {
	rootPath string
	cfg      web.Config
	jsonOut  string
	mdOut    string
	globs    []string

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
}

// Run scans files under rootPath matching globs, extracts URLs, and checks them.
func Run(rootPath string, globs []string, cfg web.Config, jsonOut string, mdOut string) error {
	m := &model{rootPath: rootPath, cfg: cfg, jsonOut: jsonOut, mdOut: mdOut, globs: globs}
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
	m.results = make(chan web.Result, 256)
	m.stats = make(chan web.Stats, 64)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		defer cancel()
		urlsMap, _ := fsCollectProgress(m.rootPath, m.globs, func(rel string) {
			m.filesScanned++
			// Emit a short event line per file to show activity
			m.lines = append(m.lines, fmt.Sprintf("📄 %s", rel))
			m.refreshViewport()
		})
		var urls []string
		for u := range urlsMap {
			urls = append(urls, u)
		}
		web.CheckURLs(ctx, urls, urlsMap, m.results, m.stats, m.cfg)
	}()

	return tea.Batch(m.spin.Tick, m.waitForEvent(), tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg{t: t} })
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
			return m, tea.Quit
		case "f":
			m.showFail = !m.showFail
			m.refreshViewport()
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
		return m, tea.Quit
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
	header := lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf(" Scanning %s ", m.rootPath))
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
		footer := lipgloss.NewStyle().Faint(true).Render("Controls: [q] quit  [f] toggle fails")
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
	footer := lipgloss.NewStyle().Faint(true).Render("Controls: [q] quit  [f] toggle fails")
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
