package cmd

import (
	"github.com/sfborg/hive/internal/server"
	"github.com/sfborg/hive/pkg/config"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:           "serve <archive.db>",
	Short:         "HTTP + WUI (browser)",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bind, _ := cmd.Flags().GetString("bind")
		readOnly, _ := cmd.Flags().GetBool("readonly")
		orcidFlag, _ := cmd.Flags().GetString("orcid")
		emailFlag, _ := cmd.Flags().GetString("openalex-email")
		allowUnauth, _ := cmd.Flags().GetBool("allow-unauthenticated")

		id, err := resolveIdentity(orcidFlag, emailFlag)
		if err != nil {
			return err
		}
		config.SetIdentity(id)

		return server.Run(args[0], server.Options{
			Bind:                 bind,
			AllowUnauthenticated: allowUnauth,
			ReadOnly:             readOnly,
			Actor:                id.ORCID,
		})
	},
}

func init() {
	serveCmd.Flags().String("bind", "",
		"host:port to listen on (defaults to 127.0.0.1 on the standard port)")
	serveCmd.Flags().Bool("readonly", false,
		"open the archive read-only (edits refused at the hive layer)")
	serveCmd.Flags().String("orcid", "",
		"actor ORCID iD stamped into col__modified_by (overrides HIVE_ORCID / config)")
	serveCmd.Flags().String("openalex-email", "",
		"email sent to OpenAlex's polite request pool (overrides HIVE_OPENALEX_EMAIL / config)")
	serveCmd.Flags().Bool("allow-unauthenticated", false,
		"permit a non-loopback bind without auth (unsafe)")
	rootCmd.AddCommand(serveCmd)
}
