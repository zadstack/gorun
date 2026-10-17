package media

import (
	"context"
	"io"
)

type Media struct {
	ID         string             `json:"id"`
	Filename   string             `json:"filename"`
	StorageKey string             `json:"storage_key"`
	URL        string             `json:"url"`
	Mime       string             `json:"mime"`
	Size       int64              `json:"size"`
	Width      int                `json:"width"`
	Height     int                `json:"height"`
	Alt        string             `json:"alt"`
	Caption    string             `json:"caption"`
	Title      string             `json:"title"`
	AuthorID   string             `json:"author_id"`
	Variants   map[string]Variant `json:"variants"`
	SourceURL  string             `json:"source_url"`
	Checksum   string             `json:"checksum"`
	CreatedAt  string             `json:"created_at"`
}

type Variant struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type Service interface {
	Upload(ctx context.Context, filename string, r io.Reader, meta UploadMeta) (*Media, error)
	FromURL(ctx context.Context, url string, meta UploadMeta) (*Media, error) // SSRF-safe, dedupes by source_url
	Get(ctx context.Context, id string) (*Media, error)
	List(ctx context.Context, q ListQuery) ([]Media, int, error)
	Update(ctx context.Context, id string, patch Patch) (*Media, error)
	Delete(ctx context.Context, id string) error
	URL(m *Media, variant string) string
	FindBySourceURL(ctx context.Context, url string) (*Media, error)
}

type UploadMeta struct {
	GeneratedContentID string `json:"generated_content_id,omitempty"`
	GeneratedKind      string `json:"generated_kind,omitempty"`
	Alt                string `json:"alt"`
	Caption            string `json:"caption"`
	Title              string `json:"title"`
	AuthorID           string `json:"author_id"`
	SourceURL          string `json:"source_url"`
}

type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, mime string) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	URL(key string) string
}

// ListQuery filters Service.List.
type ListQuery struct {
	Search  string `json:"search"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
	Sort    string `json:"sort"`
	Order   string `json:"order"`
}

// Patch updates only its non-nil fields.
type Patch struct {
	Alt     *string `json:"alt,omitempty"`
	Caption *string `json:"caption,omitempty"`
	Title   *string `json:"title,omitempty"`
}
