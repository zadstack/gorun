package builder

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed migrations/*.sql static/* catalog/*.json
var files embed.FS

type Module struct{}

func New() *Module                { return &Module{} }
func (*Module) Name() string      { return "builder" }
func (*Module) Migrations() fs.FS { return files }
func (*Module) Catalogs() fs.FS   { return files }
func (*Module) Register(d *kernel.Deps) error {
	svc := &service{d: d}
	registerLayoutAPI(d, svc)
	d.Services.Set("builder", svc)
	d.Router.Public.Get("/static/builder/builder.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if _, err := w.Write([]byte(builderCSS)); err != nil {
			d.Log.Debug("builder stylesheet write", "err", err)
		}
	})
	for _, asset := range []string{"hero-scenes.css", "hero-scenes.js", "three.module.min.js", "three.core.min.js"} {
		asset := asset
		d.Router.Public.Get("/static/builder/"+asset, func(w http.ResponseWriter, r *http.Request) {
			if asset == "hero-scenes.css" {
				w.Header().Set("Content-Type", "text/css; charset=utf-8")
			} else {
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			}
			w.Header().Set("Cache-Control", "public, max-age=3600")
			body, err := files.ReadFile("static/" + asset)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
		})
	}
	if err := registerAdmin(d, svc); err != nil {
		return err
	}
	if err := registerIdentity(d); err != nil {
		return err
	}
	if err := registerReadiness(d); err != nil {
		return err
	}
	return registerStarters(d, svc)
}

//go:embed static/builder.css
var builderBaseCSS string

//go:embed static/hero-scenes.css
var heroScenesCSS string

// The complete inline preview cascade is generated from the same public assets.
var builderCSS = builderBaseCSS + "\n" + heroScenesCSS
