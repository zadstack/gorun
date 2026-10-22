package identityai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

const (
	reviewTTL       = 10 * time.Minute
	maxBriefField   = 300
	maxOutputTokens = 1500
	maxReply        = 64 << 10
)

// Brief is what the operator tells the model.
type Brief struct {
	Connection string // connection id; "" = the site's writing settings
	Sector     string
	Audience   string
	Tone       string
	Keep       string // what must stay (existing logo, a colour)
	Languages  string // "ar", "en" or "both"
}

func (b Brief) normalize() Brief {
	clip := func(s string) string {
		s = strings.TrimSpace(s)
		if utf8.RuneCountInString(s) > maxBriefField {
			s = string([]rune(s)[:maxBriefField])
		}
		return s
	}
	b.Sector, b.Audience, b.Tone, b.Keep = clip(b.Sector), clip(b.Audience), clip(b.Tone), clip(b.Keep)
	if b.Languages != "ar" && b.Languages != "en" {
		b.Languages = "both"
	}
	return b
}

// review is one previewed request, approved and consumed at most once.
type review struct {
	Token       string
	Owner       string
	Expires     time.Time
	Brief       Brief
	Connection  Connection
	Fingerprint string // destination fingerprint at preview time
	Theme       string // active theme at preview time
	System      string
	Input       string
	Estimate    Estimate
}

// Hash is a short fingerprint of the previewed request shown next to the
// token so the operator can match what they approved.
func (r review) Hash() string {
	sum := sha256.Sum256([]byte(PromptVersion + "\n" + r.System + "\n" + r.Input))
	return hex.EncodeToString(sum[:4])
}

type service struct {
	d     *kernel.Deps
	hooks Hooks
	mu    sync.Mutex
	open  map[string]review // owner → newest preview
}

func newService(d *kernel.Deps, h Hooks) *service {
	return &service{d: d, hooks: h, open: map[string]review{}}
}

// Suggestion is the model's answer after validation.
type Suggestion struct {
	Accent, Background, Text         string
	HeadingFont, BodyFont, LatinFont string
	Radius, Spacing                  string
	TaglineAR, TaglineEN, LogoBrief  string
	HeadingFontLabel, BodyFontLabel  string
	LatinFontLabel                   string
	ContrastRatio                    string
	SavedAR, SavedEN                 bool // which tagline went into the draft
	Values                           map[string]string
}

type rawSuggestion struct {
	Accent      string `json:"accent"`
	Background  string `json:"background"`
	Text        string `json:"text"`
	HeadingFont string `json:"heading_font"`
	BodyFont    string `json:"body_font"`
	LatinFont   string `json:"latin_font"`
	Radius      string `json:"radius"`
	Spacing     string `json:"spacing"`
	TaglineAR   string `json:"tagline_ar"`
	TaglineEN   string `json:"tagline_en"`
	LogoBrief   string `json:"logo_brief"`
}

var (
	errNotReady         = errors.New("identityai: no ready AI connection")
	errReview           = errors.New("identityai: review required")
	errUpstream         = errors.New("identityai: provider failed")
	errBadReply         = errors.New("identityai: reply is not usable")
	errNoApproval       = errors.New("identityai: approval missing")
	errUnsupported      = errors.New("identityai: theme does not accept identity settings")
	errChanged          = errors.New("identityai: destination or theme changed since the preview")
	errUnsupportedModel = errors.New("identityai: the shared client cannot drive this model")
	hexRe               = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

const systemPrompt = `You are a brand designer helping a small website owner choose a visual identity for a bilingual Arabic/English site built with GoRun.
Answer with ONE JSON object and nothing else, no markdown fences, with exactly these keys:
"accent" (hex #rrggbb), "background" (hex #rrggbb), "text" (hex #rrggbb; must reach WCAG AA 4.5:1 on background),
"heading_font" and "body_font" (one of: zad, ibm-plex-arabic, noto-naskh, noto-kufi, readex, tajawal, cairo, amiri),
"latin_font" (one of: inter, source-serif, ibm-plex-sans, manrope),
"radius" (one of: square, soft, round), "spacing" (one of: compact, normal, relaxed),
"tagline_ar" (Arabic, at most 80 characters; empty string when the brief says English only), "tagline_en" (English, at most 80 characters; empty string when the brief says Arabic only),
"logo_brief" (plain-text guidance for a human designer, at most 400 characters; describe shape, mark and colour use; you cannot draw).
Do not invent a company name. Do not output images, SVG or URLs.`

func userPrompt(b Brief, site theme.IdentitySnapshot, themeName string) string {
	var sb strings.Builder
	sb.WriteString("Site name: " + site.Site["site.title"] + "\n")
	if t := site.Site["site.tagline"]; t != "" {
		sb.WriteString("Current tagline: " + t + "\n")
	}
	sb.WriteString("Theme: " + themeName + "\n")
	sb.WriteString("Sector: " + b.Sector + "\nAudience: " + b.Audience + "\nTone: " + b.Tone + "\n")
	if b.Keep != "" {
		sb.WriteString("Must keep: " + b.Keep + "\n")
	}
	sb.WriteString("Languages: " + b.Languages + "\n")
	if a := site.Values["identity_accent"]; a != "" {
		sb.WriteString("Current accent: " + a + "\n")
	}
	return sb.String()
}

// PromptVersion identifies the system prompt wording; it is part of the
// request fingerprint and of every audit row, so a changed prompt is a
// different request.
const PromptVersion = "identityai-prompt-v1"

// ClientCannotDrive reports a model the shared AI client cannot call:
// OpenAI reasoning models require max_completion_tokens on
// chat/completions, which the client does not send.
func ClientCannotDrive(provider, model string) bool {
	if strings.ToLower(provider) != "openai" {
		return false
	}
	m := strings.ToLower(model)
	for _, prefix := range []string{"gpt-5", "o1", "o3", "o4"} {
		if strings.HasPrefix(m, prefix) {
			return true
		}
	}
	return false
}

// destination resolves the chosen connection for display; "" means the
// writing settings.
func (s *service) destination(ctx context.Context, id string) (Connection, bool) {
	if id == "" {
		if s.hooks.Writing == nil {
			return Connection{}, false
		}
		w, ok := s.hooks.Writing(ctx, s.d)
		return w, ok && s.hooks.Ready(ctx, s.d, "", "")
	}
	list, err := s.hooks.Connections(ctx, s.d)
	if err != nil {
		return Connection{}, false
	}
	for _, c := range list {
		if c.ID == id {
			return c, s.hooks.Ready(ctx, s.d, c.ID, c.Model)
		}
	}
	return Connection{}, false
}

func (s *service) owner(ctx context.Context) string {
	if u := auth.UserFromContext(ctx); u != nil {
		return u.ID
	}
	return ""
}

func (s *service) store() (IdentityStore, bool) {
	return kernel.Get[IdentityStore](s.d, kernel.ServiceTheme)
}

// current is the draft when one exists, else the live identity.
func (s *service) current(ctx context.Context, st IdentityStore, slug string) theme.IdentitySnapshot {
	if d, ok := st.IdentityDraft(ctx, slug); ok {
		return d.Snapshot
	}
	return st.LiveIdentity(ctx, slug)
}

// Preview builds the exact request and keeps it for ten minutes; nothing
// is sent.
func (s *service) Preview(ctx context.Context, b Brief) (review, error) {
	b = b.normalize()
	conn, ready := s.destination(ctx, b.Connection)
	if !ready {
		return review{}, errNotReady
	}
	if ClientCannotDrive(conn.Provider, conn.Model) {
		return review{}, errUnsupportedModel
	}
	st, ok := s.store()
	if !ok {
		return review{}, errNotReady
	}
	info := st.Active(ctx)
	if !theme.SupportsIdentity(info) {
		return review{}, errUnsupported
	}
	fingerprint, ok := s.fingerprint(ctx, b.Connection)
	if !ok {
		return review{}, errNotReady
	}
	input := userPrompt(b, s.current(ctx, st, info.Slug), info.Name)
	r := review{Token: db.NewID(), Owner: s.owner(ctx), Expires: time.Now().Add(reviewTTL), Brief: b, Connection: conn, Fingerprint: fingerprint, Theme: info.Slug,
		System: systemPrompt, Input: input, Estimate: EstimateCost(conn.Provider, conn.Model, estimateTokens(systemPrompt+input), maxOutputTokens)}
	s.mu.Lock()
	s.open[r.Owner] = r
	s.mu.Unlock()
	return r, nil
}

// peek returns the owner's review when token matches, without consuming it.
func (s *service) peek(ctx context.Context, token string) (review, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.open[s.owner(ctx)]
	if !ok || token == "" || r.Token != token || time.Now().After(r.Expires) {
		return review{}, false
	}
	return r, true
}

// fingerprint is the current destination fingerprint for connectionID.
func (s *service) fingerprint(ctx context.Context, connectionID string) (string, bool) {
	if s.hooks.Fingerprint == nil {
		return "", false
	}
	return s.hooks.Fingerprint(ctx, s.d, connectionID)
}

// take consumes the owner's review when token matches and it is fresh.
func (s *service) take(ctx context.Context, token string) (review, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	owner := s.owner(ctx)
	r, ok := s.open[owner]
	if !ok || token == "" || r.Token != token || time.Now().After(r.Expires) {
		return review{}, false
	}
	delete(s.open, owner)
	return r, true
}

// Generate sends the reviewed request, validates the reply and merges it
// into the identity draft. The live identity is never written.
func (s *service) Generate(ctx context.Context, token string, approved bool) (Suggestion, error) {
	if !approved {
		return Suggestion{}, errNoApproval
	}
	r, ok := s.take(ctx, token)
	if !ok {
		return Suggestion{}, errReview
	}
	st, ok := s.store()
	if !ok {
		return Suggestion{}, errNotReady
	}
	info := st.Active(ctx)
	// The consent covers exactly what was shown: the same destination
	// (configuration and credential) and the same theme. Anything else was
	// never reviewed, so nothing is sent and nothing is recorded against
	// the destination that was approved.
	if now, ok := s.fingerprint(ctx, r.Brief.Connection); !ok || now != r.Fingerprint || info.Slug != r.Theme {
		return Suggestion{}, errChanged
	}
	audit := func(outcome string) {
		if s.d.Audit != nil {
			s.d.Audit.Record(ctx, "identityai.generate", "theme", info.Slug, map[string]any{"provider": r.Connection.Provider, "model": r.Connection.Model, "connection": r.Brief.Connection, "request": r.Hash(), "prompt_version": PromptVersion, "outcome": outcome})
		}
	}
	reply, err := s.hooks.Complete(ctx, s.d, r.Brief.Connection, r.Connection.Model, r.System, r.Input, maxOutputTokens, r.Fingerprint)
	if errors.Is(err, ErrDestinationChanged) {
		return Suggestion{}, errChanged
	}
	if err != nil {
		s.d.Log.Warn("identityai: generation failed", "provider", r.Connection.Provider)
		audit("upstream")
		return Suggestion{}, errUpstream
	}
	sug, err := parseSuggestion(reply)
	if err != nil {
		audit("bad_reply")
		return Suggestion{}, err
	}
	snap := s.current(ctx, st, info.Slug)
	snap.Theme = info.Slug
	if snap.Values == nil {
		snap.Values = map[string]string{}
	}
	if snap.Site == nil {
		snap.Site = map[string]string{}
	}
	for k, v := range sug.Values {
		snap.Values[k] = v
	}
	if snap.Site["site.title"] == "" {
		snap.Site["site.title"] = s.d.Settings.GetString(ctx, "site.title", "GoRun")
	}
	// One tagline fits site.tagline; the operator's language choice decides
	// which, and the other is shown as not saved.
	switch r.Brief.Languages {
	case "en":
		if sug.TaglineEN != "" {
			snap.Site["site.tagline"], sug.SavedEN = sug.TaglineEN, true
		}
	case "ar":
		if sug.TaglineAR != "" {
			snap.Site["site.tagline"], sug.SavedAR = sug.TaglineAR, true
		}
	default:
		if sug.TaglineAR != "" {
			snap.Site["site.tagline"], sug.SavedAR = sug.TaglineAR, true
		} else if sug.TaglineEN != "" {
			snap.Site["site.tagline"], sug.SavedEN = sug.TaglineEN, true
		}
	}
	if err := st.SaveIdentityDraft(ctx, snap); err != nil {
		var ve *theme.IdentityValidationError
		if errors.As(err, &ve) {
			audit("invalid")
			return Suggestion{}, errBadReply
		}
		audit("store_failed")
		return Suggestion{}, err
	}
	audit("ok")
	return sug, nil
}

func parseSuggestion(reply string) (Suggestion, error) {
	if len(reply) > maxReply {
		return Suggestion{}, errBadReply
	}
	start := strings.Index(reply, "{")
	if start < 0 {
		return Suggestion{}, errBadReply
	}
	var raw rawSuggestion
	if err := json.NewDecoder(strings.NewReader(reply[start:])).Decode(&raw); err != nil {
		return Suggestion{}, errBadReply
	}
	sug := Suggestion{Accent: strings.ToLower(raw.Accent), Background: strings.ToLower(raw.Background), Text: strings.ToLower(raw.Text),
		HeadingFont: raw.HeadingFont, BodyFont: raw.BodyFont, LatinFont: raw.LatinFont, Radius: raw.Radius, Spacing: raw.Spacing,
		TaglineAR: clipRunes(raw.TaglineAR, 120), TaglineEN: clipRunes(raw.TaglineEN, 120), LogoBrief: clipRunes(raw.LogoBrief, 600), Values: map[string]string{}}
	for _, c := range []string{sug.Accent, sug.Background, sug.Text} {
		if c != "" && !hexRe.MatchString(c) {
			return Suggestion{}, errBadReply
		}
	}
	if sug.Accent == "" && sug.Background == "" && sug.Text == "" && sug.TaglineAR == "" && sug.TaglineEN == "" {
		return Suggestion{}, errBadReply
	}
	put := func(key, v string) {
		if v != "" {
			sug.Values[key] = v
		}
	}
	put("identity_accent", sug.Accent)
	put("identity_background", sug.Background)
	put("identity_text", sug.Text)
	put("identity_heading_font", sug.HeadingFont)
	put("identity_body_font", sug.BodyFont)
	put("identity_latin_font", sug.LatinFont)
	put("identity_radius", sug.Radius)
	put("identity_spacing", sug.Spacing)
	// Fonts, radius, spacing and contrast are validated by the theme when
	// the draft is saved; labels are for the result page.
	sug.HeadingFontLabel, sug.BodyFontLabel, sug.LatinFontLabel = theme.IdentityFontLabel(sug.HeadingFont), theme.IdentityFontLabel(sug.BodyFont), theme.IdentityFontLabel(sug.LatinFont)
	if sug.Text != "" && sug.Background != "" {
		sug.ContrastRatio = theme.ContrastRatio(sug.Text, sug.Background)
	}
	return sug, nil
}

func clipRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > n {
		return string([]rune(s)[:n])
	}
	return s
}

// page data and handlers

// Unsupported reports a connection whose model the client cannot drive.
func (c Connection) Unsupported() bool { return ClientCannotDrive(c.Provider, c.Model) }

type pageView struct {
	Connections []Connection
	Brief       Brief
	Review      *review
	Result      *Suggestion
	Error       string
	Theme       theme.Info
	Title       string
	PricesAsOf  string
	IdentityURL string
}

func (s *service) pageData(ctx context.Context, b Brief, r *review, msg string) pageView {
	v := pageView{Brief: b, Review: r, Error: msg, PricesAsOf: PricesAsOf, IdentityURL: "/admin/appearance/identity"}
	if list, err := s.hooks.Connections(ctx, s.d); err == nil {
		v.Connections = list
	} else {
		s.d.Log.Warn("identityai: listing AI connections failed", "err", err)
	}
	if st, ok := s.store(); ok {
		v.Theme = st.Active(ctx)
	}
	v.Title = s.d.Settings.GetString(ctx, "site.title", "GoRun")
	return v
}

func (h *handlers) render(w http.ResponseWriter, r *http.Request, status int, v pageView) {
	h.tpl.RenderWith(w, r, status, "admin", "pages/identityai/index.html", v)
}

func briefFromForm(r *http.Request) Brief {
	return Brief{Connection: r.PostForm.Get("connection"), Sector: r.PostForm.Get("sector"), Audience: r.PostForm.Get("audience"), Tone: r.PostForm.Get("tone"), Keep: r.PostForm.Get("keep"), Languages: r.PostForm.Get("languages")}.normalize()
}

func (h *handlers) submit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	t := func(k string) string { return h.s.d.I18n.T(i18n.Locale(ctx), k) }
	b := briefFromForm(r)
	switch r.PostForm.Get("action") {
	case "generate":
		token := r.PostForm.Get("review")
		sug, err := h.s.Generate(ctx, token, r.PostForm.Get("approved") == "1")
		switch {
		case errors.Is(err, errNoApproval):
			// The review is still valid: show it again so one tick suffices.
			rv, ok := h.s.peek(ctx, token)
			var rp *review
			if ok {
				rp = &rv
			}
			h.render(w, r, http.StatusUnprocessableEntity, h.s.pageData(ctx, b, rp, t("identityai.error_approval")))
		case errors.Is(err, errReview):
			h.render(w, r, http.StatusConflict, h.s.pageData(ctx, b, nil, t("identityai.error_review")))
		case errors.Is(err, errChanged):
			h.render(w, r, http.StatusConflict, h.s.pageData(ctx, b, nil, t("identityai.error_changed")))
		case errors.Is(err, errUpstream):
			h.render(w, r, http.StatusBadGateway, h.s.pageData(ctx, b, nil, t("identityai.error_upstream")))
		case errors.Is(err, errBadReply):
			h.render(w, r, http.StatusUnprocessableEntity, h.s.pageData(ctx, b, nil, t("identityai.error_reply")))
		case err != nil:
			h.s.d.Log.Error("identityai: generate failed", "err", err)
			h.render(w, r, http.StatusInternalServerError, h.s.pageData(ctx, b, nil, t("identityai.error_failed")))
		default:
			v := h.s.pageData(ctx, b, nil, "")
			v.Result = &sug
			h.render(w, r, http.StatusOK, v)
		}
	default: // preview
		rv, err := h.s.Preview(ctx, b)
		if errors.Is(err, errUnsupported) {
			h.render(w, r, http.StatusUnprocessableEntity, h.s.pageData(ctx, b, nil, t("identityai.error_theme_unsupported")))
			return
		}
		if errors.Is(err, errUnsupportedModel) {
			h.render(w, r, http.StatusUnprocessableEntity, h.s.pageData(ctx, b, nil, t("identityai.error_model_unsupported")))
			return
		}
		if err != nil {
			h.render(w, r, http.StatusUnprocessableEntity, h.s.pageData(ctx, b, nil, t("identityai.error_not_ready")))
			return
		}
		h.render(w, r, http.StatusOK, h.s.pageData(ctx, b, &rv, ""))
	}
}
