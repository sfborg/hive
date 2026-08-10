package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	hive "github.com/sfborg/hive/pkg"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:           "validate <archive.db>",
	Short:         "Re-run every rule and rewrite hive__validation_issue",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		quiet, _ := cmd.Flags().GetBool("quiet")

		a, err := hive.Open(args[0])
		if err != nil {
			return err
		}
		defer a.Close()

		start := time.Now()
		var lastReport time.Time
		err = a.ReindexValidation(context.Background(), func(p hive.ReindexProgress) {
			if quiet {
				return
			}
			// Throttle to at most 20 updates per second so a fast archive
			// doesn't spend more time on stderr writes than on validation.
			now := time.Now()
			if p.Done < p.Total && now.Sub(lastReport) < 50*time.Millisecond {
				return
			}
			lastReport = now
			elapsed := now.Sub(start).Seconds()
			rate := float64(p.Done) / elapsed
			fmt.Fprintf(os.Stderr, "\rvalidating %s: %d/%d (%.0f/s)     ",
				p.Table, p.Done, p.Total, rate)
		})
		if !quiet {
			fmt.Fprintln(os.Stderr)
		}
		if err != nil {
			return err
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "done in %s\n", time.Since(start).Round(time.Millisecond))
		}
		return nil
	},
}

func init() {
	validateCmd.Flags().Bool("quiet", false, "suppress progress output")
	rootCmd.AddCommand(validateCmd)
}
