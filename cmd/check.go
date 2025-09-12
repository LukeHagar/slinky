package cmd

import (
	"context"
	"encoding/json"
	"fmt"
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
			web.CheckURLs(ctx, urls, urlToFiles, results, nil, cfg)

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

			// Optionally write Markdown report for PR comment consumption
			if mdOut != "" {
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
					JSONPath:        jsonOut,
					RepoBlobBaseURL: base,
				}
				if _, err := report.WriteMarkdown(mdOut, failedResults, summary); err != nil {
					return err
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
