package cmd

import (
	"github.com/sfborg/hive/internal/tui"
	"github.com/spf13/cobra"
)

var viewCmd = &cobra.Command{
	Use:           "view <archive.db>",
	Short:         "Read-only TUI",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return tui.Run(args[0], tui.Options{Editable: false})
	},
}

func init() {
	rootCmd.AddCommand(viewCmd)
}
