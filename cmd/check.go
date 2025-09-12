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
			path := "."

			var gl []string
			if len(args) > 0 {
				for _, a := range args {
					for _, part := range strings.Split(a, ",") {
						p := strings.TrimSpace(part)
						if p != "" {
							gl = append(gl, toSlash(p))
						}
					}
				}
			} else {
				gl = []string{"**/*"}
			}

			gl = expandDirectories(path, gl)

			// Emit normalized patterns for debugging
			fmt.Printf("::debug:: Effective patterns: %s\n", strings.Join(gl, ","))

			timeout := time.Duration(timeoutSeconds) * time.Second
			cfg := web.Config{MaxConcurrency: maxConcurrency, RequestTimeout: timeout}

			// Collect URLs
			urlToFiles, err := fsurls.CollectURLs(path, gl, respectGitignore)
			if err != nil {
				return err
			}
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
				fmt.Printf("::debug:: Scanned URL: %s status=%d ok=%v err=%s sources=%d\n", r.URL, r.Status, r.OK, r.ErrMsg, len(r.Sources))
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
					RootPath:        path,
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

func expandDirectories(root string, pats []string) []string {
	var out []string
	for _, p := range pats {
		pp := strings.TrimSpace(p)
		if pp == "" {
			continue
		}
		if hasGlobMeta(pp) {
			out = append(out, pp)
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(pp))
		if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
			out = append(out, strings.TrimSuffix(pp, "/")+"/**/*")
		} else {
			out = append(out, pp)
		}
	}
	return out
}
