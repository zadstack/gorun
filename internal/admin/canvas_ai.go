package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/mailer"
)

const (
	canvasAIMaxSuggestions = 3
	canvasAIMaxCards       = 8
	canvasAICacheTTL       = 24 * time.Hour
)

var (
	canvasAIAllowedKinds = []string{"source", "content", "prompt", "note", "checklist", "agent", "provider", "schedule"}
	errCanvasAI          = errors.New("canvas ai unavailable")
)

type canvasAICache struct {
	mu       sync.Mutex
	entries  map[string]canvasAIGeneration
	previews map[string]canvasAIPrepared
}

type canvasAIGeneration struct {
	RequestID   string
	ExpiresAt   time.Time
	TargetKind  string
	Applied     map[int]string
	Suggestions []canvasAISuggestion
}

type canvasAISuggestion struct {
	Title   string       `json:"title"`
	Summary string       `json:"summary"`
	Cards   []canvasCard `json:"cards"`
	Edges   []canvasEdge `json:"edges"`
}

type canvasAIRawSuggestion struct {
	Title   string            `json:"title"`
	Summary string            `json:"summary"`
	Cards   []canvasAIRawCard `json:"cards"`
	Edges   []canvasAIRawEdge `json:"edges"`
}

type canvasAIRawCard struct {
	Kind        string   `json:"kind"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	X           int      `json:"x"`
	Y           int      `json:"y"`
	SourceLimit int      `json:"source_limit,omitempty"`
	Interval    string   `json:"interval,omitempty"`
	Checklist   []string `json:"checklist,omitempty"`
}

type canvasAIRawEdge struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type canvasAIPayload struct {
	Suggestions []canvasAIRawSuggestion `json:"suggestions"`
}

// Prepared consent is process-local, private and single-use. A restart requires review.
type canvasAIPrepared struct {
	Owner, ContextHash string
	Expires            time.Time
	Revision           int64
	Config             aiConfig
}

func (h *handlers) canvasAICache() *canvasAICache {
	h.canvasAIOnce.Do(func() {
		h.canvasAISuggestions = &canvasAICache{entries: map[string]canvasAIGeneration{}, previews: map[string]canvasAIPrepared{}}
	})
	return h.canvasAISuggestions
}

// Read the actual config and its monotonic revision in one SQLite snapshot.
func (h *handlers) canvasAIConfig(ctx context.Context) (cfg aiConfig, revision int64, err error) {
	err = db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		snap, e := aiSnapshot(ctx, tx)
		if e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, `SELECT revision FROM admin_canvas_ai_config_revision WHERE id=1`).Scan(&revision); e != nil {
			return e
		}
		get := func(k string) string { var v string; _ = json.Unmarshal([]byte(snap[k]), &v); return v }
		cfg = aiConfig{Provider: get("ai.provider"), Model: get("ai.model"), BaseURL: get("ai.base_url")}
		if enc := get("ai.api_key"); enc != "" {
			cfg.APIKey, e = mailer.DecryptSecret(h.d.Config.SecretKey, enc)
			if e != nil {
				return errAINotConfigured
			}
		}
		switch cfg.Provider {
		case AIProviderOpenAI:
			var valid bool
			cfg.BaseURL, valid = normalizeAIBaseURL(cfg.BaseURL)
			if !valid {
				return errAINotConfigured
			}
		case AIProviderAnthropic:
			cfg.BaseURL = ""
			if cfg.Model == "" {
				cfg.Model = defaultAnthropicModel
			}
		default:
			return errAINotConfigured
		}
		return nil
	})
	return
}
func canvasAIDestination(cfg aiConfig) map[string]string {
	endpoint := cfg.BaseURL
	if cfg.Provider == AIProviderAnthropic {
		endpoint = anthropicURL
	}
	u, _ := url.Parse(endpoint)
	return map[string]string{"provider": cfg.Provider, "model": cfg.Model, "host": u.Host}
}
func (h *handlers) canvasAIRoutes() {
	r := h.d.Router.Admin
	r.Get("/x/canvases/ai-suggestions", h.canvasAIStatus)
	r.Post("/x/canvases/ai-suggestions/preview", h.canvasAIPreview)
	r.Post("/x/canvases/ai-suggestions/generate", h.canvasAIGenerate)
	r.Post("/x/canvases/ai-suggestions/apply", h.canvasAIApply)
}

func (h *handlers) canvasAICurrent(r *http.Request) bool {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		return false
	}
	current, err := h.users.Get(r.Context(), u.ID)
	if err != nil {
		return false
	}
	ctx := auth.WithUser(r.Context(), current)
	*r = *r.WithContext(ctx)
	return auth.Can(ctx, "content.edit") || auth.Can(ctx, "content.create")
}
func (h *handlers) canvasAIAllowed(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	if auth.ActorFromContext(r.Context()).Kind != "user" || !h.canvasAICurrent(r) {
		h.canvasAIOpsError(w, 403, "forbidden")
		return nil, false
	}
	u := auth.UserFromContext(r.Context())
	if u == nil || auth.ActorFromContext(r.Context()).Kind != "user" {
		h.canvasAIOpsError(w, 403, "forbidden")
		return nil, false
	}
	return u, true
}

func (h *handlers) canvasAIOpsError(w http.ResponseWriter, status int, code string) {
	h.aiJSON(w, status, map[string]string{"error": code})
}

func (h *handlers) canvasAIConfigured(ctx context.Context) bool {
	_, _, err := h.canvasAIConfig(ctx)
	return err == nil
}

func (h *handlers) canvasAIStatus(w http.ResponseWriter, r *http.Request) {
	u, ok := h.canvasAIAllowed(w, r)
	if !ok {
		return
	}
	out := map[string]any{"configured": h.canvasAIConfigured(r.Context()), "can_configure": auth.Can(r.Context(), "settings.manage")}
	if gen, ok := h.canvasAICache().get(u.ID); ok && time.Now().Before(gen.ExpiresAt) {
		out["request_id"] = gen.RequestID
		out["target_kind"] = gen.TargetKind
		out["suggestions"] = canvasAISuggestionList(gen.Suggestions)
	}
	h.aiJSON(w, 200, out)
}

func canvasAISuggestionList(items []canvasAISuggestion) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, s := range items {
		out = append(out, map[string]any{"title": s.Title, "summary": s.Summary, "cards": len(s.Cards)})
	}
	return out
}

func (c *canvasAICache) get(userID string) (canvasAIGeneration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gen, ok := c.entries[userID]
	if !ok || time.Now().After(gen.ExpiresAt) {
		delete(c.entries, userID)
		return canvasAIGeneration{}, false
	}
	return gen, true
}

// Only the owning user can consume; failed owner guesses cannot burn another user's review.
func (c *canvasAICache) take(owner, token string) (canvasAIPrepared, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.previews[token]
	if !ok || p.Owner != owner {
		return canvasAIPrepared{}, false
	}
	delete(c.previews, token)
	if time.Now().After(p.Expires) {
		return canvasAIPrepared{}, false
	}
	return p, true
}
func (c *canvasAICache) put(userID string, gen canvasAIGeneration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= 5000 {
		now := time.Now()
		for id, item := range c.entries {
			if now.After(item.ExpiresAt) {
				delete(c.entries, id)
			}
		}
	}
	if _, exists := c.entries[userID]; !exists && len(c.entries) >= 5000 {
		return false
	}
	c.entries[userID] = gen
	return true
}

func (h *handlers) canvasAIContext(ctx context.Context, locale, targetKind string) map[string]any {
	siteTitle := strings.TrimSpace(h.d.Settings.GetString(ctx, "site.title", ""))
	if len(siteTitle) > 500 {
		siteTitle = string([]rune(siteTitle)[:min(120, len([]rune(siteTitle)))])
	}
	types := []string{}
	if cs, ok := h.contentService(); ok && auth.Can(ctx, "content.view") {
		ts, err := cs.Types(ctx)
		if err == nil {
			for _, t := range ts {
				if t.Public && len(types) < 12 {
					types = append(types, t.Slug)
				}
			}
		}
	}
	return map[string]any{
		"locale":      locale,
		"target_kind": targetKind,
		"site_title":  siteTitle,
		"types":       types,
	}
}

func (h *handlers) canvasAIPreview(w http.ResponseWriter, r *http.Request) {
	u, ok := h.canvasAIAllowed(w, r)
	if !ok {
		return
	}

	var in struct {
		TargetKind string `json:"target_kind"`
		Locale     string `json:"locale"`
	}
	if canvasAIRead(w, r, &in) != nil || !canvasAITarget(in.TargetKind) {
		h.canvasAIOpsError(w, 422, "invalid")
		return
	}
	locale := canvasAILocale(in.Locale, r)
	args := h.canvasAIContext(r.Context(), locale, in.TargetKind)
	cfg, revision, err := h.canvasAIConfig(r.Context())
	if err != nil {
		h.canvasAIOpsError(w, 503, "not_configured")
		return
	}
	requestID := db.NewID()
	p := canvasAIPrepared{Owner: u.ID, Expires: time.Now().Add(10 * time.Minute), Revision: revision, Config: cfg, ContextHash: studioReviewHash(args)}
	c := h.canvasAICache()
	c.mu.Lock()
	for key, old := range c.previews {
		if time.Now().After(old.Expires) || old.Owner == u.ID {
			delete(c.previews, key)
		}
	}
	if len(c.previews) >= 5000 {
		c.mu.Unlock()
		h.canvasAIOpsError(w, 429, "rate_limited")
		return
	}
	c.previews[requestID] = p
	c.mu.Unlock()
	h.aiJSON(w, 200, map[string]any{"request_id": requestID, "context": args, "system": canvasAISystemPrompt, "user": canvasAIUserPrompt(args), "destination": canvasAIDestination(cfg), "expires_at": p.Expires, "locale": locale, "target_kind": in.TargetKind})
}

func (h *handlers) canvasAIGenerate(w http.ResponseWriter, r *http.Request) {
	u, ok := h.canvasAIAllowed(w, r)
	if !ok {
		return
	}

	var in struct {
		Reviewed   bool   `json:"reviewed"`
		RequestID  string `json:"request_id"`
		TargetKind string `json:"target_kind"`
		Locale     string `json:"locale"`
	}
	if canvasAIRead(w, r, &in) != nil || !in.Reviewed || !connectionIDPattern.MatchString(in.RequestID) || !canvasAITarget(in.TargetKind) {
		h.canvasAIOpsError(w, 422, "invalid")
		return
	}
	locale := canvasAILocale(in.Locale, r)
	args := h.canvasAIContext(r.Context(), locale, in.TargetKind)
	p, found := h.canvasAICache().take(u.ID, in.RequestID)
	if !found || p.Owner != u.ID || time.Now().After(p.Expires) || p.ContextHash != studioReviewHash(args) {
		h.canvasAIOpsError(w, 409, "review_required")
		return
	}
	_, revision, err := h.canvasAIConfig(r.Context())
	if err != nil || revision != p.Revision {
		h.canvasAIOpsError(w, 409, "review_required")
		return
	}
	if !h.aiLimit.allow("canvas-ai:" + u.ID) {
		h.canvasAIOpsError(w, 429, "rate_limited")
		return
	}
	// Use precisely the reviewed immutable destination, never a config loaded after the check.
	cfg := p.Config
	raw, err := aiComplete(r.Context(), cfg, canvasAISystemPrompt, canvasAIUserPrompt(args))
	if err != nil {
		h.d.Log.Warn("admin: canvas ai suggestions failed")
		h.canvasAIOpsError(w, 502, "upstream")
		return
	}
	suggestions, err := materializeCanvasAISuggestions(r.Context(), h, raw, in.TargetKind)
	if err != nil || len(suggestions) == 0 {
		h.canvasAIOpsError(w, 422, "invalid_response")
		return
	}
	gen := canvasAIGeneration{
		RequestID:   in.RequestID,
		ExpiresAt:   time.Now().Add(canvasAICacheTTL),
		TargetKind:  in.TargetKind,
		Suggestions: suggestions,
		Applied:     map[int]string{},
	}
	_, currentRevision, currentErr := h.canvasAIConfig(r.Context())
	if currentErr != nil || currentRevision != p.Revision || !h.canvasAICurrent(r) || studioReviewHash(h.canvasAIContext(r.Context(), locale, in.TargetKind)) != p.ContextHash {
		h.canvasAIOpsError(w, 409, "review_required")
		return
	}
	if !h.canvasAICache().put(u.ID, gen) {
		h.canvasAIOpsError(w, 429, "rate_limited")
		return
	}
	h.d.Audit.Record(r.Context(), "canvas.ai_suggest", "user", u.ID, map[string]any{"count": len(suggestions), "target": in.TargetKind})
	h.aiJSON(w, 200, map[string]any{
		"request_id":  gen.RequestID,
		"target_kind": gen.TargetKind,
		"suggestions": canvasAISuggestionList(gen.Suggestions),
	})
}

func (h *handlers) canvasAIApply(w http.ResponseWriter, r *http.Request) {
	u, ok := h.canvasAIAllowed(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID string `json:"request_id"`
		Index     int    `json:"index"`
		Title     string `json:"title"`
	}
	if canvasAIRead(w, r, &in) != nil || !connectionIDPattern.MatchString(in.RequestID) || in.Index < 0 {
		h.canvasAIOpsError(w, 422, "invalid")
		return
	}
	c := h.canvasAICache()
	c.mu.Lock()
	defer c.mu.Unlock()
	gen, ok := c.entries[u.ID]
	if !ok || time.Now().After(gen.ExpiresAt) || gen.RequestID != in.RequestID || in.Index >= len(gen.Suggestions) {
		h.canvasAIOpsError(w, 409, "expired")
		return
	}
	if id := gen.Applied[in.Index]; id != "" {
		h.aiJSON(w, 200, map[string]string{"id": id, "href": "/admin/canvases/" + id})
		return
	}
	pick := gen.Suggestions[in.Index]
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = pick.Title
	}
	if !canvasText(title, 120) || title == "" {
		h.canvasAIOpsError(w, 422, "invalid")
		return
	}
	id, err := h.canvasCreateDocument(r.Context(), title, pick.Cards, pick.Edges)
	if errors.Is(err, errCanvasLimit) {
		h.canvasAIOpsError(w, 422, "board_limit")
		return
	}
	if err != nil {
		h.canvasAIOpsError(w, 500, "unavailable")
		return
	}
	gen.Applied[in.Index] = id
	c.entries[u.ID] = gen
	h.d.Audit.Record(r.Context(), "canvas.ai_apply", "canvas", id, map[string]any{"index": in.Index})
	h.aiJSON(w, 200, map[string]string{"id": id, "href": "/admin/canvases/" + id})
}

func canvasAIRead(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.Decode(new(any)) != io.EOF {
		return errCanvasAI
	}
	return nil
}

func canvasAITarget(kind string) bool {
	return kind == "agent" || kind == "provider"
}

func canvasAILocale(locale string, r *http.Request) string {
	if locale == "ar" || locale == "en" {
		return locale
	}
	if auth.UserFromContext(r.Context()) != nil {
		if loc := auth.UserFromContext(r.Context()).Locale; loc == "ar" || loc == "en" {
			return loc
		}
	}
	return "en"
}

const canvasAISystemPrompt = `You propose operational canvas workflows for a solo site operator using a CMS.
Reply with ONLY one JSON object and no markdown fences.
The object must contain "suggestions": an array of exactly 3 items.
Each suggestion has:
- "title": board title (max 80 chars)
- "summary": one-line benefit (max 160 chars)
- "cards": 3 to 8 cards with kind, title, body, x, y; optional source_limit (1-5), interval (hourly|daily|weekly), checklist (string array for checklist kind only)
- "edges": connections using zero-based card indexes "from" and "to"; every edge must end at the single agent or provider card
Allowed kinds: source, content, prompt, note, checklist, agent, provider, schedule.
Do not include agent_id, provider_id, content_id, urls, widgets, or result cards.
Prompt bodies must instruct the executor to avoid publishing, sending mail, or changing settings.
Use the requested locale for titles and prompt bodies.`

func canvasAIUserPrompt(args map[string]any) string {
	b, _ := json.Marshal(args)
	return "Site context JSON:\n" + string(b) + "\nPropose 3 distinct workflows for this operator."
}

func materializeCanvasAISuggestions(ctx context.Context, h *handlers, raw, targetKind string) ([]canvasAISuggestion, error) {
	raw = strings.TrimSpace(raw)
	if m := codeFence.FindStringSubmatch(raw); len(m) == 2 {
		raw = strings.TrimSpace(m[1])
	}
	var payload canvasAIPayload
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, errCanvasAI
	}
	if len(payload.Suggestions) == 0 || len(payload.Suggestions) > canvasAIMaxSuggestions {
		return nil, errCanvasAI
	}
	out := []canvasAISuggestion{}
	for _, item := range payload.Suggestions {
		s, err := materializeCanvasAISuggestion(ctx, h, item, targetKind)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, errCanvasAI
	}
	return out, nil
}

func materializeCanvasAISuggestion(ctx context.Context, h *handlers, raw canvasAIRawSuggestion, targetKind string) (canvasAISuggestion, error) {
	title := strings.TrimSpace(raw.Title)
	summary := strings.TrimSpace(raw.Summary)
	if !canvasText(title, 120) || title == "" || !canvasText(summary, 300) || summary == "" || len(raw.Cards) == 0 || len(raw.Cards) > canvasAIMaxCards {
		return canvasAISuggestion{}, errCanvasAI
	}
	cards := make([]canvasCard, 0, len(raw.Cards))
	for i, c := range raw.Cards {
		kind := strings.TrimSpace(c.Kind)
		if !containsString(canvasAIAllowedKinds, kind) {
			return canvasAISuggestion{}, errCanvasAI
		}
		x, y := c.X, c.Y
		if x < 0 || y < 0 {
			x, y = 24+(i%3)*396, 24+(i/3)*220
		}
		card := canvasCard{
			ID:    uuid.NewString(),
			Kind:  kind,
			Title: strings.TrimSpace(c.Title),
			Body:  strings.TrimSpace(c.Body),
			X:     x,
			Y:     y,
		}
		if card.Title == "" {
			card.Title = kind
		}
		if !canvasText(card.Title, 120) || !canvasText(card.Body, 4000) {
			return canvasAISuggestion{}, errCanvasAI
		}
		switch kind {
		case "source":
			limit := c.SourceLimit
			if limit == 0 {
				limit = 3
			}
			card.SourceLimit = limit
		case "schedule":
			if canvasInterval(c.Interval) == 0 {
				card.Interval = "daily"
			} else {
				card.Interval = c.Interval
			}
		case "checklist":
			if len(c.Checklist) == 0 {
				card.Items = []canvasCheck{{Text: summary, Checked: false}}
			} else {
				for _, line := range c.Checklist {
					line = strings.TrimSpace(line)
					if !canvasText(line, 300) || line == "" || len(card.Items) >= 30 {
						return canvasAISuggestion{}, errCanvasAI
					}
					card.Items = append(card.Items, canvasCheck{Text: line})
				}
			}
		case "agent", "provider":
			if targetKind == "agent" {
				card.Kind = "agent"
				card.GenerationKind = ""
			} else {
				card.Kind = "provider"
				card.GenerationKind = "text"
			}
		}
		cards = append(cards, card)
	}
	execCount := 0
	for _, c := range cards {
		if c.Kind == "agent" || c.Kind == "provider" {
			execCount++
		}
	}
	if execCount != 1 {
		return canvasAISuggestion{}, errCanvasAI
	}
	edges := []canvasEdge{}
	for _, e := range raw.Edges {
		if e.From < 0 || e.To < 0 || e.From >= len(cards) || e.To >= len(cards) || e.From == e.To {
			return canvasAISuggestion{}, errCanvasAI
		}
		edges = append(edges, canvasEdge{From: cards[e.From].ID, To: cards[e.To].ID})
	}
	if validateCanvasEdges(cards, edges) != nil {
		return canvasAISuggestion{}, errCanvasAI
	}
	if h.validateCanvas(ctx, title, cards) != nil {
		return canvasAISuggestion{}, errCanvasAI
	}
	return canvasAISuggestion{Title: title, Summary: summary, Cards: cards, Edges: edges}, nil
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func (h *handlers) canvasCreateDocument(ctx context.Context, title string, cards []canvasCard, edges []canvasEdge) (string, error) {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return "", errCanvasAI
	}
	if h.validateCanvas(ctx, title, cards) != nil || validateCanvasEdges(cards, edges) != nil {
		return "", errCanvasInvalid
	}
	id, now := db.NewID(), db.Now()
	res, err := h.d.DB.ExecContext(ctx, `INSERT INTO admin_canvases(id,owner_id,title,document_json,edges_json,version,created_at,updated_at) SELECT ?,?,?,?,?,1,?,? WHERE (SELECT count(*) FROM admin_canvases WHERE owner_id=?)<100`,
		id, u.ID, title, canvasEncode(cards), canvasEncode(edges), now, now, u.ID)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", errCanvasLimit
	}
	return id, nil
}
