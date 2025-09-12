package cmd

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"slinky/internal/tui"
	"slinky/internal/web"
)

func init() {
	runCmd := &cobra.Command{
		Use:   "run [targets...]",
		Short: "Scan a directory/repo for URLs in files and validate them (TUI)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := web.Config{MaxConcurrency: maxConcurrency}
			var gl []string
			if len(args) > 0 {
				for _, a := range args {
					for _, part := range strings.Split(a, ",") {
						p := strings.TrimSpace(part)
						if p != "" {
							gl = append(gl, p)
						}
					}
				}
			} else {
				gl = []string{"**/*"}
			}

			root := "."
			if len(gl) == 1 && !hasGlobMeta(gl[0]) {
				candidate := gl[0]
				if fi, err := os.Stat(candidate); err == nil {
					if fi.IsDir() {
						root = candidate
						gl = []string{"**/*"}
					} else {
						root = candidate
						gl = nil
					}
				}
			}

			return tui.Run(root, gl, cfg, jsonOut, mdOut)
		},
	}

	runCmd.Flags().IntVar(&maxConcurrency, "concurrency", 16, "maximum concurrent requests")
	runCmd.Flags().StringVar(&jsonOut, "json-out", "", "path to write full JSON results (array)")
	runCmd.Flags().StringVar(&mdOut, "md-out", "", "path to write Markdown report for PR comment")
	runCmd.Flags().StringVar(&repoBlobBase, "repo-blob-base", "", "override GitHub blob base URL (e.g. https://github.com/owner/repo/blob/<sha>)")
	rootCmd.AddCommand(runCmd)
}

var (
	maxConcurrency int
	jsonOut        string
	mdOut          string
)
