package taxonomy

import (
	"github.com/gnames/gnlib/ent/nomcode"
)

// parseCodeID wraps nomcode.New with an empty-in/empty-out convention:
// an unset code stays unset instead of being coerced to a zero enum.
// Kept local (rather than exported from pkg root) so taxonomy stays
// import-cycle-free with pkg/. Mirrors the parseCodeID in pkg/name.go.
func parseCodeID(id string) nomcode.Code {
	if id == "" {
		return nomcode.Code(0)
	}
	return nomcode.New(id)
}
