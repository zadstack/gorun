package commerce

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/seo"
)

const adminPerPage = 25

// adminHandlers serve the Commerce admin screens.
type adminHandlers struct {
	svc *service
	tpl *httpx.Templates
	log *slog.Logger
}

// registerAdmin mounts the Commerce screens and nav items. Orders and
// entitlements lists need commerce.view_orders; everything else
// commerce.manage.
func registerAdmin(d *kernel.Deps, s *service, tpl *httpx.Templates) {
	h := &adminHandlers{svc: s, tpl: tpl, log: d.Log}
	view := d.Router.Admin.With(auth.RequirePerm(PermViewOrders))
	view.Get("/commerce/orders", h.orders)
	view.Get("/commerce/orders/{id}", h.order)
	view.Get("/commerce/entitlements", h.entitlements)

	m := d.Router.Admin.With(auth.RequirePerm(PermManage))
	m.Post("/commerce/digital-files", h.uploadDigital)
	m.Post("/commerce/products/{id}/digital-file", h.uploadDigital)
	m.Get("/commerce/products", h.products)
	m.Get("/commerce/products/new", h.productNew)
	m.Post("/commerce/products", h.productSave)
	m.Get("/commerce/products/{id}", h.productEdit)
	m.Post("/commerce/products/{id}", h.productSave)
	m.Post("/commerce/products/{id}/delete", h.productDelete)
	m.Post("/commerce/orders/{id}/retry", h.orderRetry)
	m.Post("/commerce/requests/{id}", h.requestUpdate)
	m.Post("/commerce/entitlements/{id}/{action}", h.entitlementAction)
	m.Get("/commerce/servers", h.servers)
	m.Get("/commerce/servers/new", h.serverNew)
	m.Post("/commerce/servers", h.serverSave)
	m.Get("/commerce/servers/{id}", h.serverEdit)
	m.Post("/commerce/servers/{id}", h.serverSave)
	m.Post("/commerce/servers/{id}/delete", h.serverDelete)
	m.Post("/commerce/servers/{id}/test", h.serverTest)
	m.Get("/commerce/setup", h.setup)
	m.Get("/commerce/settings", h.settings)
	m.Post("/commerce/settings", h.settingsSave)

	nav := func(id, icon, href, perm string, pos int) {
		d.Admin.AddNav(kernel.NavItem{ID: "commerce." + id, LabelKey: "commerce.nav." + id, Icon: icon, Href: href, Permission: perm, Group: "commerce", Position: pos})
	}
	nav("setup", "rocket", "/admin/commerce/setup", PermManage, 295)
	nav("products", "package", "/admin/commerce/products", PermManage, 300)
	nav("orders", "receipt", "/admin/commerce/orders", PermViewOrders, 310)
	nav("entitlements", "key-round", "/admin/commerce/entitlements", PermViewOrders, 320)
	nav("servers", "server", "/admin/commerce/servers", PermManage, 330)
	nav("settings", "settings-2", "/admin/commerce/settings", PermManage, 340)
}

func (h *adminHandlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	h.log.Error("commerce: admin", "err", err, "path", httpx.RedactPath(r.URL.Path))
	h.tpl.RenderError(w, r, http.StatusInternalServerError)
}

func pageParam(r *http.Request) int {
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	return max(p, 1)
}

// productRow is a product in the admin list.
type productRow struct {
	Product
	Price, ServerName string
	DigitalReady      bool
}

func (h *adminHandlers) products(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Products(r.Context(), false)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	names := h.serverNames(r)
	rows := make([]productRow, len(list))
	for i, p := range list {
		rows[i] = productRow{Product: p, Price: FormatPrice(p.PriceMinor, p.Currency) + " " + p.Currency, ServerName: names[p.ServerID], DigitalReady: p.Kind == ProductDigital && h.svc.digitalFileExists(p.DigitalFile)}
	}
	h.tpl.Render(w, r, "pages/commerce/products.html", map[string]any{"Products": rows})
}

func (h *adminHandlers) serverNames(r *http.Request) map[string]string {
	out := map[string]string{}
	servers, _ := h.svc.Servers(r.Context())
	for _, s := range servers {
		out[s.ID] = s.Name
	}
	return out
}

// productForm is the product editor form.
type productForm struct {
	Kind             string   `form:"kind"`
	DigitalFile      string   `form:"digital_file"`
	DownloadDays     int      `form:"download_days"`
	Name             string   `form:"name"`
	Slug             string   `form:"slug"`
	GroupName        string   `form:"group_name"`
	GroupSlug        string   `form:"group_slug"`
	ShortDescription string   `form:"short_description"`
	Description      string   `form:"description"`
	Price            string   `form:"price"`
	Currency         string   `form:"currency"`
	Active           bool     `form:"active"`
	PlanCode         string   `form:"plan_code"`
	ServerID         string   `form:"server_id"`
	AccessMonths     int      `form:"access_months"`
	Features         []string `form:"features"`
	ImageMediaID     string   `form:"image_media_id"`
	Sort             int      `form:"sort"`
	SEOTitle         string   `form:"seo_title"`
	SEODescription   string   `form:"seo_description"`
	SEOOGImage       string   `form:"seo_og_image"`
	SEORobots        string   `form:"seo_robots"`
}

func formOf(p *Product) productForm {
	m := seo.FromMap(p.SEO)
	return productForm{Kind: p.Kind, DigitalFile: p.DigitalFile, DownloadDays: p.DownloadDays, Name: p.Name, Slug: p.Slug, GroupName: p.GroupName, GroupSlug: p.GroupSlug, ShortDescription: p.ShortDescription,
		Description: p.Description, Price: FormatPrice(p.PriceMinor, p.Currency), Currency: p.Currency, Active: p.Active, PlanCode: p.PlanCode,
		ServerID: p.ServerID, AccessMonths: p.AccessMonths, Features: p.Features, ImageMediaID: p.ImageMediaID, Sort: p.Sort,
		SEOTitle: m.Title, SEODescription: m.Description, SEOOGImage: m.OGImage, SEORobots: m.Robots}
}

type productPage struct {
	ID, WPProductID, ImageURL string
	UploadNotice, UploadError string
	Form                      productForm
	Fields                    map[string]string
	Servers                   []Server
}

func (h *adminHandlers) renderProduct(w http.ResponseWriter, r *http.Request, status int, pg productPage) {
	pg.Servers, _ = h.svc.Servers(r.Context())
	if pg.Fields == nil {
		pg.Fields = map[string]string{}
	}
	if pg.Form.Features == nil {
		pg.Form.Features = []string{}
	}
	if m, ok := h.svc.media(); ok && pg.Form.ImageMediaID != "" {
		if item, err := m.Get(r.Context(), pg.Form.ImageMediaID); err == nil {
			pg.ImageURL = m.URL(item, "thumbnail")
		}
	}
	h.tpl.RenderWith(w, r, status, "admin", "pages/commerce/product_form.html", pg)
}

func (h *adminHandlers) productNew(w http.ResponseWriter, r *http.Request) {
	h.renderProduct(w, r, http.StatusOK, productPage{Form: productForm{Kind: ProductProvisioning, Currency: h.svc.currency(r.Context()), AccessMonths: 1, DownloadDays: 7}})
}

func (h *adminHandlers) productEdit(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.productByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderProduct(w, r, http.StatusOK, productPage{ID: p.ID, WPProductID: p.WPProductID, Form: formOf(p)})
}

func (h *adminHandlers) productSave(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var f productForm
	if err := httpx.Bind(r, &f); err != nil {
		h.renderProduct(w, r, http.StatusBadRequest, productPage{ID: id, Form: f, Fields: map[string]string{"form": "commerce.error.required"}})
		return
	}
	f.Currency = strings.ToUpper(strings.TrimSpace(f.Currency))
	if f.Currency == "" { // the product gets the store currency: scale the price by it too
		f.Currency = h.svc.currency(r.Context())
	}
	price, perr := ParsePrice(f.Price, f.Currency)
	p := Product{Kind: f.Kind, DigitalFile: f.DigitalFile, DownloadDays: f.DownloadDays, ID: id, Name: f.Name, Slug: f.Slug, GroupName: f.GroupName, GroupSlug: f.GroupSlug, ShortDescription: f.ShortDescription,
		Description: f.Description, PriceMinor: price, Currency: f.Currency, Active: f.Active, PlanCode: f.PlanCode, ServerID: f.ServerID,
		AccessMonths: f.AccessMonths, Features: f.Features, ImageMediaID: strings.TrimSpace(f.ImageMediaID), Sort: f.Sort}
	meta := seo.Meta{Title: strings.TrimSpace(f.SEOTitle), Description: strings.TrimSpace(f.SEODescription), OGImage: strings.TrimSpace(f.SEOOGImage), Robots: strings.TrimSpace(f.SEORobots)}
	p.SEO = meta.ToMap()
	wpID := ""
	if id != "" {
		old, err := h.svc.productByID(r.Context(), id)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		wpID, p.WPProductID = old.WPProductID, old.WPProductID
		p.Intake = old.Intake
		// Keep SEO keys the form does not edit (canonical, schema…).
		for k, v := range old.SEO {
			if _, edited := map[string]bool{"title": true, "description": true, "og_image": true, "robots": true}[k]; !edited {
				p.SEO[k] = v
			}
		}
	}
	if perr != nil {
		h.renderProduct(w, r, http.StatusUnprocessableEntity, productPage{ID: id, WPProductID: wpID, Form: f, Fields: map[string]string{"price": "commerce.error.price_invalid"}})
		return
	}
	saved, err := h.svc.SaveProduct(r.Context(), p)
	var ve *ValidationError
	if errors.As(err, &ve) {
		h.renderProduct(w, r, http.StatusUnprocessableEntity, productPage{ID: id, WPProductID: wpID, Form: f, Fields: ve.Fields})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "commerce.flash.saved")
	http.Redirect(w, r, "/admin/commerce/products/"+saved.ID, http.StatusSeeOther)
}

func (h *adminHandlers) productDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteProduct(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "commerce.flash.deleted")
	http.Redirect(w, r, "/admin/commerce/products", http.StatusSeeOther)
}
