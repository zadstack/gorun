package media

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed catalog/*.json
var catalogs embed.FS

// FetchJobKind downloads a URL into the library (payload FetchJob). The
// service's FromURL is synchronous; this job lets callers do it in the
// background.
const FetchJobKind = "media.fetch_url"

// FetchJob is the payload of FetchJobKind.
type FetchJob struct {
	URL  string     `json:"url"`
	Meta UploadMeta `json:"meta"`
}

// Module is the media kernel.Module.
type Module struct{}

// New returns the media module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "media" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// NewService builds the media service over storage (used by Register and by
// tools that need the library outside a booted app).
func NewService(d *kernel.Deps, storage Storage) Service { return newService(d, storage) }

func newService(d *kernel.Deps, storage Storage) *service {
	return &service{db: d.DB, storage: storage, settings: d.Settings, hooks: d.Hooks, audit: d.Audit, log: d.Log,
		client: httpx.NewSafeClient(d.Config.AllowPrivateNet), now: time.Now}
}

// Register publishes kernel.ServiceMedia, serves GET /media/*, and adds the
// admin library, picker, API operations and the fetch job.
func (*Module) Register(d *kernel.Deps) error {
	storage, err := newStorage(d)
	if err != nil {
		return err
	}
	storage = &ManagedStorage{db: d.DB, settings: d.Settings, secret: d.Config.SecretKey, fallback: storage}
	svc := newService(d, storage)
	d.Services.Set(kernel.ServiceMedia, Service(svc))

	d.Router.Public.Get(URLPrefix+"*", serveHandler(storage))

	d.Jobs.Register(FetchJobKind, func(ctx context.Context, p json.RawMessage) error {
		var job FetchJob
		if err := json.Unmarshal(p, &job); err != nil {
			return err
		}
		_, err := svc.FromURL(ctx, job.URL, job.Meta)
		if errors.Is(err, ErrNotAllowed) || errors.Is(err, ErrTooLarge) {
			d.Log.Warn("media: fetch rejected", "err", err)
			return nil // permanent: retrying cannot help
		}
		return err
	}, 3)

	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return fmt.Errorf("media: admin templates not registered (auth module must boot first)")
	}
	h := &adminHandlers{svc: svc, tpl: tpl, log: d.Log}
	upload := d.Router.Admin.With(auth.RequirePerm("media.upload"))
	upload.Get("/media", h.library)
	upload.Post("/media", h.upload)
	upload.Get("/media/{id}", h.edit)
	upload.Post("/media/{id}", h.update)
	upload.Get("/x/media/picker", h.picker)
	d.Router.Admin.Get("/x/media/{id}/thumb", h.thumb) // permission checked in the handler
	d.Router.Admin.With(auth.RequirePerm("media.delete")).Post("/media/{id}/delete", h.remove)
	d.Admin.AddNav(kernel.NavItem{ID: "media", LabelKey: "media.nav", Icon: "image", Href: "/admin/media",
		Permission: "media.upload", Group: "content", Position: 50})

	registerAPI(d, svc)
	return nil
}

// newStorage is the configured media driver: a bucket when [s3] is set (files already on disk
// keep being served from it), else the data directory's media/ folder.
func newStorage(d *kernel.Deps) (Storage, error) {
	local := NewLocalStorage(filepath.Join(d.Config.DataDir, "media"))
	c := d.Config.S3
	cfg := S3Config{Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, AccessKey: c.AccessKey, SecretKey: c.SecretKey, PublicURL: c.PublicURL}
	if !cfg.Enabled() {
		return local, nil
	}
	s3, err := NewS3Storage(cfg, local, d.Config.Env == "dev")
	if err != nil {
		return nil, err
	}
	d.Log.Info("media: storing new files in the s3 bucket", "endpoint", s3.endpoint.Host, "bucket", cfg.Bucket)
	return s3, nil
}
