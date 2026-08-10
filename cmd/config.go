package cmd

import (
	"fmt"

	"github.com/sfborg/hive/pkg/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read / write ~/.config/sfborg/hive/config.yml",
}

var configPathCmd = &cobra.Command{
	Use:           "path",
	Short:         "Print the config file path",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := config.Path()
		if err != nil {
			return err
		}
		fmt.Println(p)
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:           "get <key>",
	Short:         "Read a config field (keys: orcid, openalex-email)",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		v, err := configField(cfg, args[0])
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:           "set <key> <value>",
	Short:         "Write a config field (keys: orcid, openalex-email)",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := setConfigField(cfg, args[0], args[1]); err != nil {
			return err
		}
		return config.Save(cfg)
	},
}

func configField(c *config.Config, key string) (string, error) {
	switch key {
	case "orcid":
		return c.ORCID, nil
	case "openalex-email", "openalex_email":
		return c.OpenAlexEmail, nil
	}
	return "", fmt.Errorf("unknown config key %q (valid: orcid, openalex-email)", key)
}

func setConfigField(c *config.Config, key, value string) error {
	switch key {
	case "orcid":
		c.ORCID = value
		return nil
	case "openalex-email", "openalex_email":
		c.OpenAlexEmail = value
		return nil
	}
	return fmt.Errorf("unknown config key %q (valid: orcid, openalex-email)", key)
}

func init() {
	configCmd.AddCommand(configPathCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	rootCmd.AddCommand(configCmd)
}
