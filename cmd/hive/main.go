// Command hive is the sfga taxonomic editor. Subcommands cover read-only
// browsing (view), editing (edit), and network delivery (serve). Additional
// CLI verbs (import, validate, reindex) mirror the same core/ library.
//
// This file is the top-level dispatcher. It intentionally does not use
// cobra yet — only a handful of subcommands exist — but the switch will
// migrate to cobra once the surface expands.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sfborg/hive/cmd/hive/serve"
	"github.com/sfborg/hive/cmd/hive/view"
	"github.com/sfborg/hive/core"
	"golang.org/x/term"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "view":
		if err := runView(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "hive view:", err)
			os.Exit(1)
		}
	case "edit":
		if err := runEdit(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "hive edit:", err)
			os.Exit(1)
		}
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "hive serve:", err)
			os.Exit(1)
		}
	case "config":
		if err := runConfig(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "hive config:", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "hive: unknown subcommand %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func runView(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: hive view <archive.db>")
	}
	// View is read-only; no identity is stamped anywhere, so we skip
	// the resolve+prompt entirely and just launch the TUI.
	return view.Run(args[0], view.Options{Editable: false})
}

func runEdit(args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: hive edit [--orcid ORCID] [--openalex-email EMAIL] <archive.db>")
		fs.PrintDefaults()
	}
	orcidFlag := fs.String("orcid", "", "actor ORCID iD stamped into col__modified_by (overrides HIVE_ORCID / config)")
	emailFlag := fs.String("openalex-email", "", "email sent to OpenAlex's polite request pool (overrides HIVE_OPENALEX_EMAIL / config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("expected exactly one archive path")
	}
	id, err := resolveIdentity(*orcidFlag, *emailFlag)
	if err != nil {
		return err
	}
	core.SetIdentity(id)
	return view.Run(rest[0], view.Options{Editable: true, Actor: id.ORCID})
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(),
			"usage: hive serve [--bind host:port] [--readonly] [--orcid ORCID] [--openalex-email EMAIL] [--allow-unauthenticated] <archive.db>")
		fs.PrintDefaults()
	}
	bind := fs.String("bind", "", "host:port to listen on (defaults to 127.0.0.1 on the standard port)")
	readOnly := fs.Bool("readonly", false, "open the archive read-only (edits refused at the core layer)")
	orcidFlag := fs.String("orcid", "", "actor ORCID iD stamped into col__modified_by (overrides HIVE_ORCID / config)")
	emailFlag := fs.String("openalex-email", "", "email sent to OpenAlex's polite request pool (overrides HIVE_OPENALEX_EMAIL / config)")
	allowUnauth := fs.Bool("allow-unauthenticated", false, "permit a non-loopback bind without auth (unsafe)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("expected exactly one archive path")
	}
	id, err := resolveIdentity(*orcidFlag, *emailFlag)
	if err != nil {
		return err
	}
	core.SetIdentity(id)

	return serve.Run(rest[0], serve.Options{
		Bind:                 *bind,
		AllowUnauthenticated: *allowUnauth,
		ReadOnly:             *readOnly,
		Actor:                id.ORCID,
	})
}

// resolveIdentity applies the precedence chain flag > env > config for
// both the actor ORCID and the OpenAlex email. If the config file
// doesn't exist AND stdout is a TTY, runs the first-run prompt to seed
// it. Non-TTY environments (systemd, docker) skip the prompt silently
// and just use whatever flag/env values are set.
func resolveIdentity(orcidFlag, emailFlag string) (core.Identity, error) {
	cfg, err := core.LoadConfig()
	if err != nil {
		return core.Identity{}, err
	}

	// If nothing is set anywhere and we're on a TTY, offer to seed
	// the config now. Missing file is the trigger — a curator who
	// once set values and later cleared them isn't re-prompted.
	if configFileMissing() && isatty(os.Stdin.Fd()) {
		if seeded, err := promptFirstRun(cfg); err == nil && seeded {
			if err := core.SaveConfig(cfg); err != nil {
				fmt.Fprintln(os.Stderr, "warning: could not save config:", err)
			}
		}
	}

	id := core.Identity{
		ORCID:         firstNonEmpty(orcidFlag, os.Getenv("HIVE_ORCID"), cfg.ORCID),
		OpenAlexEmail: firstNonEmpty(emailFlag, os.Getenv("HIVE_OPENALEX_EMAIL"), cfg.OpenAlexEmail),
	}
	return id, nil
}

func configFileMissing() bool {
	path, err := core.ConfigPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return os.IsNotExist(err)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// isatty reports whether fd refers to a terminal. Wraps
// golang.org/x/term.IsTerminal so the intent is obvious at call sites.
func isatty(fd uintptr) bool { return term.IsTerminal(int(fd)) }

// promptFirstRun asks the curator for their ORCID iD and OpenAlex
// email. Both are optional. Returns true when at least one field was
// entered (config is worth saving); false when the curator skipped
// everything.
func promptFirstRun(cfg *core.Config) (bool, error) {
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Welcome to hive. Two optional identifiers help hive work better:")
	fmt.Fprintln(os.Stderr, "  • ORCID iD — stamped on every edit so contributions are traceable")
	fmt.Fprintln(os.Stderr, "  • OpenAlex email — sent with reference lookups to OpenAlex's")
	fmt.Fprintln(os.Stderr, "    higher-priority request pool (blank still works, just slower)")
	fmt.Fprintln(os.Stderr, "")

	r := bufio.NewReader(os.Stdin)
	orcid, err := prompt(r, "ORCID iD (0000-0000-0000-0000, blank to skip): ")
	if err != nil {
		return false, err
	}
	email, err := prompt(r, "OpenAlex email (blank to skip): ")
	if err != nil {
		return false, err
	}
	seeded := false
	if orcid != "" {
		cfg.ORCID = orcid
		seeded = true
	}
	if email != "" {
		cfg.OpenAlexEmail = email
		seeded = true
	}
	if seeded {
		path, _ := core.ConfigPath()
		fmt.Fprintf(os.Stderr, "Saved to %s\n\n", path)
	} else {
		fmt.Fprintln(os.Stderr, "")
	}
	return seeded, nil
}

func prompt(r *bufio.Reader, msg string) (string, error) {
	fmt.Fprint(os.Stderr, msg)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// runConfig implements the hive config subcommand.
//
//	hive config get <key>
//	hive config set <key> <value>
//	hive config path
//
// Keys: "orcid", "openalex-email". Aliases the config-file field
// names so scripting is symmetric across the CLI and file.
func runConfig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hive config <get|set|path> [args]")
	}
	switch args[0] {
	case "path":
		p, err := core.ConfigPath()
		if err != nil {
			return err
		}
		fmt.Println(p)
		return nil
	case "get":
		if len(args) != 2 {
			return fmt.Errorf("usage: hive config get <key>")
		}
		cfg, err := core.LoadConfig()
		if err != nil {
			return err
		}
		v, err := configField(cfg, args[1])
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil
	case "set":
		if len(args) != 3 {
			return fmt.Errorf("usage: hive config set <key> <value>")
		}
		cfg, err := core.LoadConfig()
		if err != nil {
			return err
		}
		if err := setConfigField(cfg, args[1], args[2]); err != nil {
			return err
		}
		return core.SaveConfig(cfg)
	default:
		return fmt.Errorf("unknown config subcommand %q", args[0])
	}
}

func configField(c *core.Config, key string) (string, error) {
	switch key {
	case "orcid":
		return c.ORCID, nil
	case "openalex-email", "openalex_email":
		return c.OpenAlexEmail, nil
	}
	return "", fmt.Errorf("unknown config key %q (valid: orcid, openalex-email)", key)
}

func setConfigField(c *core.Config, key, value string) error {
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

func usage(w *os.File) {
	fmt.Fprintln(w, `hive — sfga taxonomic editor

Usage:
  hive <subcommand> [args]

Subcommands:
  view <archive.db>   Read-only TUI
  edit [--orcid ID] [--openalex-email EMAIL] <archive.db>
                      Editing TUI
  serve [--bind host:port] [--readonly] [--orcid ID] [--openalex-email EMAIL] [--allow-unauthenticated] <archive.db>
                      HTTP + WUI (browser)
  config <get|set|path> [args]
                      Read / write ~/.config/sfborg/hive/config.yml
  import <src> -o <archive.db>
                      Import via sflib (not yet)
  validate <archive.db>
                      Run gsvalidator (not yet)
  reindex <archive.db>
                      Bulk gnparser refresh (not yet)`)
}
