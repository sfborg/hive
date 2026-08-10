package cmd

import (
	"github.com/sfborg/hive/internal/tui"
	"github.com/sfborg/hive/pkg/config"
	"github.com/spf13/cobra"
)

var editCmd = &cobra.Command{
	Use:           "edit <archive.db>",
	Short:         "Editing TUI",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		orcidFlag, _ := cmd.Flags().GetString("orcid")
		emailFlag, _ := cmd.Flags().GetString("openalex-email")
		id, err := resolveIdentity(orcidFlag, emailFlag)
		if err != nil {
			return err
		}
		config.SetIdentity(id)
		return tui.Run(args[0], tui.Options{Editable: true, Actor: id.ORCID})
	},
}

func init() {
	editCmd.Flags().String("orcid", "",
		"actor ORCID iD stamped into col__modified_by (overrides HIVE_ORCID / config)")
	editCmd.Flags().String("openalex-email", "",
		"email sent to OpenAlex's polite request pool (overrides HIVE_OPENALEX_EMAIL / config)")
	rootCmd.AddCommand(editCmd)
}
