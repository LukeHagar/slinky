package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "slinky",
	Short: "Link checker for repos/directories and webpages (TUI)",
	Long:  "Slinky scans a directory/repo for URLs in files or crawls a URL, then validates links concurrently in a TUI.",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}


