package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

const (
	corePrompt   = "11111111-1111-4111-8111-111111111111"
	coreAgent    = "22222222-2222-4222-8222-222222222222"
	coreSchedule = "33333333-3333-4333-8333-333333333333"
	coreSource   = "44444444-4444-4444-8444-444444444444"
)

type coreCanvasHTTP struct {
	s           *site
	owner       *auth.User
	key         *auth.APIKey
	path, board string
	cards       []map[string]any
	source      *content.Item
	fake        *fakeAI
	version     int64
}

func coreCanvasHTTPFixture(t *testing.T) *coreCanvasHTTP {
	t.Helper()
	s := boot(t, content.New())
	owner := s.login("canvas-audit@example.invalid", "admin", "en")
	keys, ok := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	if !ok {
		t.Fatal("key service missing")
	}
	_, key, err := keys.CreateKind(context.Background(), auth.KeyKindAgent, "External named fixture agent", owner.ID, []string{"canvas.read", "canvas.append", "canvas.run", "content.view"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	item := studioPost(t, s, owner)
	fake := newFakeAI(t, 200, openAIReply("Must not dispatch implicitly"), nil)
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {fake.srv.URL}, "model": {"fixture-model"}})
	path := newCanvas(t, s)
	f := &coreCanvasHTTP{s: s, owner: owner, key: key, path: path, board: strings.TrimPrefix(path, "/admin/canvases/"), source: item, fake: fake, version: 1}
	f.cards = []map[string]any{
		{"id": corePrompt, "kind": "prompt", "body": "Summarize facts without publishing"},
		{"id": coreAgent, "kind": "agent", "agent_id": key.ID},
		{"id": coreSchedule, "kind": "schedule", "interval": "hourly"},
		{"id": coreSource, "kind": "source", "source_type": "post", "source_limit": 5, "source_ids": []string{item.ID}},
	}
	f.save(t)
	f.post(t, "/operations/grants", map[string]any{"agent_id": key.ID, "read": true, "append": true, "run": true, "node_ids": []string{corePrompt, coreSource}}, 200)
	return f
}

func (f *coreCanvasHTTP) post(t *testing.T, suffix string, in any, want int) map[string]any {
	t.Helper()
	status, out := studioJSON(t, f.s, f.path+suffix, in, true)
	if status != want {
		t.Fatalf("%s: status%d want%d %#v", suffix, status, want, out)
	}
	return out
}
func (f *coreCanvasHTTP) get(t *testing.T, suffix string) map[string]any {
	t.Helper()
	response, body := f.s.do("GET", f.path+suffix, nil)
	if response.StatusCode != 200 {
		t.Fatalf("GET%s: %d", suffix, response.StatusCode)
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func (f *coreCanvasHTTP) save(t *testing.T) {
	t.Helper()
	input := canvasInput(f.cards...)
	input["version"] = f.version
	input["edges"] = []map[string]string{{"from": corePrompt, "to": coreAgent}, {"from": coreSource, "to": coreAgent}, {"from": coreSchedule, "to": coreAgent}}
	f.post(t, "/document", input, 200)
	f.version++
}
func (f *coreCanvasHTTP) review(t *testing.T) string {
	t.Helper()
	out := f.post(t, "/operations/preview", map[string]any{"version": f.version, "node_id": coreAgent}, 200)
	text, ok := out["context"].(string)
	if !ok || !strings.Contains(text, f.source.Title) || !strings.Contains(text, "Original saved body") || strings.Contains(text, "<p>") {
		t.Fatalf("review lost selected source facts: %#v", out)
	}
	token, ok := out["review_token"].(string)
	if !ok || token == "" {
		t.Fatal("missing consent token")
	}
	return token
}
func (f *coreCanvasHTTP) noDispatch(t *testing.T, runs int) {
	t.Helper()
	for q, want := range map[string]int{`SELECT count(*) FROM admin_canvas_runs`: runs, `SELECT count(*) FROM jobs WHERE kind='canvas.generate'`: 0, `SELECT count(*) FROM admin_canvas_provenance`: 0} {
		var n int
		if err := f.s.app.Deps.DB.QueryRow(q).Scan(&n); err != nil || n != want {
			t.Fatalf("unexpected dispatch/state: count%d want%d err%v", n, want, err)
		}
	}
	if f.fake.calls.Load() != 0 {
		t.Fatal("read/review/schedule invoked provider")
	}
	if len(canvasGet(t, f.s, f.path)["cards"].([]any)) != 4 {
		t.Fatal("operation added result implicitly")
	}
}

func TestCoreCanvasHTTPScheduleConsentAndDisable(t *testing.T) {
	f := coreCanvasHTTPFixture(t)
	token := f.review(t)
	route := "/operations/schedules/" + coreSchedule
	input := map[string]any{"enabled": true, "review_token": token}
	out := f.post(t, route, input, 200)["schedule"].(map[string]any)
	next := out["next_run_at"].(string)
	at, err := time.Parse(time.RFC3339, next)
	if err != nil || !at.After(time.Now()) || !strings.HasSuffix(next, "Z") || out["enabled"] != true || out["status"] != "enabled" {
		t.Fatalf("schedule state: %#v %v", out, err)
	}
	if again := f.post(t, route, input, 200)["schedule"].(map[string]any); again["next_run_at"] != next {
		t.Fatal("enable retry consumed/rescheduled slot")
	}
	doc := f.get(t, "/document")["schedules"].(map[string]any)[coreSchedule].(map[string]any)
	if doc["next_run_at"] != next || doc["enabled"] != true {
		t.Fatal("document omitted persisted schedule")
	}
	var enabled bool
	var storedNext, state string
	if err := f.s.app.Deps.DB.QueryRow(`SELECT enabled,next_run_at,status FROM admin_canvas_schedules WHERE board_id=? AND node_id=?`, f.board, coreSchedule).Scan(&enabled, &storedNext, &state); err != nil || !enabled || storedNext != next || state != "enabled" {
		t.Fatal(enabled, storedNext, state, err)
	}
	f.cards[0]["body"] = "A different instruction needs fresh consent"
	f.save(t)
	if doc := f.get(t, "/document")["schedules"].(map[string]any)[coreSchedule].(map[string]any); doc["enabled"] != false || doc["status"] != "needs_review" {
		t.Fatal("changed prompt retained old schedule consent", doc)
	}
	if out := f.post(t, route, input, 409); out["error"] != "consent_changed" {
		t.Fatal(out)
	}
	f.post(t, route, map[string]any{"enabled": false}, 200)
	if err := f.s.app.Deps.DB.QueryRow(`SELECT enabled,status FROM admin_canvas_schedules WHERE board_id=?`, f.board).Scan(&enabled, &state); err != nil || enabled || state != "disabled" {
		t.Fatal(enabled, state, err)
	}
	input["review_token"] = f.review(t)
	f.post(t, route, input, 200)
	f.noDispatch(t, 0)
}

func TestCoreCanvasHTTPExpiredAndInvalidScheduleDoNotWrite(t *testing.T) {
	f := coreCanvasHTTPFixture(t)
	token := f.review(t)
	if _, err := f.s.app.Deps.DB.Exec(`UPDATE admin_canvas_reviews SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, token); err != nil {
		t.Fatal(err)
	}
	if out := f.post(t, "/operations/schedules/"+coreSchedule, map[string]any{"enabled": true, "review_token": token}, 409); out["error"] != "review_expired" {
		t.Fatal(out)
	}
	f.post(t, "/operations/schedules/not-a-node", map[string]any{"enabled": true}, 422)
	original := f.cards[2]["interval"]
	f.cards[2]["interval"] = "every-second"
	input := canvasInput(f.cards...)
	input["version"] = f.version
	f.post(t, "/document", input, 422)
	f.cards[2]["interval"] = original
	if doc := f.get(t, "/document"); doc["version"] != float64(f.version) {
		t.Fatal("invalid schedule changed document version")
	}
	var n int
	if err := f.s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvas_schedules`).Scan(&n); err != nil || n != 0 {
		t.Fatal("invalid/expired request wrote schedule", n, err)
	}
	f.noDispatch(t, 0)
}

func TestCoreCanvasHTTPSourceChangesInvalidateSingleRunConsent(t *testing.T) {
	f := coreCanvasHTTPFixture(t)
	token := f.review(t)
	cs, _ := kernel.Get[content.Service](f.s.app.Deps, kernel.ServiceContent)
	input := content.InputFromItem(f.source)
	input.Body = "New facts after consent"
	if _, err := cs.Update(auth.WithUser(context.Background(), f.owner), f.source.ID, input); err != nil {
		t.Fatal(err)
	}
	out := f.post(t, "/operations/runs", map[string]any{"request_id": corePrompt, "review_token": token}, 409)
	if out["error"] != "consent_changed" {
		t.Fatal(out)
	}
	f.noDispatch(t, 0)
}

func TestCoreCanvasHTTPImplicitSourcesRespectAuthorAndPrivateTypes(t *testing.T) {
	f := coreCanvasHTTPFixture(t)
	cs, ok := kernel.Get[content.Service](f.s.app.Deps, kernel.ServiceContent)
	if !ok {
		t.Fatal("content service missing")
	}
	ctx := auth.WithUser(context.Background(), f.owner)
	other := f.s.createUser("canvas-source-other@example.invalid", "author", "en")
	if _, err := cs.Create(ctx, content.Input{Type: "post", Title: "FOREIGN_SOURCE_SENTINEL", Body: "Foreign private draft facts", AuthorID: other.ID}); err != nil {
		t.Fatal(err)
	}
	if err := cs.SaveType(ctx, content.Type{Slug: "audit_private", Name: map[string]string{"en": "Private audit notes"}, Public: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Create(ctx, content.Input{Type: "audit_private", Title: "PRIVATE_TYPE_SENTINEL", AuthorID: f.owner.ID}); err != nil {
		t.Fatal(err)
	}
	options := f.get(t, "/operations/options")
	if strings.Contains(string(mustJSON(t, options["types"])), "audit_private") {
		t.Fatal("private content type offered as operational source")
	}
	f.cards[3]["source_ids"] = []string{}
	f.save(t)
	role := "author"
	if _, err := f.s.users().Update(context.Background(), f.owner.ID, auth.UserPatch{Role: &role}); err != nil {
		t.Fatal(err)
	}
	f.s.signIn(f.owner.Email)
	preview := f.post(t, "/operations/preview", map[string]any{"version": f.version, "node_id": coreAgent}, 200)
	text, ok := preview["context"].(string)
	if !ok || !strings.Contains(text, f.source.Title) || !strings.Contains(text, "Original saved body") || strings.Contains(text, "FOREIGN_SOURCE_SENTINEL") || strings.Contains(text, "PRIVATE_TYPE_SENTINEL") {
		t.Fatalf("implicit source selection crossed author/type boundaries: %q", text)
	}
	response, body := f.s.do("GET", f.path+"/references", nil)
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" || !strings.Contains(body, f.source.ID) || strings.Contains(body, "FOREIGN_SOURCE_SENTINEL") || strings.Contains(body, "PRIVATE_TYPE_SENTINEL") {
		t.Fatalf("source picker disagrees with current read authority: status=%d cache=%q expected_id=%s body=%s", response.StatusCode, response.Header.Get("Cache-Control"), f.source.ID, body)
	}
	f.noDispatch(t, 0)
}

func TestCoreCanvasHTTPRunsAndReadContracts(t *testing.T) {
	f := coreCanvasHTTPFixture(t)
	options := f.get(t, "/operations/options")
	if options["can_share"] != true || len(options["agents"].([]any)) != 1 || len(options["providers"].([]any)) == 0 {
		t.Fatal("configured choices missing", options)
	}
	refs := f.get(t, "/references?q=Saved")["items"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["id"] != f.source.ID {
		t.Fatal("source picker identity", refs)
	}
	grants := f.get(t, "/operations/grants")["grants"].([]any)
	if len(grants) != 1 || grants[0].(map[string]any)["agent_id"] != f.key.ID || grants[0].(map[string]any)["run"] != true {
		t.Fatal("grant readback", grants)
	}
	if len(f.get(t, "/operations/runs")["runs"].([]any)) != 0 {
		t.Fatal("new board has run")
	}
	token := f.review(t)
	input := map[string]any{"request_id": corePrompt, "review_token": token}
	run := f.post(t, "/operations/runs", input, 202)["run"].(map[string]any)
	if run["status"] != "waiting_agent" {
		t.Fatal("external agent should await explicit result", run)
	}
	if retry := f.post(t, "/operations/runs", input, 202)["run"].(map[string]any); retry["id"] != run["id"] {
		t.Fatal("run retry duplicated identity", retry)
	}
	runs := f.get(t, "/operations/runs")["runs"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["id"] != run["id"] {
		t.Fatal("run list disagrees", runs)
	}
	var status, board, owner string
	if err := f.s.app.Deps.DB.QueryRow(`SELECT status,board_id,owner_id FROM admin_canvas_runs WHERE id=?`, run["id"]).Scan(&status, &board, &owner); err != nil || status != "waiting_agent" || board != f.board || owner != f.owner.ID {
		t.Fatal(status, board, owner, err)
	}
	f.post(t, "/operations/runs", map[string]any{"request_id": "invalid", "review_token": token}, 422)
	f.post(t, "/operations/runs", map[string]any{"request_id": coreAgent, "review_token": "missing"}, 409)
	f.noDispatch(t, 1)
}

func TestCoreCanvasHTTPGrantRevocationAndValidation(t *testing.T) {
	f := coreCanvasHTTPFixture(t)
	token := f.review(t)
	grant := map[string]any{"agent_id": f.key.ID, "read": true, "append": true, "run": true, "node_ids": []string{"missing"}}
	f.post(t, "/operations/grants", grant, 422)
	grant["node_ids"] = []string{corePrompt, coreSource}
	grant["agent_id"] = "missing"
	f.post(t, "/operations/grants", grant, 422)
	grant["agent_id"] = f.key.ID
	grant["run"] = false
	f.post(t, "/operations/grants", grant, 200)
	if g := f.get(t, "/operations/grants")["grants"].([]any)[0].(map[string]any); g["run"] != false || g["read"] != true {
		t.Fatal("grant revocation not persisted", g)
	}
	f.post(t, "/operations/preview", map[string]any{"version": f.version, "node_id": coreAgent}, 422)
	f.post(t, "/operations/runs", map[string]any{"request_id": corePrompt, "review_token": token}, 409)
	keys, _ := kernel.Get[auth.KeyService](f.s.app.Deps, kernel.ServiceAPIKeys)
	if err := keys.Revoke(context.Background(), f.key.ID); err != nil {
		t.Fatal(err)
	}
	grant["run"] = true
	if out := f.post(t, "/operations/grants", grant, 422); out["error"] != "invalid_key" {
		t.Fatal(out)
	}
	f.noDispatch(t, 0)
}

func TestCoreCanvasHTTPRoutesProtectOwnerCSRFAndPermissions(t *testing.T) {
	f := coreCanvasHTTPFixture(t)
	token := f.review(t)
	schedule := map[string]any{"enabled": true, "review_token": token}
	for _, suffix := range []string{"/operations/schedules/" + coreSchedule, "/operations/grants", "/operations/runs"} {
		if status, _ := studioJSON(t, f.s, f.path+suffix, schedule, false); status != http.StatusForbidden {
			t.Fatalf("real CSRF middleware %s: %d", suffix, status)
		}
	}
	f.s.login("canvas-other@example.invalid", "admin", "en")
	for _, suffix := range []string{"/operations/options", "/operations/runs", "/operations/grants", "/references", "/document"} {
		response, body := f.s.do("GET", f.path+suffix, nil)
		if response.StatusCode != 404 || strings.Contains(body, f.source.Title) {
			t.Fatal("foreign board leaked", suffix, response.StatusCode)
		}
	}
	f.post(t, "/operations/schedules/"+coreSchedule, schedule, 404)
	f.post(t, "/operations/grants", map[string]any{}, 404)
	f.post(t, "/operations/runs", map[string]any{}, 404)
	f.s.signIn(f.owner.Email)
	if _, err := f.s.app.Deps.DB.Exec(`INSERT INTO roles(slug,name_json,system) VALUES('canvas_auditor','{"en":"Audit reader"}',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.app.Deps.DB.Exec(`INSERT INTO role_permissions(role,permission) VALUES('canvas_auditor','content.view')`); err != nil {
		t.Fatal(err)
	}
	if err := auth.ReloadRoles(context.Background(), f.s.app.Deps.DB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.s.app.Deps.DB.Exec(`DELETE FROM role_permissions WHERE role='canvas_auditor'`); err != nil {
			t.Error(err)
		}
		if err := auth.ReloadRoles(context.Background(), f.s.app.Deps.DB); err != nil {
			t.Error(err)
		}
	})
	role := "canvas_auditor"
	if _, err := f.s.users().Update(context.Background(), f.owner.ID, auth.UserPatch{Role: &role}); err != nil {
		t.Fatal(err)
	}
	f.s.signIn(f.owner.Email)
	if out := f.post(t, "/operations/schedules/"+coreSchedule, schedule, 403); out["error"] != "forbidden" {
		t.Fatal(out)
	}
	response, _ := f.s.do("GET", f.path+"/operations/grants", nil)
	if response.StatusCode != 403 {
		t.Fatal("missing key permission read grants", response.StatusCode)
	}
	f.post(t, "/operations/grants", map[string]any{}, 403)
	f.post(t, "/operations/preview", map[string]any{"version": f.version, "node_id": coreAgent}, 422)
	if f.get(t, "/operations/options")["can_share"] != false {
		t.Fatal("permissions not reflected by choices")
	}
	f.noDispatch(t, 0)
}
