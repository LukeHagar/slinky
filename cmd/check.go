package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"slinky/internal/fsurls"
	"slinky/internal/report"
	"slinky/internal/web"
)

// SerializableResult mirrors web.Result but omits the error field for JSON.
type SerializableResult struct {
	URL         string   `json:"url"`
	OK          bool     `json:"ok"`
	Status      int      `json:"status"`
	ErrMsg      string   `json:"error"`
	Method      string   `json:"method"`
	ContentType string   `json:"contentType"`
	Sources     []string `json:"sources"`
}

func init() {
	checkCmd := &cobra.Command{
		Use:   "check [targets...]",
		Short: "Scan for URLs and validate them (headless)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse targets: allow comma-separated chunks
			var raw []string
			for _, a := range args {
				for _, part := range strings.Split(a, ",") {
					p := strings.TrimSpace(part)
					if p != "" {
						raw = append(raw, toSlash(p))
					}
				}
			}
			if len(raw) == 0 {
				raw = []string{"**/*"}
			}

			// Separate into globs (relative to ".") and concrete paths (dirs/files)
			var globPatterns []string
			type pathRoot struct {
				path  string
				isDir bool
			}
			var roots []pathRoot
			for _, t := range raw {
				if hasGlobMeta(t) {
					globPatterns = append(globPatterns, t)
					continue
				}
				if fi, err := os.Stat(t); err == nil {
					roots = append(roots, pathRoot{path: t, isDir: fi.IsDir()})
				} else {
					// If stat fails, treat as glob pattern under "."
					globPatterns = append(globPatterns, t)
				}
			}

			// Debug: show effective targets
			if shouldDebug() {
				fmt.Printf("::debug:: Roots: %s\n", strings.Join(func() []string {
					var out []string
					for _, r := range roots {
						out = append(out, r.path)
					}
					return out
				}(), ","))
				fmt.Printf("::debug:: Glob patterns: %s\n", strings.Join(globPatterns, ","))
			}

			// Aggregate URL->files across all targets
			agg := make(map[string]map[string]struct{})
			merge := func(res map[string][]string, prefix string, isDir bool) {
				for u, files := range res {
					set, ok := agg[u]
					if !ok {
						set = make(map[string]struct{})
						agg[u] = set
					}
					for _, fp := range files {
						var merged string
						if prefix == "" {
							merged = fp
						} else if isDir {
							merged = toSlash(filepath.Join(prefix, fp))
						} else {
							// File root: keep the concrete file path
							merged = toSlash(prefix)
						}
						set[merged] = struct{}{}
					}
				}
			}

			// 1) Collect for globs under current dir
			if len(globPatterns) > 0 {
				res, err := fsurls.CollectURLs(".", globPatterns, respectGitignore)
				if err != nil {
					return err
				}
				merge(res, "", true)
			}
			// 2) Collect for each concrete root
			for _, r := range roots {
				clean := toSlash(filepath.Clean(r.path))
				if r.isDir {
					res, err := fsurls.CollectURLs(r.path, []string{"**/*"}, respectGitignore)
					if err != nil {
						return err
					}
					merge(res, clean, true)
				} else {
					res, err := fsurls.CollectURLs(r.path, nil, respectGitignore)
					if err != nil {
						return err
					}
					merge(res, clean, false)
				}
			}

			// Convert aggregator to final map with sorted file lists
			urlToFiles := make(map[string][]string, len(agg))
			for u, set := range agg {
				var files []string
				for f := range set {
					files = append(files, f)
				}
				sort.Strings(files)
				urlToFiles[u] = files
			}

			// Derive display root; we use "." when multiple roots to avoid confusion
			displayRoot := "."
			if len(roots) == 1 && len(globPatterns) == 0 {
				displayRoot = roots[0].path
			}
			if shouldDebug() {
				fmt.Printf("::debug:: Root: %s\n", displayRoot)
			}

			// Build config
			timeout := time.Duration(timeoutSeconds) * time.Second
			cfg := web.Config{MaxConcurrency: maxConcurrency, RequestTimeout: timeout}

			// Prepare URL list
			var urls []string
			for u := range urlToFiles {
				urls = append(urls, u)
			}
			sort.Strings(urls)

			// If no URLs found, exit early
			if len(urls) == 0 {
				fmt.Println("No URLs found.")
				return nil
			}

			// Run checks
			startedAt := time.Now()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			results := make(chan web.Result, 256)
			go web.CheckURLs(ctx, urls, urlToFiles, results, nil, cfg)

			var total, okCount, failCount int
			var failures []SerializableResult
			var failedResults []web.Result

			for r := range results {
				total++
				if r.OK {
					okCount++
				} else {
					failCount++
				}
				// Emit GitHub Actions debug log for each URL.
				// These lines appear only when step debug logging is enabled via the
				// repository/organization secret ACTIONS_STEP_DEBUG=true.
				if shouldDebug() {
					fmt.Printf("::debug:: Scanned URL: %s status=%d ok=%v err=%s sources=%d\n", r.URL, r.Status, r.OK, r.ErrMsg, len(r.Sources))
				}
				if jsonOut != "" && !r.OK {
					failures = append(failures, SerializableResult{
						URL:         r.URL,
						OK:          r.OK,
						Status:      r.Status,
						ErrMsg:      r.ErrMsg,
						Method:      r.Method,
						ContentType: r.ContentType,
						Sources:     r.Sources,
					})
				}
				if !r.OK {
					failedResults = append(failedResults, r)
				}
			}

			// Write JSON if requested (failures only)
			if jsonOut != "" {
				f, ferr := os.Create(jsonOut)
				if ferr != nil {
					return ferr
				}
				enc := json.NewEncoder(f)
				enc.SetIndent("", "  ")
				if err := enc.Encode(failures); err != nil {
					_ = f.Close()
					return err
				}
				_ = f.Close()
			}

			// Build report summary
			base := repoBlobBase
			if strings.TrimSpace(base) == "" {
				base = os.Getenv("SLINKY_REPO_BLOB_BASE_URL")
			}
			summary := report.Summary{
				RootPath:        displayRoot,
				StartedAt:       startedAt,
				FinishedAt:      time.Now(),
				Processed:       total,
				OK:              okCount,
				Fail:            failCount,
				FilesScanned:    countFiles(urlToFiles),
				JSONPath:        jsonOut,
				RepoBlobBaseURL: base,
			}

			// Ensure we have a markdown file if needed for PR comment
			mdPath := mdOut
			ghRepo, ghPR, ghToken, ghOK := detectGitHubPR()
			if strings.TrimSpace(mdPath) != "" {
				if _, err := report.WriteMarkdown(mdPath, failedResults, summary); err != nil {
					return err
				}
			} else if ghOK {
				p, err := report.WriteMarkdown("", failedResults, summary)
				if err != nil {
					return err
				}
				mdPath = p
			}

			if shouldDebug() {
				fmt.Printf("::debug:: Running Environment: repo=%s pr=%d token=%s mdPath=%s\n", ghRepo, ghPR, ghToken, mdPath)
			}

			// If running on a PR, post or update the comment
			if ghOK && strings.TrimSpace(mdPath) != "" {
				b, rerr := os.ReadFile(mdPath)
				if rerr == nil {
					body := string(b)
					body = fmt.Sprintf("%s\n%s", "<!-- slinky-report -->", body)
					_ = upsertPRComment(ghRepo, ghPR, ghToken, body)
				}
			}

			fmt.Printf("Checked %d URLs: %d OK, %d failed\n", total, okCount, failCount)
			if failOnFailures && failCount > 0 {
				return fmt.Errorf("%d links failed", failCount)
			}
			return nil
		},
	}

	checkCmd.Flags().IntVar(&maxConcurrency, "concurrency", 16, "maximum concurrent requests")
	checkCmd.Flags().StringVar(&jsonOut, "json-out", "", "path to write full JSON results (array)")
	checkCmd.Flags().StringVar(&mdOut, "md-out", "", "path to write Markdown report for PR comment")
	checkCmd.Flags().StringVar(&repoBlobBase, "repo-blob-base", "", "override GitHub blob base URL (e.g. https://github.com/owner/repo/blob/<sha>)")
	checkCmd.Flags().IntVar(&timeoutSeconds, "timeout", 10, "HTTP request timeout in seconds")
	checkCmd.Flags().BoolVar(&failOnFailures, "fail-on-failures", true, "exit non-zero if any links fail")
	checkCmd.Flags().BoolVar(&respectGitignore, "respect-gitignore", true, "respect .gitignore while scanning (default true)")

	rootCmd.AddCommand(checkCmd)
}

var (
	timeoutSeconds   int
	failOnFailures   bool
	repoBlobBase     string
	respectGitignore bool
)

func toSlash(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	p = filepath.ToSlash(p)
	if after, ok := strings.CutPrefix(p, "./"); ok {
		p = after
	}
	return p
}

func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func countFiles(urlToFiles map[string][]string) int {
	seen := make(map[string]struct{})
	for _, files := range urlToFiles {
		for _, f := range files {
			seen[f] = struct{}{}
		}
	}
	return len(seen)
}

func detectGitHubPR() (repo string, prNumber int, token string, ok bool) {
	repo = os.Getenv("GITHUB_REPOSITORY")
	token = os.Getenv("GITHUB_TOKEN")
	eventPath := os.Getenv("GITHUB_EVENT_PATH")
	ref := os.Getenv("GITHUB_REF")

	if shouldDebug() {
		fmt.Printf("::debug:: Detected Environment: repo=%s eventPath=%s ref=%s\n", repo, eventPath, ref)
	}

	if repo == "" || eventPath == "" || token == "" {
		return "", 0, "", false
	}
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return "", 0, "", false
	}
	var ev struct {
		PullRequest struct {
			Number int `json:"number"`
		} `json:"pull_request"`
	}
	_ = json.Unmarshal(data, &ev)

	if shouldDebug() {
		fmt.Printf("::debug:: Detected Pull Request: number=%d\n", ev.PullRequest.Number)
	}

	if ev.PullRequest.Number == 0 {
		return "", 0, "", false
	}
	return repo, ev.PullRequest.Number, token, true
}

func upsertPRComment(repo string, prNumber int, token string, body string) error {
	apiBase := "https://api.github.com"
	listURL := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100", apiBase, repo, prNumber)
	req, _ := http.NewRequest(http.MethodGet, listURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var comments []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &comments)
	var existingID int
	for _, c := range comments {
		if strings.Contains(c.Body, "<!-- slinky-report -->") {
			existingID = c.ID
			break
		}
	}

	payload, _ := json.Marshal(map[string]string{"body": body})
	if existingID > 0 {
		if shouldDebug() {
			fmt.Printf("::debug:: Updating existing comment: %d\n", existingID)
		}

		u := fmt.Sprintf("%s/repos/%s/issues/comments/%d", apiBase, repo, existingID)
		req, _ = http.NewRequest(http.MethodPatch, u, bytes.NewReader(payload))
	} else {
		if shouldDebug() {
			fmt.Printf("::debug:: Creating new comment\n")
		}

		u := fmt.Sprintf("%s/repos/%s/issues/%d/comments", apiBase, repo, prNumber)
		req, _ = http.NewRequest(http.MethodPost, u, bytes.NewReader(payload))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	upsertResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer upsertResp.Body.Close()
	upsertRespBody, _ := io.ReadAll(upsertResp.Body)
	upsertRespUnmarshalErr := json.Unmarshal(upsertRespBody, &upsertResp)
	if upsertRespUnmarshalErr != nil {
		return fmt.Errorf("failed to unmarshal upsert response: %s", upsertRespUnmarshalErr)
	}
	if shouldDebug() {
		fmt.Printf("::debug:: Comment upserted Response: %s\n%s", upsertResp.Status, string(upsertRespBody))
		fmt.Printf("::debug:: Comment upserted: %+v\n", upsertResp)
	}
	if upsertResp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to upsert comment: %s", upsertResp.Status)
	}
	return nil
}
