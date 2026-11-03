package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

func (e *collectEnv) apiKey(scopes ...string) string {
	e.t.Helper()
	svc, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	u, err := svc.Create(context.Background(), "key-"+strings.Join(scopes, "-")+"@example.com", "Key", "correct horse battery", "admin")
	if err != nil {
		e.t.Fatal(err)
	}
	keys, _ := kernel.Get[auth.KeyService](e.app.Deps, kernel.ServiceAPIKeys)
	plain, _, err := keys.Create(context.Background(), "ci", u.ID, scopes, 0, "")
	if err != nil {
		e.t.Fatal(err)
	}
	return plain
}

func TestAPISummary(t *testing.T) {
	e := bootAdmin(t, nil)
	now := time.Now().UTC()
	e.seed(viewAt(now, "/a", "v1"), viewAt(now, "/b", "v2"), viewAt(now.AddDate(0, 0, -3), "/a", "v1"))
	key := e.apiKey(PermView)
	from, to := now.AddDate(0, 0, -6).Format(time.DateOnly), now.Format(time.DateOnly)

	resp, body := e.do("GET", "/api/v1/analytics/summary?from="+from+"&to="+to, nil, "Authorization", "Bearer "+key)
	if resp.StatusCode != 200 {
		t.Fatalf("summary: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Data struct {
			Summary struct {
				Visitors, Views int
				Prev            *struct{ Visitors int }
			}
			Series []struct {
				At              string
				Visitors, Views int
			}
			Top map[string][]struct {
				Key             string
				Visitors, Views int
			}
		}
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	d := out.Data
	if d.Summary.Visitors != 2 || d.Summary.Views != 3 || d.Summary.Prev == nil {
		t.Errorf("summary = %+v", d.Summary)
	}
	if len(d.Series) != 7 {
		t.Errorf("series = %d points, want 7", len(d.Series))
	}
	if pages := d.Top["pages"]; len(pages) != 2 || pages[0].Key != "/a" || pages[0].Views != 2 {
		t.Errorf("top pages = %+v", pages)
	}
	for _, k := range []string{"pages", "sources", "campaigns", "countries", "devices", "browsers", "os"} {
		if _, ok := d.Top[k]; !ok {
			t.Errorf("top lacks %q", k)
		}
	}

	resp, body = e.do("GET", "/api/v1/analytics/summary?page=%2Fb", nil, "Authorization", "Bearer "+key)
	if resp.StatusCode != 200 || !strings.Contains(body, `"visitors":1`) {
		t.Errorf("filtered summary: %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do("GET", "/api/v1/analytics/summary?from=2026-13-01", nil, "Authorization", "Bearer "+key); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad from: %d, want 400", resp.StatusCode)
	}
	if resp, _ := e.do("GET", "/api/v1/analytics/summary?from="+to+"&to="+from, nil, "Authorization", "Bearer "+key); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("reversed range: %d, want 400", resp.StatusCode)
	}

	long := now.AddDate(0, 0, -400).Format(time.DateOnly) // 401 days with to included
	resp, body = e.do("GET", "/api/v1/analytics/summary?from="+long+"&to="+to, nil, "Authorization", "Bearer "+key)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "400 days") {
		t.Errorf("401-day range: %d %s, want 400 with a clear message", resp.StatusCode, body)
	}
	ok400 := now.AddDate(0, 0, -399).Format(time.DateOnly)
	if resp, body := e.do("GET", "/api/v1/analytics/summary?from="+ok400+"&to="+to, nil, "Authorization", "Bearer "+key); resp.StatusCode != 200 {
		t.Errorf("400-day range: %d %s", resp.StatusCode, body)
	}
	for _, p := range []string{"page", "ref", "country", "device"} {
		v := "/" + strings.Repeat("x", 512)
		resp, body := e.do("GET", "/api/v1/analytics/summary?"+p+"="+v, nil, "Authorization", "Bearer "+key)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, p) {
			t.Errorf("%s of 513 bytes: %d %s, want 400", p, resp.StatusCode, body)
		}
	}
	if resp, _ := e.do("GET", "/api/v1/analytics/summary?page=/"+strings.Repeat("x", 511), nil, "Authorization", "Bearer "+key); resp.StatusCode != 200 {
		t.Errorf("page of 512 bytes: %d", resp.StatusCode)
	}

	other := e.apiKey("content.view")
	if resp, _ := e.do("GET", "/api/v1/analytics/summary", nil, "Authorization", "Bearer "+other); resp.StatusCode != http.StatusForbidden {
		t.Errorf("without scope: %d, want 403", resp.StatusCode)
	}

	resp, body = e.get("/api/v1/openapi.json")
	if resp.StatusCode != 200 || !strings.Contains(body, "/analytics/summary") {
		t.Errorf("openapi does not list the operation (%d)", resp.StatusCode)
	}
}
