package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	hive "github.com/sfborg/hive/pkg"
	"github.com/spf13/cobra"
)

var ruleCmd = &cobra.Command{
	Use:   "rule",
	Short: "Manage per-rule validation overrides stored in the archive",
}

var ruleListCmd = &cobra.Command{
	Use:           "list <archive.db>",
	Short:         "List every rule override stored in this archive",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := hive.Open(args[0])
		if err != nil {
			return err
		}
		defer a.Close()
		cfgs, err := a.ListRuleConfigs(context.Background())
		if err != nil {
			return err
		}
		if len(cfgs) == 0 {
			fmt.Fprintln(os.Stderr, "no rule overrides set")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "RULE\tENABLED\tSEVERITY\tBY\tAT")
		for _, c := range cfgs {
			enabled := ""
			if c.Enabled != nil {
				enabled = fmt.Sprintf("%v", *c.Enabled)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				c.RuleID, enabled, c.SeverityOverride, c.UpdatedBy, c.UpdatedAt)
		}
		return w.Flush()
	},
}

var ruleMuteCmd = &cobra.Command{
	Use:           "mute <archive.db> <rule_id>",
	Short:         "Disable a rule in this archive (rule stops firing)",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := hive.Open(args[0])
		if err != nil {
			return err
		}
		defer a.Close()
		disabled := false
		return a.SetRuleConfig(context.Background(), hive.RuleConfig{
			RuleID:  args[1],
			Enabled: &disabled,
		})
	},
}

var ruleUnmuteCmd = &cobra.Command{
	Use:           "unmute <archive.db> <rule_id>",
	Short:         "Re-enable a rule in this archive",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := hive.Open(args[0])
		if err != nil {
			return err
		}
		defer a.Close()
		enabled := true
		return a.SetRuleConfig(context.Background(), hive.RuleConfig{
			RuleID:  args[1],
			Enabled: &enabled,
		})
	},
}

var ruleSeverityCmd = &cobra.Command{
	Use:           "severity <archive.db> <rule_id> <error|warn|info|debug>",
	Short:         "Override a rule's severity in this archive",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := hive.Open(args[0])
		if err != nil {
			return err
		}
		defer a.Close()
		return a.SetRuleConfig(context.Background(), hive.RuleConfig{
			RuleID:           args[1],
			SeverityOverride: strings.ToLower(args[2]),
		})
	},
}

var ruleResetCmd = &cobra.Command{
	Use:           "reset <archive.db> <rule_id>",
	Short:         "Clear all overrides for a rule (revert to bundle default)",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := hive.Open(args[0])
		if err != nil {
			return err
		}
		defer a.Close()
		return a.ClearRuleConfig(context.Background(), args[1])
	},
}

var rulesetCmd = &cobra.Command{
	Use:   "ruleset",
	Short: "Manage per-ruleset (hive / clb / tw) toggles",
}

var rulesetListCmd = &cobra.Command{
	Use:           "list <archive.db>",
	Short:         "List ruleset overrides in this archive",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := hive.Open(args[0])
		if err != nil {
			return err
		}
		defer a.Close()
		cfgs, err := a.ListRulesetConfigs(context.Background())
		if err != nil {
			return err
		}
		if len(cfgs) == 0 {
			fmt.Fprintln(os.Stderr, "no ruleset overrides set — all rulesets use bundle defaults (enabled)")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "RULESET\tENABLED\tBY\tAT")
		for _, c := range cfgs {
			fmt.Fprintf(w, "%s\t%v\t%s\t%s\n", c.RulesetName, c.Enabled, c.UpdatedBy, c.UpdatedAt)
		}
		return w.Flush()
	},
}

var rulesetEnableCmd = &cobra.Command{
	Use:           "enable <archive.db> <hive|clb|tw>",
	Short:         "Enable a ruleset in this archive",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := hive.Open(args[0])
		if err != nil {
			return err
		}
		defer a.Close()
		return a.SetRulesetConfig(context.Background(), args[1], true)
	},
}

var rulesetDisableCmd = &cobra.Command{
	Use:           "disable <archive.db> <hive|clb|tw>",
	Short:         "Disable a ruleset in this archive (all its rules stop firing)",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := hive.Open(args[0])
		if err != nil {
			return err
		}
		defer a.Close()
		return a.SetRulesetConfig(context.Background(), args[1], false)
	},
}

var rulesetResetCmd = &cobra.Command{
	Use:           "reset <archive.db> <hive|clb|tw>",
	Short:         "Clear a ruleset's override (revert to default: enabled)",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := hive.Open(args[0])
		if err != nil {
			return err
		}
		defer a.Close()
		return a.ClearRulesetConfig(context.Background(), args[1])
	},
}

func init() {
	ruleCmd.AddCommand(ruleListCmd)
	ruleCmd.AddCommand(ruleMuteCmd)
	ruleCmd.AddCommand(ruleUnmuteCmd)
	ruleCmd.AddCommand(ruleSeverityCmd)
	ruleCmd.AddCommand(ruleResetCmd)
	rootCmd.AddCommand(ruleCmd)

	rulesetCmd.AddCommand(rulesetListCmd)
	rulesetCmd.AddCommand(rulesetEnableCmd)
	rulesetCmd.AddCommand(rulesetDisableCmd)
	rulesetCmd.AddCommand(rulesetResetCmd)
	rootCmd.AddCommand(rulesetCmd)
}
