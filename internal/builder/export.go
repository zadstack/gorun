package builder

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// LayoutExport is one page's builder documents for a site export (gorun
// export): the published layout, and the private draft when it differs.
type LayoutExport struct {
	ContentID string    `json:"content_id"`
	Published *Document `json:"published,omitempty"`
	Draft     *Document `json:"draft,omitempty"`
}

// ExportLayouts reads every builder document straight from the database:
// the export runs as the operator, outside a request, with no principal
// to check. Drafts equal to the published layout are left out.
func ExportLayouts(ctx context.Context, db *sql.DB) ([]LayoutExport, error) {
	rows, err := db.QueryContext(ctx, `SELECT content_id, document_json, published_document_json FROM builder_documents ORDER BY content_id`)
	if err != nil {
		return nil, fmt.Errorf("builder: export layouts: %w", err)
	}
	defer rows.Close()
	var out []LayoutExport
	for rows.Next() {
		var id, draftRaw, publishedRaw string
		if err := rows.Scan(&id, &draftRaw, &publishedRaw); err != nil {
			return nil, err
		}
		var draft Document
		if err := json.Unmarshal([]byte(draftRaw), &draft); err != nil {
			return nil, fmt.Errorf("builder: export layouts: %s: %w", id, err)
		}
		e := LayoutExport{ContentID: id}
		if publishedRaw != "null" && publishedRaw != "" {
			var published Document
			if err := json.Unmarshal([]byte(publishedRaw), &published); err != nil {
				return nil, fmt.Errorf("builder: export layouts: %s: %w", id, err)
			}
			e.Published = &published
			if !Diff(published, draft).Empty() {
				e.Draft = &draft
			}
		} else {
			e.Draft = &draft
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
