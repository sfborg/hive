package cmd

import (
	"fmt"
	"os"

	hive "github.com/sfborg/hive/pkg"
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
		create, _ := cmd.Flags().GetBool("create")

		archivePath := args[0]
		if create {
			// Create the archive if it doesn't exist yet. Opt-in
			// (curators typoing a path shouldn't silently create a
			// blank archive) but the default in container/deployed
			// setups where a missing archive is the fresh-install
			// case. Existing archives are left alone.
			if _, err := os.Stat(archivePath); os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr,
					"hive: no archive at %s — creating empty one (--create)\n",
					archivePath)
				a, err := hive.Create(archivePath)
				if err != nil {
					return fmt.Errorf("create archive: %w", err)
				}
				a.Close()
			}
		}

		id, err := resolveIdentity(orcidFlag, emailFlag)
		if err != nil {
			return err
		}
		config.SetIdentity(id)

		return server.Run(archivePath, server.Options{
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
	serveCmd.Flags().Bool("create", false,
		"create an empty archive if the path doesn't exist yet (default off; on in the Docker CMD so first-run just works)")
	rootCmd.AddCommand(serveCmd)
}
