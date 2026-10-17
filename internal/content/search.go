package content

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/i18n"
)

// minTrigram is the shortest query the trigram index answers; shorter ones scan the indexed text.
const minTrigram = 3

// searchRank orders full-text hits: title, then excerpt, then body (content_id carries no weight).
const searchRank = "bm25(content_fts_v2, 0, 10, 3, 1)"

// searchTables are the full-text indexes every write keeps in step. Queries read only
// content_fts_v2 (Arabic-normalised text); content_fts keeps the plain text for one release
// so an older binary still searches a database this one migrated.
var searchTables = [...]string{"content_fts", "content_fts_v2"}

// plainText is the searchable text of an HTML fragment: element text separated by spaces,
// entities decoded, no tags or attributes.
func plainText(fragment string) string {
	z := html.NewTokenizer(strings.NewReader(fragment))
	var b strings.Builder
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				return strings.Join(strings.Fields(fragment), " ")
			}
			return strings.Join(strings.Fields(b.String()), " ")
		case html.TextToken:
			b.Write(z.Text())
		default:
			b.WriteByte(' ')
		}
	}
}

// indexSearchTx replaces the search row of it inside the write's transaction.
func indexSearchTx(ctx context.Context, tx *sql.Tx, it *Item) error {
	if err := unindexSearchTx(ctx, tx, it.ID); err != nil {
		return err
	}
	title, excerpt, body := plainText(it.Title), plainText(it.Excerpt), plainText(it.Body)
	if _, err := tx.ExecContext(ctx, `INSERT INTO content_fts (content_id, title, excerpt, body) VALUES (?, ?, ?, ?)`,
		it.ID, title, excerpt, body); err != nil {
		return fmt.Errorf("content: search index %s: %w", it.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO content_fts_v2 (content_id, title, excerpt, body) VALUES (?, ?, ?, ?)`,
		it.ID, i18n.NormalizeArabic(title), i18n.NormalizeArabic(excerpt), i18n.NormalizeArabic(body)); err != nil {
		return fmt.Errorf("content: search index %s: %w", it.ID, err)
	}
	return nil
}

// unindexSearchTx drops the search rows of id (virtual tables have no ON DELETE CASCADE).
func unindexSearchTx(ctx context.Context, tx *sql.Tx, id string) error {
	for _, table := range searchTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE content_id = ?`, id); err != nil {
			return fmt.Errorf("content: search unindex %s: %w", id, err)
		}
	}
	return nil
}

// searchStale reports whether either index and the content table disagree on their rows.
// Every write replaces its item's rows, so an index without orphans and with one row per item
// is in step; both checks look content up by its primary key (content_id is not indexed in
// FTS5). An empty content_fts_v2 beside existing content (first boot after the migration)
// is stale, which is how it is backfilled.
func (s *service) searchStale(ctx context.Context) (bool, error) {
	for _, table := range searchTables {
		var items, rows, orphans int
		err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM content), (SELECT count(*) FROM `+table+`),
			(SELECT count(*) FROM `+table+` f WHERE NOT EXISTS (SELECT 1 FROM content c WHERE c.id = f.content_id))`).Scan(&items, &rows, &orphans)
		if err != nil {
			return false, fmt.Errorf("content: search check: %w", err)
		}
		if items != rows || orphans > 0 {
			return true, nil
		}
	}
	return false, nil
}

// rebuildSearch rewrites both search indexes from the content table.
func (s *service) rebuildSearch(ctx context.Context) error {
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		for _, table := range searchTables {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
				return fmt.Errorf("content: search reset: %w", err)
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, title, excerpt, body FROM content`)
		if err != nil {
			return fmt.Errorf("content: search list: %w", err)
		}
		var items []Item
		for rows.Next() {
			var it Item
			if err := rows.Scan(&it.ID, &it.Title, &it.Excerpt, &it.Body); err != nil {
				rows.Close()
				return err
			}
			items = append(items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range items {
			if err := indexSearchTx(ctx, tx, &items[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// ensureSearch rebuilds the index when it is out of step with the content (first boot after
// the migration, or rows written behind the service's back).
func (s *service) ensureSearch(ctx context.Context) error {
	stale, err := s.searchStale(ctx)
	if err != nil || !stale {
		return err
	}
	s.log.Info("content: rebuilding the search index")
	return s.rebuildSearch(ctx)
}

// addSearch adds the search condition to b and reports whether it ranks (a full-text match).
// The query is one literal phrase: a substring of the plain text, case-insensitive, with
// Arabic folded the way the indexed text is (i18n.NormalizeArabic). Results are read from
// the content table, so they show the stored text, never the folded one. A query that folds
// to nothing (only harakat or tatweel) matches nothing.
func addSearch(b *queryBuilder, q string) bool {
	q = i18n.NormalizeArabic(q)
	if q == "" {
		b.add("0")
		return false
	}
	if utf8.RuneCountInString(q) < minTrigram {
		pat := "%" + likeEscape(q) + "%"
		b.add(`c.id IN (SELECT content_id FROM content_fts_v2 WHERE title LIKE ? ESCAPE '\' OR excerpt LIKE ? ESCAPE '\' OR body LIKE ? ESCAPE '\')`,
			pat, pat, pat)
		return false
	}
	b.joins = append(b.joins, "JOIN content_fts_v2 ON content_fts_v2.content_id = c.id")
	b.add("content_fts_v2 MATCH ?", `"`+strings.ReplaceAll(q, `"`, `""`)+`"`)
	return true
}
