package identitylogoadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	coreadmin "github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/identitylogo"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

type handlers struct {
	d       *kernel.Deps
	tpl     *httpx.Templates
	service *identitylogo.Service
}
type pageView struct {
	Profiles                                                 []coreadmin.LogoProfile
	Input                                                    identitylogo.Input
	Review                                                   *identitylogo.Review
	Candidate                                                *identitylogo.Candidate
	Payload, Error, Message, ImportedMedia, Budget, Estimate string
	CanUpload                                                bool
	Theme                                                    string
}

func (h *handlers) view(ctx context.Context) pageView {
	v := pageView{CanUpload: auth.Can(ctx, "media.upload"), Input: identitylogo.Input{Size: 1024, Currency: "USD"}}
	v.Profiles, _ = coreadmin.ListIdentityLogoProfiles(ctx, h.d)
	if st, ok := kernel.Get[theme.Service](h.d, kernel.ServiceTheme); ok {
		v.Theme = st.Active(ctx).Slug
	}
	return v
}
func (h *handlers) active(ctx context.Context) bool {
	on, e := kernel.AddonEnabled(ctx, h.d.DB, "identitylogo")
	return e == nil && on
}
func (h *handlers) render(w http.ResponseWriter, r *http.Request, status int, v pageView) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	h.tpl.RenderWith(w, r, status, "admin", "pages/identitylogo/index.html", v)
}
func (h *handlers) page(w http.ResponseWriter, r *http.Request) {
	if !h.active(r.Context()) {
		http.NotFound(w, r)
		return
	}
	v := h.view(r.Context())
	if id := r.URL.Query().Get("candidate"); id != "" {
		if !auth.Can(r.Context(), "media.upload") {
			http.Error(w, "Forbidden", 403)
			return
		}
		candidate, _, err := h.service.Candidate(r.Context(), owner(r.Context()), id)
		if err != nil {
			v.Error = errorKey(err)
		} else {
			v.Candidate = &candidate
		}
	}
	h.render(w, r, 200, v)
}

var amountPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,6})?$`)

func amount(raw string) (int64, error) {
	if !amountPattern.MatchString(raw) {
		return 0, identitylogo.ErrUnsupported
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || len(parts[0]) > 7 || strings.HasPrefix(raw, "-") {
		return 0, identitylogo.ErrUnsupported
	}
	whole, e := strconv.ParseInt(parts[0], 10, 64)
	if e != nil {
		return 0, e
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > 6 {
		return 0, identitylogo.ErrUnsupported
	}
	frac += strings.Repeat("0", 6-len(frac))
	decimal, e := strconv.ParseInt(frac, 10, 64)
	return whole*1e6 + decimal, e
}
func (h *handlers) submit(w http.ResponseWriter, r *http.Request) {
	if !h.active(r.Context()) {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 48<<10)
	if r.ParseForm() != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	v := h.view(r.Context())
	ctx := r.Context()
	var e error
	switch r.Form.Get("action") {
	case "configure":
		estimate := int64(0)
		if raw := r.Form.Get("estimate"); raw != "" {
			estimate, e = amount(raw)
		}
		if e == nil {
			e = coreadmin.SaveIdentityLogoBinding(ctx, h.d, r.Form.Get("connection"), coreadmin.LogoBinding{ConnectionVersion: r.Form.Get("connection_version"), Protocol: r.Form.Get("protocol"), Model: r.Form.Get("model"), Quality: r.Form.Get("quality"), Background: r.Form.Get("background"), EstimateMicros: estimate, Currency: r.Form.Get("currency"), Source: r.Form.Get("source"), AsOf: r.Form.Get("as_of"), Basis: r.Form.Get("basis")})
		}
		if e == nil {
			v = h.view(ctx)
			v.Message = "identitylogo.config_saved"
		}
	case "review":
		v.Budget = r.Form.Get("budget")
		budget, parseErr := amount(v.Budget)
		e = parseErr
		v.Input = identitylogo.Input{ConnectionID: r.Form.Get("connection"), Theme: v.Theme, Prompt: r.Form.Get("prompt"), Size: 1024, BudgetMicros: budget, Currency: r.Form.Get("currency")}
		if e == nil {
			var review identitylogo.Review
			review, e = h.service.Preview(ctx, owner(ctx), v.Input)
			if e == nil {
				v.Review = &review
				v.Estimate = coreadmin.FormatLogoAmount(review.Prepared.Quote.AmountMicros)
				var pretty bytes.Buffer
				_ = json.Indent(&pretty, review.Prepared.Payload, "", "  ")
				v.Payload = pretty.String()
			}
		}
	case "generate":
		var c identitylogo.Candidate
		c, e = h.service.Generate(ctx, owner(ctx), r.Form.Get("review"), r.Form.Get("approved") == "1")
		if e == nil {
			http.Redirect(w, r, "/admin"+pagePath+"?candidate="+c.ID, http.StatusSeeOther)
			return
		}
	case "select":
		e = h.service.Select(ctx, owner(ctx), r.Form.Get("candidate"), r.Form.Get("slot"), r.Form.Get("revision"))
		if e == nil {
			v.Message = "identitylogo.draft_saved"
		}
	default:
		e = identitylogo.ErrUnsupported
	}
	if e != nil {
		v.Error = errorKey(e)
		var imported *importedConflict
		if errors.As(e, &imported) {
			v.ImportedMedia = imported.MediaID
		}
		h.render(w, r, 422, v)
		return
	}
	h.render(w, r, 200, v)
}
func errorKey(e error) string {
	switch {
	case errors.Is(e, identitylogo.ErrApproval):
		return "identitylogo.error_approval"
	case errors.Is(e, identitylogo.ErrChanged), errors.Is(e, coreadmin.ErrLogoChanged), errors.Is(e, theme.ErrIdentityLogoConflict):
		return "identitylogo.error_changed"
	case errors.Is(e, identitylogo.ErrProvider), errors.Is(e, coreadmin.ErrLogoProvider):
		return "identitylogo.error_provider"
	case errors.Is(e, identitylogo.ErrCandidate):
		return "identitylogo.error_candidate"
	case errors.Is(e, identitylogo.ErrLimit):
		return "identitylogo.error_limit"
	}
	return "identitylogo.error_unavailable"
}
func (h *handlers) image(w http.ResponseWriter, r *http.Request) {
	c, b, e := h.service.Candidate(r.Context(), owner(r.Context()), chi.URLParam(r, "id"))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `inline; filename="logo-proposal.png"`)
	w.Header().Set("Content-Length", strconv.Itoa(c.Bytes))
	_, _ = w.Write(b)
}
