// mknomen extracts NOMEN_URI + gbif_status constants from a checked-out
// TaxonWorks source tree and emits nomen_tw.json — the "authoritative
// partition" of NOMEN URIs that hive uses to filter its status picker
// (classifications only) and to downcast to CoLDP-generalized values on
// export.
//
// Not built into the hive binary. Run manually when the TW source
// changes and commit the resulting JSON alongside nomen.owl.
//
// Usage:  go run ./tools/mknomen <taxonworks-checkout>
//
// Output goes to pkg/nomen_tw.json.
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Entry is one row in nomen_tw.json — a NOMEN URI paired with the TW
// metadata hive cares about. Kind partitions statuses from relations
// so the picker on `name` shows only statuses and the (future) picker
// on `name_relation` shows only relations.
type Entry struct {
	URI    string `json:"uri"`
	Kind   string `json:"kind"` // "classification" | "relationship"
	Class  string `json:"class"`
	Gbif   string `json:"gbif_status,omitempty"`
	Ranks  string `json:"applicable_ranks,omitempty"`
	Assign string `json:"assignable,omitempty"`
}

var (
	// URI constant: NOMEN_URI = 'http://…/NOMEN_XXXXXXX'.freeze
	uriRe = regexp.MustCompile(`(?m)^\s*NOMEN_URI\s*=\s*['"]([^'"]+)['"]`)
	// Class line, capturing the leading class name — supports nested classes.
	classRe = regexp.MustCompile(`(?m)^class\s+(TaxonName\w+::[\w:]+)`)
	// def self.gbif_status ... 'value' ... end
	gbifRe = regexp.MustCompile(`def self\.gbif_status[\s\S]*?['"]([^'"\n]+)['"]`)
	// def self.assignable ... true / false ... end
	assignRe = regexp.MustCompile(`def self\.assignable[\s\S]*?(true|false)`)
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/mknomen <taxonworks-checkout>")
		os.Exit(2)
	}
	twRoot := os.Args[1]
	out := "pkg/nomen_tw.json"

	var entries []Entry
	entries = append(entries, scanKind(twRoot, "taxon_name_classification", "classification")...)
	entries = append(entries, scanKind(twRoot, "taxon_name_relationship", "relationship")...)

	// Sort by URI for a stable diff-friendly output.
	sort.Slice(entries, func(i, j int) bool { return entries[i].URI < entries[j].URI })

	f, err := os.Create(out)
	if err != nil {
		die("open %s: %v", out, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(entries); err != nil {
		die("encode: %v", err)
	}
	fmt.Printf("wrote %s (%d entries: %d classification, %d relationship)\n",
		out, len(entries), countKind(entries, "classification"), countKind(entries, "relationship"))
}

// scanKind walks one of TW's model subdirs (classification | relationship)
// and extracts every (class, NOMEN_URI, metadata) tuple. Each .rb file
// may hold more than one URI declaration (parent files often inline
// several sub-classes with their own URIs); we associate each URI with
// the nearest preceding `class Foo::Bar < ...` line so the mapping is
// correct even for those.
func scanKind(twRoot, subdir, kind string) []Entry {
	base := filepath.Join(twRoot, "app", "models", subdir)
	var out []Entry
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".rb") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, extractFromFile(string(src), kind)...)
		return nil
	})
	if err != nil {
		die("walk %s: %v", base, err)
	}
	return out
}

// extractFromFile pairs each NOMEN_URI declaration with the nearest
// preceding class definition in the same file. Metadata blocks
// (gbif_status, assignable) attach to whichever class they're inside;
// we approximate by scanning the whole file with anchored regexes,
// which is fine for TW's flat single-class-per-file convention.
func extractFromFile(src, kind string) []Entry {
	classMatches := classRe.FindAllStringSubmatchIndex(src, -1)
	uriMatches := uriRe.FindAllStringSubmatchIndex(src, -1)
	gbifMatch := gbifRe.FindStringSubmatch(src)
	assignMatch := assignRe.FindStringSubmatch(src)

	// Build a list of (offset, class-name).
	type cls struct {
		offset int
		name   string
	}
	var classes []cls
	for _, cm := range classMatches {
		classes = append(classes, cls{cm[0], src[cm[2]:cm[3]]})
	}
	// Nearest preceding class lookup.
	classAt := func(offset int) string {
		best := ""
		for _, c := range classes {
			if c.offset <= offset {
				best = c.name
			} else {
				break
			}
		}
		return best
	}

	var out []Entry
	for _, um := range uriMatches {
		uri := src[um[2]:um[3]]
		entry := Entry{
			URI:   uri,
			Kind:  kind,
			Class: classAt(um[0]),
		}
		// gbif_status / assignable currently apply file-wide (matches
		// how the leaf class defines them) — good enough for our
		// downcast + assignable-filter needs.
		if len(gbifMatch) > 1 {
			entry.Gbif = gbifMatch[1]
		}
		if len(assignMatch) > 1 {
			entry.Assign = assignMatch[1]
		}
		out = append(out, entry)
	}
	return out
}

func countKind(entries []Entry, kind string) int {
	n := 0
	for _, e := range entries {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func die(f string, args ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", args...)
	os.Exit(1)
}
