package site

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

type intakeFormData struct {
	Kind, RequestID, Action string
	Values                  map[string]string
	OK                      bool
	Error                   string
}

func (h *handlers) intakeEnabled(r *http.Request) bool {
	return h.boolSetting(r.Context(), "intake.enabled", false)
}
func (h *handlers) intakeGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.intakeEnabled(r) {
			http.NotFound(w, r)
			return
		}
		if !h.reports.allow(audit.IPFromContext(r.Context())) {
			http.Error(w, "Please try again later", 429)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 96<<10)
		next.ServeHTTP(w, r)
	})
}
func intakeHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
}
func (h *handlers) renderIntake(w http.ResponseWriter, r *http.Request, status int, data intakeFormData) {
	intakeHeaders(w)
	tc := h.newContext(w, r)
	tc.Status = status
	tc.Extra["intake"] = data
	h.render(w, r, kernel.KindRequest{Kind: "private-intake"}, tc)
}
func (h *handlers) intakeForm(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "form")
	if !h.intakeEnabled(r) || (kind != "incident" && kind != "follow-up") {
		http.NotFound(w, r)
		return
	}
	prefix := ""
	if locale := i18n.Locale(r.Context()); locale != h.siteLocaleOf(r.Context()) {
		prefix = "/" + locale
	}
	h.renderIntake(w, r, 200, intakeFormData{Kind: kind, RequestID: db.NewID(), Action: prefix + "/reports/" + kind, Values: map[string]string{}})
}
func (h *handlers) intakeSubmit(w http.ResponseWriter, r *http.Request) {
	intakeHeaders(w)
	kind := chi.URLParam(r, "form")
	if !h.intakeEnabled(r) || (kind != "incident" && kind != "follow-up") {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	values := map[string]string{}
	for _, key := range []string{"category", "area", "date", "description", "contact", "reference"} {
		values[key] = strings.TrimSpace(r.PostForm.Get(key))
		if !utf8.ValidString(values[key]) || strings.ContainsRune(values[key], 0) {
			http.Error(w, "Invalid form", 400)
			return
		}
	}
	prefix := ""
	if locale := i18n.Locale(r.Context()); locale != h.siteLocaleOf(r.Context()) {
		prefix = "/" + locale
	}
	data := intakeFormData{Kind: kind, RequestID: r.PostForm.Get("request_id"), Action: prefix + "/reports/" + kind, Values: values}
	if r.PostForm.Get("website") != "" {
		data.OK = true
		data.Values = nil
		h.renderIntake(w, r, 200, data)
		return
	}
	valid := intakeID.MatchString(data.RequestID) && r.PostForm.Get("consent") == "yes"
	valid = valid && values["description"] != "" && utf8.RuneCountInString(values["description"]) <= 5000 && utf8.RuneCountInString(values["area"]) <= 160
	valid = valid && utf8.RuneCountInString(values["reference"]) <= 100 && (values["contact"] == "" || validEmail(values["contact"]))
	valid = valid && (values["category"] == "safety" || values["category"] == "violence" || values["category"] == "other")
	if values["date"] != "" {
		_, err := time.Parse("2006-01-02", values["date"])
		valid = valid && err == nil
	}
	if kind == "follow-up" && values["reference"] == "" {
		valid = false
	}
	if !valid {
		data.Error = "site.intake.invalid"
		h.renderIntake(w, r, 422, data)
		return
	}
	err := h.createIntakeFrom(r.Context(), data.RequestID, kind, data.Action, intakePayload{Category: values["category"], Area: values["area"], Date: values["date"], Description: values["description"], Contact: values["contact"], Reference: values["reference"]})
	if errors.Is(err, errIntakeConflict) {
		http.Error(w, "Report request changed; start a new form.", 409)
		return
	}
	if err != nil {
		h.d.Log.Error("intake: storing report failed")
		http.Error(w, "Unable to store report", 500)
		return
	}
	data.OK = true
	data.Values = nil
	h.renderIntake(w, r, 200, data)
}
