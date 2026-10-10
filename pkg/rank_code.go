package hive

import "strings"

// codeFreeRanks are the ranks whose names no nomenclatural code
// governs: those above kingdom (Eukaryota, Bacteria as a domain) and
// unranked taxa such as clades.
var codeFreeRanks = map[string]bool{
	"SUPERDOMAIN": true, "DOMAIN": true, "REALM": true, "SUBREALM": true,
	"EMPIRE": true, "SUPERKINGDOM": true, "UNRANKED": true,
}

// CodeOptional reports whether a name of the given rank may be saved
// without a nomenclatural code. Every other rank needs one.
func CodeOptional(rankID string) bool {
	return codeFreeRanks[strings.ToUpper(strings.TrimSpace(rankID))]
}
