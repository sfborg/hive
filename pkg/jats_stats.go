package hive

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sfborg/hive/pkg/refmanager/pdftext"
	"github.com/sfborg/hive/pkg/refmanager/sidecar"
)

// JATSStats is the size of one attached file's JATS sidecar.
type JATSStats struct {
	PDFPages   int // 0 when unknown
	Words      int
	Characters int
}

var (
	statsCommentRegex = regexp.MustCompile(`(?s)<!--.*?-->`)
	statsTagRegex     = regexp.MustCompile(`<[^>]+>`)
)

// MeasureJATS counts the words in a JATS document's text and the
// characters in the whole file. Comments, which hold text removed by
// corrections, are not counted as words; characters count the file
// as sent to an extraction model, markup included.
func MeasureJATS(jats []byte) (words, characters int) {
	text := statsCommentRegex.ReplaceAll(jats, []byte(" "))
	text = statsTagRegex.ReplaceAll(text, []byte(" "))
	words = len(strings.Fields(html.UnescapeString(string(text))))
	return words, utf8.RuneCount(jats)
}

// StoreJATS writes a JATS sidecar and refreshes its size counts. Every
// write of a jats.xml sidecar goes through here so the counts in the
// reference list stay current.
func (a *Archive) StoreJATS(ctx context.Context, sha string, jats []byte) error {
	if err := sidecar.Store(a.path, sha, "jats.xml", jats); err != nil {
		return err
	}
	return a.refreshJATSStats(ctx, sha, jats)
}

// refreshJATSStats records the counts for sha. The PDF page count is
// read once and kept, since the PDF does not change.
func (a *Archive) refreshJATSStats(ctx context.Context, sha string, jats []byte) error {
	words, chars := MeasureJATS(jats)
	var pages sql.NullInt64
	err := a.db.QueryRowContext(ctx,
		`SELECT pdf_pages FROM hive__jats_stats WHERE sha512 = ?`, sha).Scan(&pages)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("core: read jats stats %s: %w", sha, err)
	}
	if !pages.Valid {
		pdfPath := sidecar.Path(a.path, sha, "pdf")
		if _, statErr := os.Stat(pdfPath); statErr == nil {
			if n, err := pdftext.PageCount(pdfPath); err == nil {
				pages = sql.NullInt64{Int64: int64(n), Valid: true}
			}
		}
	}
	return a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(tx.ctx,
			`INSERT INTO hive__jats_stats (sha512, pdf_pages, words, characters, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (sha512) DO UPDATE SET
			   pdf_pages = COALESCE(hive__jats_stats.pdf_pages, excluded.pdf_pages),
			   words = excluded.words, characters = excluded.characters,
			   updated_at = excluded.updated_at`,
			sha, pages, words, chars, time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			return fmt.Errorf("core: store jats stats %s: %w", sha, err)
		}
		return nil
	})
}

// BackfillJATSStats records counts for attached files whose JATS
// sidecar exists but has none yet. Returns how many were filled.
func (a *Archive) BackfillJATSStats(ctx context.Context) (int, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT DISTINCT p.sha512 FROM hive__reference_pdf p
		 LEFT JOIN hive__jats_stats s ON s.sha512 = p.sha512
		 WHERE s.sha512 IS NULL`)
	if err != nil {
		return 0, fmt.Errorf("core: list files without jats stats: %w", err)
	}
	var shas []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err == nil {
			shas = append(shas, sha)
		}
	}
	rows.Close()
	n := 0
	for _, sha := range shas {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		jats, err := sidecar.Read(a.path, sha, "jats.xml")
		if err != nil || len(jats) == 0 {
			continue
		}
		if err := a.refreshJATSStats(ctx, sha, jats); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
