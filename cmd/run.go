package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"slinky/internal/tui"
	"slinky/internal/web"
)

func init() {
	runCmd := &cobra.Command{
		Use:   "run [path]",
		Short: "Scan a directory/repo for URLs in files and validate them (TUI)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			cfg := web.Config{MaxConcurrency: maxConcurrency}
			var gl []string
			if len(patterns) > 0 {
				gl = append(gl, patterns...)
			} else if globPat != "" {
				gl = strings.Split(globPat, ",")
			} else {
				gl = []string{"**/*"}
			}
			return tui.Run(path, gl, cfg, jsonOut, mdOut)
		},
	}

	runCmd.Flags().StringVar(&globPat, "glob", "", "comma-separated glob patterns for files (doublestar); empty = all files")
	runCmd.Flags().StringSliceVar(&patterns, "patterns", nil, "file match patterns (doublestar). Examples: docs/**/*.md,**/*.go; defaults to **/*")
	runCmd.Flags().IntVar(&maxConcurrency, "concurrency", 16, "maximum concurrent requests")
	runCmd.Flags().StringVar(&jsonOut, "json-out", "", "path to write full JSON results (array)")
	runCmd.Flags().StringVar(&mdOut, "md-out", "", "path to write Markdown report for PR comment")
	runCmd.Flags().StringVar(&repoBlobBase, "repo-blob-base", "", "override GitHub blob base URL (e.g. https://github.com/owner/repo/blob/<sha>)")
	rootCmd.AddCommand(runCmd)
}

var (
	maxConcurrency int
	jsonOut        string
	globPat        string
	mdOut          string
)
