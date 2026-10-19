package admin_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func inboundHTTP(t *testing.T, s *site, path, secret, body string) int {
	t.Helper()
	r, _ := http.NewRequest("POST", s.srv.URL+path, strings.NewReader(body))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	delivery := "http_delivery_0000001"
	r.Header.Set("X-GoRun-Timestamp", timestamp)
	r.Header.Set("X-GoRun-Delivery", delivery)
	r.Header.Set("Content-Type", "application/json")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + delivery + "." + body))
	r.Header.Set("X-GoRun-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	// No session cookies and no CSRF header: the HMAC is the receiver credential.
	response, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	return response.StatusCode
}
func TestCanvasInboundHTTPConsentSecretRotationOwnerAndSessionBoundaries(t *testing.T) {
	f := coreCanvasHTTPFixture(t)
	path := "/cards/" + coreAgent + "/inbound"
	code, _ := studioJSON(t, f.s, f.path+path, map[string]any{"action": "configure", "command": "summarize"}, false)
	if code != 403 {
		t.Fatal("configure bypassed CSRF")
	}
	configured := f.post(t, path, map[string]any{"action": "configure", "command": "summarize"}, 200)
	secret := configured["secret"].(string)
	meta := configured["inbound"].(map[string]any)
	endpoint := meta["endpoint_path"].(string)
	if meta["enabled"] != false || meta["execution_mode"] != "external" || meta["agent_id"] != f.key.ID || len(secret) != 64 {
		t.Fatal("configuration metadata", meta)
	}
	var stored string
	if e := f.s.app.Deps.DB.QueryRow(`SELECT secret FROM admin_canvas_inbound`).Scan(&stored); e != nil || strings.Contains(stored, secret) {
		t.Fatal("plaintext stored", e)
	}
	if got := f.get(t, path); got["secret"] != nil {
		t.Fatal("GET redisplayed secret")
	}
	token := f.review(t)
	f.post(t, path, map[string]any{"action": "enable", "review_token": token}, 409)
	reviewed := f.post(t, path, map[string]any{"action": "review"}, 200)
	token = reviewed["review_token"].(string)
	f.post(t, path, map[string]any{"action": "enable", "review_token": token}, 200)
	if code := inboundHTTP(t, f.s, endpoint, secret, `{"command":"summarize"}`); code != 202 {
		t.Fatal("signed session-free receiver", code)
	}
	f.post(t, path, map[string]any{"action": "disable"}, 200)
	if code := inboundHTTP(t, f.s, endpoint, secret, `{"command":"summarize"}`); code != 409 {
		t.Fatal("disabled replay", code)
	}
	f.post(t, path, map[string]any{"action": "enable", "review_token": token}, 409)
	rotated := f.post(t, path, map[string]any{"action": "rotate"}, 200)
	newSecret := rotated["secret"].(string)
	if newSecret == secret {
		t.Fatal("secret reused")
	}
	if code := inboundHTTP(t, f.s, endpoint, secret, `{"command":"summarize"}`); code != 401 {
		t.Fatal("old secret survived rotation", code)
	}
	reviewed = f.post(t, path, map[string]any{"action": "review"}, 200)
	f.post(t, path, map[string]any{"action": "enable", "review_token": reviewed["review_token"]}, 200)
	if code := inboundHTTP(t, f.s, endpoint, newSecret, `{"command":"summarize"}`); code != 200 {
		t.Fatal("durable receipt lost across rotation", code)
	}
	var count int
	f.s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvas_runs`).Scan(&count)
	if count != 1 {
		t.Fatal("replayed run", count)
	}
	if _, e := f.s.app.Deps.DB.Exec(`UPDATE sessions SET reauth_at=?`, time.Now().Add(-11*time.Minute).UTC().Format(time.RFC3339)); e != nil {
		t.Fatal(e)
	}
	response := f.post(t, path, map[string]any{"action": "rotate"}, 403)
	if response["error"] != "reauth_required" {
		t.Fatal(response)
	}
	f.s.login("second-owner@example.invalid", "admin", "en")
	code, _ = studioJSON(t, f.s, f.path+path, map[string]any{"action": "configure", "command": "steal"}, true)
	if code != 404 {
		t.Fatal("cross-owner mutation", code)
	}
}

func TestCanvasInboundTargetRebindCannotReviveOldConsent(t *testing.T) {
	f := coreCanvasHTTPFixture(t)
	path := "/cards/" + coreAgent + "/inbound"
	configured := f.post(t, path, map[string]any{"action": "configure", "command": "summarize"}, 200)
	secret := configured["secret"].(string)
	endpoint := configured["inbound"].(map[string]any)["endpoint_path"].(string)
	keys, _ := kernel.Get[auth.KeyService](f.s.app.Deps, kernel.ServiceAPIKeys)
	_, second, err := keys.CreateKind(context.Background(), auth.KeyKindAgent, "Second worker", f.owner.ID, []string{"canvas.read", "canvas.append", "canvas.run", "content.view"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	f.cards[1]["agent_id"] = second.ID
	f.save(t)
	f.post(t, "/operations/grants", map[string]any{"agent_id": second.ID, "read": true, "append": true, "run": true, "node_ids": []string{corePrompt, coreSource}}, 200)
	reviewed := f.post(t, path, map[string]any{"action": "review"}, 200)
	for _, allowed := range []bool{false, true} {
		if _, err = f.s.app.Deps.DB.Exec(`UPDATE admin_canvas_grants SET can_run=? WHERE board_id=? AND key_id=?`, allowed, f.board, second.ID); err != nil {
			t.Fatal(err)
		}
	}
	f.post(t, path, map[string]any{"action": "enable", "review_token": reviewed["review_token"]}, 409)
	reviewed = f.post(t, path, map[string]any{"action": "review"}, 200)
	f.post(t, path, map[string]any{"action": "enable", "review_token": reviewed["review_token"]}, 200)
	var bound string
	if err = f.s.app.Deps.DB.QueryRow(`SELECT key_id FROM admin_canvas_inbound`).Scan(&bound); err != nil || bound != second.ID {
		t.Fatal("stale key binding", bound, err)
	}
	for _, revoked := range []string{time.Now().UTC().Format(time.RFC3339), ""} {
		if _, err = f.s.app.Deps.DB.Exec(`UPDATE api_keys SET revoked_at=? WHERE id=?`, revoked, second.ID); err != nil {
			t.Fatal(err)
		}
	}
	if code := inboundHTTP(t, f.s, endpoint, secret, `{"command":"summarize"}`); code != 409 {
		t.Fatal("restored B authority revived consent", code)
	}
}
