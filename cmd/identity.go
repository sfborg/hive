package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/sfborg/hive/pkg/config"
	"golang.org/x/term"
)

// resolveIdentity applies the precedence chain flag > env > config for
// both the actor ORCID and the OpenAlex email. If the config file
// doesn't exist AND stdout is a TTY, runs the first-run prompt to seed
// it. Non-TTY environments (systemd, docker) skip the prompt silently
// and just use whatever flag/env values are set.
func resolveIdentity(orcidFlag, emailFlag string) (config.Identity, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Identity{}, err
	}

	if configFileMissing() && isatty(os.Stdin.Fd()) {
		if seeded, err := promptFirstRun(cfg); err == nil && seeded {
			if err := config.Save(cfg); err != nil {
				fmt.Fprintln(os.Stderr, "warning: could not save config:", err)
			}
		}
	}

	id := config.Identity{
		ORCID:         firstNonEmpty(orcidFlag, os.Getenv("HIVE_ORCID"), cfg.ORCID),
		OpenAlexEmail: firstNonEmpty(emailFlag, os.Getenv("HIVE_OPENALEX_EMAIL"), cfg.OpenAlexEmail),
	}
	return id, nil
}

func configFileMissing() bool {
	path, err := config.Path()
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
func promptFirstRun(cfg *config.Config) (bool, error) {
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
		path, _ := config.Path()
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
