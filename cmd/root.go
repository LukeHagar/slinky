package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var debugLogs bool

var rootCmd = &cobra.Command{
	Use:   "slinky",
	Short: "Link checker for repos/directories and webpages (TUI)",
	Long:  "Slinky scans a directory/repo for URLs in files or crawls a URL, then validates links concurrently in a TUI.",
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&debugLogs, "debug", false, "enable debug logs")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func shouldDebug() bool {
	if debugLogs {
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
