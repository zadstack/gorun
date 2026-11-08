package seo

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/zadstack/gorun/internal/db"
)

// Only domain writes invalidate snapshots. Queue heartbeats, access logs and
// redirect hit counters must not turn cached sitemap reads into rebuilds.
func installSitemapTriggers(ctx context.Context, database *sql.DB) error {
	tables := []string{"content", "content_types", "content_page_access", "content_relations", "taxonomies", "terms", "content_terms", "settings", "redirects"}
	return db.InTx(ctx, database, func(tx *sql.Tx) error {
		for _, table := range tables {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, table).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				continue
			} // Minimal embedded installations may omit domain modules.
			for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
				clause := event
				if table == "redirects" && event == "UPDATE" {
					clause += " OF from_path,to_url,status,enabled"
				}
				statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS seo_sitemap_%s_%s AFTER %s ON %s BEGIN UPDATE seo_sitemap_revision SET revision=revision+1 WHERE id=1; END`, table, event, clause, table)
				if _, err := tx.ExecContext(ctx, statement); err != nil {
					return fmt.Errorf("seo: invalidation trigger: %w", err)
				}
			}
		}
		return nil
	})
}
