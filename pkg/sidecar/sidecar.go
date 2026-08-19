// Package sidecar manages hive's per-archive sidecar directory —
// the on-disk companion to a `.db` archive that holds attached
// source documents (PDF / EPUB / HTML), their canonicalized JATS
// conversions, curator annotations, and provenance metadata.
//
// For an archive at `path/to/archive.db`, the sidecar lives at
// `path/to/archive.pdfs/` and files inside are content-addressed
// on `sha512(source)`. Same document uploaded twice deduplicates.
// See REFERENCE_PDF_PLAN.md § Sidecar directory layout for the
// full layout contract.
//
// The package is deliberately I/O-only: no SQL, no HTTP, no
// GROBID client. Callers (mostly the HTTP upload handler) compose
// sidecar with those other concerns.
package sidecar

import (
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DirSuffix is the suffix appended to the archive path (with .db
// stripped) to form the sidecar directory. Kept as a var so tests
// or future flavors could rename without hunting hardcoded strings.
var DirSuffix = ".pdfs"

// Meta is the JSON shape written to <sha>.meta.json alongside every
// ingested file. Provenance data — where the file came from, when
// it landed, what tool + version did the ingest. Grows over time
// as the pipeline adds stages (JATS conversion tool + version,
// OCR engine + language, etc.); JSON is additive, older meta files
// stay readable.
type Meta struct {
	SHA512     string    `json:"sha512"`
	Ext        string    `json:"ext"`
	Size       int64     `json:"size"`
	MimeType   string    `json:"mime_type,omitempty"`
	SourceURL  string    `json:"source_url,omitempty"`
	UploadedAt time.Time `json:"uploaded_at"`
}

// IngestResult is what Ingest returns on success. Dedup is
// signaled via AlreadyExisted so callers can skip work that
// would otherwise re-run (JATS conversion, DOI extraction).
type IngestResult struct {
	SHA512         string
	Ext            string
	Size           int64
	AlreadyExisted bool
	Path           string
}

// Dir returns the sidecar directory path for a given archive.
// Strips `.db` from the archive name (case-insensitive) and
// appends DirSuffix. Doesn't touch the filesystem — pure path
// computation.
func Dir(archivePath string) string {
	base := archivePath
	if strings.EqualFold(filepath.Ext(base), ".db") {
		base = base[:len(base)-3]
	}
	return base + DirSuffix
}

// Path returns the full sidecar path for a stored file. Doesn't
// verify existence — callers use Exists for that.
func Path(archivePath, sha, ext string) string {
	name := sha
	if ext != "" {
		name = sha + "." + strings.TrimPrefix(ext, ".")
	}
	return filepath.Join(Dir(archivePath), name)
}

// MetaPath returns the sidecar path for a file's meta.json.
func MetaPath(archivePath, sha string) string {
	return filepath.Join(Dir(archivePath), sha+".meta.json")
}

// Exists reports whether the sidecar file for (sha, ext) is on
// disk. Cheap — a single Stat call.
func Exists(archivePath, sha, ext string) (bool, error) {
	_, err := os.Stat(Path(archivePath, sha, ext))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// Ingest streams src into the sidecar, hashing on the fly, and
// stores the file at <archive>.pdfs/<sha512>.<ext>. Also writes
// a Meta record to <sha>.meta.json.
//
// Dedup: if a file with the same sha already exists, Ingest
// returns that file's path with AlreadyExisted=true and doesn't
// rewrite. The uploaded stream is drained but its bytes are
// discarded after the hash matches (SHA collisions are
// astronomical — treating them as identical content is safe).
//
// ext is the file extension without the leading dot ("pdf",
// "epub", "html"). Callers derive it from the upload's original
// filename or MIME type.
//
// SourceURL is optional provenance — set when the file came from
// a download the curator initiated (e.g., DOI-resolved PDF from a
// publisher URL), empty when the curator uploaded a local file.
func Ingest(archivePath string, src io.Reader, ext, mimeType, sourceURL string) (*IngestResult, error) {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	dir := Dir(archivePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("sidecar: create dir %s: %w", dir, err)
	}

	// Stream to a temp file in the sidecar dir so a rename onto the
	// final path is atomic (same filesystem). Hash as we go — one
	// pass over the bytes, no separate rewind.
	tmp, err := os.CreateTemp(dir, ".ingest-*")
	if err != nil {
		return nil, fmt.Errorf("sidecar: temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanupTmp := func() {
		tmp.Close()
		os.Remove(tmpPath)
	}

	hasher := sha512.New()
	written, err := io.Copy(io.MultiWriter(tmp, hasher), src)
	if err != nil {
		cleanupTmp()
		return nil, fmt.Errorf("sidecar: stream file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("sidecar: close temp file: %w", err)
	}

	sha := hex.EncodeToString(hasher.Sum(nil))
	finalPath := Path(archivePath, sha, ext)

	exists, err := Exists(archivePath, sha, ext)
	if err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("sidecar: check exists: %w", err)
	}
	if exists {
		// Content already stored — drop the tmp, return the
		// existing path so callers can skip re-processing.
		os.Remove(tmpPath)
		return &IngestResult{
			SHA512:         sha,
			Ext:            ext,
			Size:           written,
			AlreadyExisted: true,
			Path:           finalPath,
		}, nil
	}

	// Move tmp into place atomically. Rename is atomic on POSIX
	// (same filesystem, which we guaranteed by creating tmp in the
	// sidecar dir).
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("sidecar: rename to %s: %w", finalPath, err)
	}

	meta := Meta{
		SHA512:     sha,
		Ext:        ext,
		Size:       written,
		MimeType:   mimeType,
		SourceURL:  sourceURL,
		UploadedAt: time.Now().UTC(),
	}
	if err := writeMeta(archivePath, sha, &meta); err != nil {
		// Meta failure isn't fatal — the file is safely stored.
		// Callers get the ingest result with a hint via the error;
		// in the current design we treat this as advisory only.
		// Return the result; caller decides.
		return &IngestResult{
			SHA512:         sha,
			Ext:            ext,
			Size:           written,
			AlreadyExisted: false,
			Path:           finalPath,
		}, fmt.Errorf("sidecar: write meta: %w", err)
	}
	return &IngestResult{
		SHA512:         sha,
		Ext:            ext,
		Size:           written,
		AlreadyExisted: false,
		Path:           finalPath,
	}, nil
}

// ReadMeta loads the meta.json for a stored file, or nil (no
// error) when the meta is absent. Callers should tolerate absent
// meta — early archives may have been ingested before meta was
// stored.
func ReadMeta(archivePath, sha string) (*Meta, error) {
	path := MetaPath(archivePath, sha)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("sidecar: read meta %s: %w", path, err)
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("sidecar: parse meta %s: %w", path, err)
	}
	return &m, nil
}

func writeMeta(archivePath, sha string, m *Meta) error {
	path := MetaPath(archivePath, sha)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Store writes a derived artifact (e.g., TEI XML from GROBID,
// JATS XML from the TEI-to-JATS converter) alongside the source
// file. Path is <sha>.<suffix> (suffix like "tei.xml" or
// "jats.xml"). Overwrites if the target exists — derivations are
// re-runnable and the latest is the source of truth.
func Store(archivePath, sha, suffix string, data []byte) error {
	suffix = strings.TrimPrefix(suffix, ".")
	if err := os.MkdirAll(Dir(archivePath), 0o755); err != nil {
		return fmt.Errorf("sidecar: create dir: %w", err)
	}
	path := filepath.Join(Dir(archivePath), sha+"."+suffix)
	return os.WriteFile(path, data, 0o644)
}

// Read loads a stored derivation by (sha, suffix). Returns
// (nil, nil) when the file doesn't exist; (nil, err) on other
// errors. Callers use this to check whether GROBID has already
// processed a given source, whether JATS conversion has run, etc.
func Read(archivePath, sha, suffix string) ([]byte, error) {
	suffix = strings.TrimPrefix(suffix, ".")
	path := filepath.Join(Dir(archivePath), sha+"."+suffix)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("sidecar: read %s: %w", path, err)
	}
	return data, nil
}
