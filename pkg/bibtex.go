package hive

import (
	"github.com/sfborg/hive/pkg/bibtex"
	"github.com/sfborg/sflib/pkg/coldp"
)

// ErrBibTeXParse is re-exported so callers can errors.Is-check the
// existing core sentinel without importing pkg/bibtex.
var ErrBibTeXParse = bibtex.ErrParse

// ParseBibTeX is a thin wrapper preserving the existing caller API
// (core.ParseBibTeX) while the parser itself lives in pkg/bibtex.
func ParseBibTeX(input string) (*coldp.Reference, error) {
	return bibtex.Parse(input)
}
