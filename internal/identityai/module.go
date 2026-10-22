// Package identityai is an optional module that asks a configured text AI
// connection for identity suggestions (palette, fonts, taglines, a logo
// brief) and writes them to the Appearance → Identity draft, never live.
// It produces text only: no image, vector or GIF is ever generated or
// claimed.
package identityai

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"io/fs"
	"net/http"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

//go:embed admin/templates/pages/identityai/*.html catalog/*.json
var files embed.FS

const (
	// SettingEnabled switches the module (kernel.AddonEnabled): false turns it off.
	SettingEnabled = "addons.identityai.enabled"
	// PermGenerate is the permission the page requires (the same as the identity page).
	PermGenerate = "settings.manage"
	pagePath     = "/appearance/identity/ai"
)

// ErrDestinationChanged is what Hooks.Complete returns when the destination
// differs from the reviewed fingerprint; the module then sends nothing and
// records nothing.
var ErrDestinationChanged = errors.New("identityai: destination changed since it was reviewed")

// Connection describes an AI connection profile for display: never a secret.
type Connection struct {
	ID, Name, Provider, Model, Host, TestStatus string
}

// Hooks are the admin-owned AI functions the module depends on, injected
// at boot (like workagents), so this package never reads credentials.
type Hooks struct {
	// Complete sends one system + user turn through connectionID (""
	// means the site's writing settings) with at most maxTokens of reply
	// and returns the reply text. It must refuse with ErrDestinationChanged
	// when the destination no longer matches expectFingerprint.
	Complete func(ctx context.Context, d *kernel.Deps, connectionID, model, system, input string, maxTokens int, expectFingerprint string) (string, error)
	// Ready reports that the connection's configuration and credential are
	// readable, without sending a request.
	Ready func(ctx context.Context, d *kernel.Deps, connectionID, model string) bool
	// Connections lists the saved AI connection profiles.
	Connections func(ctx context.Context, d *kernel.Deps) ([]Connection, error)
	// Writing describes the site's writing settings as a destination
	// (provider, model, host); false when not configured.
	Writing func(ctx context.Context, d *kernel.Deps) (Connection, bool)
	// Fingerprint identifies the exact destination a request would go to
	// right now: provider, endpoint, model, connection version and the
	// stored credential (hashed, never exposed). connectionID "" is the
	// writing settings. The approval given at preview is valid only while
	// the fingerprint is unchanged.
	Fingerprint func(ctx context.Context, d *kernel.Deps, connectionID string) (string, bool)
}

// IdentityStore is what the module needs from the theme service.
type IdentityStore interface {
	Active(ctx context.Context) theme.Info
	LiveIdentity(ctx context.Context, slug string) theme.IdentitySnapshot
	IdentityDraft(ctx context.Context, slug string) (theme.IdentityDraft, bool)
	SaveIdentityDraft(ctx context.Context, snap theme.IdentitySnapshot) error
}

// Module is the identityai kernel.Module.
type Module struct{ hooks Hooks }

// New returns the module with its AI hooks.
func New(h Hooks) *Module { return &Module{hooks: h} }

func (*Module) Name() string      { return "identityai" }
func (*Module) Migrations() fs.FS { return nil }
func (*Module) Catalogs() fs.FS   { return files }

// Enabled implements kernel.OptionalModule through the shared addon gate:
// on unless addons.identityai.enabled is false (System → Modules), like the
// other optional modules. Being on only adds the page; nothing is sent to
// a provider without a reviewed, approved request.
func (*Module) Enabled(ctx context.Context, d *sql.DB) (bool, error) {
	return kernel.AddonEnabled(ctx, d, "identityai")
}

// Register mounts the page and its navigation entry.
func (m *Module) Register(d *kernel.Deps) error {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return errors.New("identityai: admin templates unavailable")
	}
	if err := tpl.RegisterExtension(files); err != nil {
		return err
	}
	s := newService(d, m.hooks)
	h := &handlers{s: s, tpl: tpl}
	d.Admin.AddNav(kernel.NavItem{ID: "appearance.identity_ai", LabelKey: "identityai.nav", Icon: "sparkles", Href: "/admin" + pagePath, Permission: PermGenerate, Group: "appearance", Position: 2})
	r := d.Router.Admin.With(auth.RequirePerm(PermGenerate))
	r.Get(pagePath, h.page)
	r.Post(pagePath, h.submit)
	return nil
}

var _ kernel.OptionalModule = (*Module)(nil)

type handlers struct {
	s   *service
	tpl *httpx.Templates
}

func (h *handlers) page(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, h.s.pageData(r.Context(), Brief{}, nil, ""))
}
