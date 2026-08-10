package cmd

import (
	"fmt"
	"os"

	hive "github.com/sfborg/hive/pkg"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "hive",
	Short:         "sfga taxonomic editor",
	Long:          "hive edits sfga SQLite archives. Subcommands cover read-only browsing (view), editing (edit), network delivery (serve), validation, and configuration.",
	Version:       hive.GetVersion().Version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute is the CLI entry point. main.go calls this.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "hive:", err)
		os.Exit(1)
	}
}
