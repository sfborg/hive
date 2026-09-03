package jatsconv

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sfborg/hive/schema"
)

// ValidationResult reports whether a JATS document validates
// against hive's Publishing-1.3 + tp:/gn: overlay. Zero-value =
// unvalidated; check Executed before reading Valid.
type ValidationResult struct {
	// Executed is false when xmllint isn't installed on this
	// host. Callers should treat it as "we don't know" — not as
	// an implicit pass. The validator inside the JATS-validation
	// rule uses this to silently skip when xmllint is missing.
	Executed bool
	// Valid is true when xmllint reported no errors.
	Valid bool
	// Errors carries the first N (~5) error lines from xmllint,
	// one per element. Empty when Valid.
	Errors []string
}

// Validate runs xmllint against `jats` using hive's embedded RNG
// tree (see schema.FS). Returns Executed=false when xmllint isn't
// available on PATH — callers decide how to handle that case
// (usually: silent skip). All other errors surface as validator
// output in the Errors slice.
//
// Thread-safe. The embedded schema files are extracted to a
// temporary directory once per process (via sync.Once); subsequent
// calls reuse it. Nothing writes to disk aside from that one-time
// extraction.
func Validate(jats []byte) (ValidationResult, error) {
	xmllint, err := exec.LookPath("xmllint")
	if err != nil {
		return ValidationResult{Executed: false}, nil
	}
	schemaPath, err := ensureSchemaOnDisk()
	if err != nil {
		return ValidationResult{}, fmt.Errorf("jatsconv: extract schema: %w", err)
	}
	cmd := exec.Command(xmllint, "--noout", "--relaxng", schemaPath, "-")
	cmd.Stdin = bytes.NewReader(jats)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return ValidationResult{Executed: true, Valid: true}, nil
	}
	// xmllint returns non-zero on validation failure. Capture the
	// first few error lines — enough to identify the problem
	// without ballooning the row's `message` field in
	// __gsvalidator_results.
	return ValidationResult{
		Executed: true,
		Valid:    false,
		Errors:   firstErrorLines(string(out), 5),
	}, nil
}

// firstErrorLines returns the first n lines that look like
// xmllint validation errors — filtering out the file-fails-to-
// validate summary line and blank lines that don't carry useful
// context. Order is preserved to keep the report deterministic.
func firstErrorLines(stderr string, n int) []string {
	var out []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "fails to validate") {
			continue
		}
		out = append(out, line)
		if len(out) >= n {
			break
		}
	}
	return out
}

var (
	extractOnce sync.Once
	extractDir  string
	extractErr  error
)

// ensureSchemaOnDisk unpacks schema.FS into a per-process temp
// directory and returns the on-disk path to hive-jats.rng. RelaxNG
// includes reference sibling files by relative name, so all module
// files have to land in one directory tree before xmllint reads
// them.
func ensureSchemaOnDisk() (string, error) {
	extractOnce.Do(func() {
		dir, err := os.MkdirTemp("", "hive-jats-schema-*")
		if err != nil {
			extractErr = err
			return
		}
		extractErr = fs.WalkDir(schema.FS, ".", func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(dir, path), 0o755)
			}
			data, err := schema.FS.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, path), data, 0o644)
		})
		if extractErr != nil {
			_ = os.RemoveAll(dir)
			return
		}
		extractDir = dir
	})
	if extractErr != nil {
		return "", extractErr
	}
	return filepath.Join(extractDir, "hive-jats.rng"), nil
}
