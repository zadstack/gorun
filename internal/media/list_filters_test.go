package media

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLibraryFiltersComposeSearchTypeSizeAndPagination(t *testing.T) {
	s, _ := testService(t, false)
	ctx := context.Background()
	image, e := s.Upload(ctx, "same-match.jpg", bytes.NewReader(jpegBytes(t, 10, 10)), UploadMeta{})
	if e != nil {
		t.Fatal(e)
	}
	document, e := s.Upload(ctx, "same-match.pdf", strings.NewReader("%PDF-1.4\nhello"), UploadMeta{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE media SET size=? WHERE id=?`, 2<<20, image.ID); e != nil {
		t.Fatal(e)
	}
	for _, x := range []struct {
		query ListQuery
		total int
		id    string
	}{{ListQuery{Search: "match", MimePrefix: "image/", MinSize: 1 << 20}, 1, image.ID}, {ListQuery{Search: "match", MimePrefix: "application/", MaxSize: (1 << 20) - 1}, 1, document.ID}, {ListQuery{Search: "match", MimePrefix: "image/", MaxSize: (1 << 20) - 1}, 0, ""}, {ListQuery{Search: "missing", MimePrefix: "image/", MinSize: 1 << 20}, 0, ""}, {ListQuery{MimePrefix: "%' OR 1=1 --"}, 0, ""}, {ListQuery{Search: "match", PerPage: 1, Page: 2}, 2, ""}} {
		rows, total, e := s.List(ctx, x.query)
		if e != nil || total != x.total {
			t.Fatal("count predicate mismatch", x.query, total, e)
		}
		if x.id != "" && (len(rows) != 1 || rows[0].ID != x.id) {
			t.Fatal("filtered row mismatch", x.query, rows)
		}
	}
}
