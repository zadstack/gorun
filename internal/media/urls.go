package media

import (
	"context"
	"fmt"
	"strings"
)

// MediaURLs maps the given media ids to their URLs (content exports use it through
// content.MediaURLResolver); unknown ids are left out. Ids are looked up in chunks of 100.
func (s *service) MediaURLs(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	for start := 0; start < len(ids); start += 100 {
		chunk := ids[start:min(start+100, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id, url FROM media WHERE id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("media: urls: %w", err)
		}
		for rows.Next() {
			var id, url string
			if err := rows.Scan(&id, &url); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = url
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
