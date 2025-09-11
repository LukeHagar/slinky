package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
		Use:   "check [path]",
		Short: "Scan a directory for URLs and validate them (headless)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}

			var gl []string
			if len(patterns) > 0 {
				gl = append(gl, patterns...)
			} else if globPat != "" {
				gl = strings.Split(globPat, ",")
			} else {
				gl = []string{"**/*"}
			}

			timeout := time.Duration(timeoutSeconds) * time.Second
			cfg := web.Config{MaxConcurrency: maxConcurrency, RequestTimeout: timeout}

			// Collect URLs
			urlToFiles, err := fsurls.CollectURLs(path, gl)
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

	checkCmd.Flags().StringVar(&globPat, "glob", "", "comma-separated glob patterns for files (doublestar); empty = all files")
	checkCmd.Flags().StringSliceVar(&patterns, "patterns", nil, "file match patterns (doublestar). Examples: docs/**/*.md,**/*.go; defaults to **/*")
	checkCmd.Flags().IntVar(&maxConcurrency, "concurrency", 16, "maximum concurrent requests")
	checkCmd.Flags().StringVar(&jsonOut, "json-out", "", "path to write full JSON results (array)")
	checkCmd.Flags().StringVar(&mdOut, "md-out", "", "path to write Markdown report for PR comment")
	checkCmd.Flags().StringVar(&repoBlobBase, "repo-blob-base", "", "override GitHub blob base URL (e.g. https://github.com/owner/repo/blob/<sha>)")
	checkCmd.Flags().IntVar(&timeoutSeconds, "timeout", 10, "HTTP request timeout in seconds")
	checkCmd.Flags().BoolVar(&failOnFailures, "fail-on-failures", true, "exit non-zero if any links fail")

	rootCmd.AddCommand(checkCmd)
}

var (
	timeoutSeconds int
	failOnFailures bool
	patterns       []string
	repoBlobBase   string
)
