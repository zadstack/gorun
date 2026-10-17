package hello

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func sign(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "\n"))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func post(t *testing.T, h http.Handler, secret string, env map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	env["ts"], _ = strconv.Atoi(ts)
	body, _ := json.Marshal(env)
	req := httptest.NewRequest(http.MethodPost, "/gorun", bytes.NewReader(body))
	req.Header.Set("X-GoRun-Timestamp", ts)
	req.Header.Set("X-GoRun-Signature", sign(secret, ts, body))
	req.Header.Set("X-GoRun-Extension", "hello")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHelloFilterAppendsTitle(t *testing.T) {
	h := Handler("k")
	w := post(t, h, "k", map[string]any{"hook": "content.before_save", "kind": "filter", "payload": map[string]any{"title": "Hi", "type": "post"}})
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var out struct {
		Value map[string]any `json:"value"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Value["title"] != "Hi"+Suffix || out.Value["type"] != "post" {
		t.Fatalf("value = %v", out.Value)
	}
}

func TestHelloRecordsEventsAndRejectsBadSignature(t *testing.T) {
	h := Handler("k")
	if w := post(t, h, "wrong", map[string]any{"hook": "content.published", "kind": "event", "payload": map[string]any{}}); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", w.Code)
	}
	if len(h.Events()) != 0 {
		t.Fatal("unsigned delivery recorded")
	}
	if w := post(t, h, "k", map[string]any{"hook": "content.published", "kind": "event", "payload": map[string]any{"id": "1"}, "delivery_id": "d1"}); w.Code != 200 {
		t.Fatalf("event: %d", w.Code)
	}
	ev := h.Events()
	if len(ev) != 1 || ev[0].Hook != "content.published" || ev[0].DeliveryID != "d1" {
		t.Fatalf("events = %+v", ev)
	}
}

func TestHelloFilterIsIdempotent(t *testing.T) {
	h := Handler("k")
	w := post(t, h, "k", map[string]any{"hook": "content.before_save", "kind": "filter", "payload": map[string]any{"title": "Hi" + Suffix}})
	var out struct {
		Value map[string]any `json:"value"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Value["title"] != "Hi"+Suffix {
		t.Fatalf("suffix appended twice: %v", out.Value["title"])
	}
}
