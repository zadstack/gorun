package redirects

import "context"

type Redirect struct {
	ID        string `json:"id"`
	FromPath  string `json:"from_path"`
	ToURL     string `json:"to_url"`
	Status    int    `json:"status"`
	Hits      int    `json:"hits"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

type Service interface {
	Lookup(ctx context.Context, path string) (*Redirect, bool)
	List(ctx context.Context, q ListQuery) ([]Redirect, int, error)
	Save(ctx context.Context, r Redirect) (*Redirect, error)
	Delete(ctx context.Context, id string) error
	Hit(ctx context.Context, id string)
}

// ListQuery filters Service.List.
type ListQuery struct {
	Search  string `json:"search"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
	Sort    string `json:"sort"`
	Order   string `json:"order"`
}
