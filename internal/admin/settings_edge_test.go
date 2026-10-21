package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func edgeTestHandlers(t *testing.T) *handlers {
	t.Helper()
	app, err := kernel.BootWithOptions(context.Background(), config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}, []kernel.Module{settingsmod.New(), auth.New(), New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	tpl, _ := kernel.Get[*httpx.Templates](app.Deps, kernel.ServiceAdminTemplates)
	return &handlers{d: app.Deps, tpl: tpl}
}

func storeEdgeConnection(t *testing.T, h *handlers, origin, key string) string {
	t.Helper()
	enc, err := db.Encrypt(h.d.Config.SecretKey, []byte(key), edgeKeyAAD)
	if err != nil {
		t.Fatal(err)
	}
	err = db.InTx(context.Background(), h.d.DB, func(tx *sql.Tx) error {
		for k, v := range map[string]string{settingEdgeOrigin: origin, settingEdgeKey: enc, settingEdgeHint: key[len(key)-4:], settingEdgeManager: "", settingEdgeLabel: "", settingEdgeSite: "", settingEdgeChecked: ""} {
			if err := h.d.Settings.SetTx(context.Background(), tx, k, v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

func TestEdgeCheckRejectsConcurrentSettingsSave(t *testing.T) {
	const oldKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const newKey = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, tc := range []struct{ name, origin, key string }{
		{"origin and key", "https://new.example", newKey},
		{"origin only", "https://new.example", oldKey},
		{"key only", "https://old.example", newKey},
		{"same key replaced", "https://old.example", oldKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := edgeTestHandlers(t)
			ctx := context.Background()
			oldCipher := storeEdgeConnection(t, h, "https://old.example", oldKey)
			var nextCipher string
			client := &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "old.example" || r.Header.Get("Authorization") != "Bearer "+oldKey {
					t.Error("check mixed connection credentials")
				}
				// A second settings save finishes while the old remote check is in flight.
				if tc.name == "origin only" {
					if err := h.d.Settings.Set(ctx, settingEdgeOrigin, tc.origin); err != nil {
						t.Fatal(err)
					}
					nextCipher = oldCipher
				} else {
					nextCipher = storeEdgeConnection(t, h, tc.origin, tc.key)
				}
				w := httptest.NewRecorder()
				_, _ = io.WriteString(w, `{"ok":true,"data":{"site_id":"old-site","manager_id":"old-manager","label":"Old manager","primary":true}}`)
				return w.Result(), nil
			})}
			w := httptest.NewRecorder()
			h.settingsEdgeCheckWithClient(w, httptest.NewRequest("POST", "/admin/settings/edge/check", nil), client)
			if w.Code != http.StatusConflict {
				t.Fatalf("stale check status = %d, want 409", w.Code)
			}
			for key, want := range map[string]string{settingEdgeOrigin: tc.origin, settingEdgeKey: nextCipher, settingEdgeManager: "", settingEdgeLabel: "", settingEdgeSite: "", settingEdgeChecked: ""} {
				if got := h.d.Settings.GetString(ctx, key, ""); got != want {
					t.Errorf("concurrent save lost field %s", key)
				}
			}
			if !strings.Contains(w.Body.String(), tc.origin) || !strings.Contains(w.Body.String(), "field-error") {
				t.Fatal("conflict did not render the current settings and error")
			}
			for _, secret := range []string{oldKey, newKey, oldCipher, nextCipher} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatal("conflict response leaked credentials")
				}
			}
			var audited int
			if err := h.d.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='settings.edge_check'`).Scan(&audited); err != nil {
				t.Fatal(err)
			}
			if audited != 0 {
				t.Fatal("stale check was audited as success")
			}
		})
	}
}

func TestEdgeSaveRejectsStaleSnapshot(t *testing.T) {
	h := edgeTestHandlers(t)
	ctx := context.Background()
	const oldKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const newKey = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	storeEdgeConnection(t, h, "https://old.example", oldKey)
	snapshot, err := h.currentEdge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vals, fields, _ := h.edgeValues(edgeInput{Origin: "https://submitted.example"}, snapshot)
	if len(fields) != 0 {
		t.Fatalf("valid blank-key save rejected: %v", fields)
	}
	storeEdgeConnection(t, h, "https://new.example", newKey)
	if err := h.d.Settings.Set(ctx, settingEdgeSite, "new-site"); err != nil {
		t.Fatal(err)
	}
	before, _ := h.d.Settings.All(ctx, "edge.")
	if err := h.saveEdgeKeys(ctx, snapshot, vals); !errors.Is(err, errEdgeConflict) {
		t.Fatalf("stale save: %v", err)
	}
	after, _ := h.d.Settings.All(ctx, "edge.")
	if !maps.EqualFunc(before, after, func(a, b json.RawMessage) bool { return string(a) == string(b) }) {
		t.Fatal("stale save modified newer credentials or identity")
	}
}

func TestEdgeSaveDetectsNewEmptyConnectionSettings(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		value     any
	}{
		{"empty origin", settingEdgeOrigin, ""},
		{"empty key", settingEdgeKey, ""},
		{"null key", settingEdgeKey, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := edgeTestHandlers(t)
			ctx := context.Background()
			snapshot, err := h.currentEdge(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.d.Settings.Set(ctx, tc.key, tc.value); err != nil {
				t.Fatal(err)
			}
			if err := h.saveEdgeKeys(ctx, snapshot, map[string]any{settingEdgeOrigin: "https://submitted.example"}); !errors.Is(err, errEdgeConflict) {
				t.Fatalf("new connection value was treated as missing: %v", err)
			}
		})
	}
}

func TestEdgeSnapshotRemainsCoherentDuringAtomicSaves(t *testing.T) {
	h := edgeTestHandlers(t)
	ctx := context.Background()
	generations := []map[string]string{}
	for _, letter := range []string{"a", "b"} {
		key := strings.Repeat(letter, 43)
		cipher, err := db.Encrypt(h.d.Config.SecretKey, []byte(key), edgeKeyAAD)
		if err != nil {
			t.Fatal(err)
		}
		generations = append(generations, map[string]string{settingEdgeOrigin: "https://" + letter + ".example", settingEdgeKey: cipher, settingEdgeHint: strings.Repeat(letter, 4), settingEdgeManager: letter, settingEdgeLabel: letter, settingEdgeSite: letter, settingEdgeChecked: letter})
	}
	write := func(values map[string]string) error {
		return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
			for key, value := range values {
				if err := h.d.Settings.SetTx(ctx, tx, key, value); err != nil {
					return err
				}
			}
			return nil
		})
	}
	if err := write(generations[0]); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 100; i++ {
			if err := write(generations[i%2]); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	defer func() {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for range 100 {
		snapshot, err := h.currentEdge(ctx)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := db.Decrypt(h.d.Config.SecretKey, snapshot.Cipher, edgeKeyAAD)
		if err != nil {
			t.Fatal(err)
		}
		letter := string(plain[:1])
		page := snapshot.Page
		if page.Origin != "https://"+letter+".example" || page.Hint != strings.Repeat(letter, 4) || page.ManagerID != letter || page.Label != letter || page.SiteID != letter || page.CheckedAt != letter || !page.KeySet {
			t.Fatal("read mixed credentials or page values from different saved configurations")
		}
	}
}

func TestEdgeCheckSuccessAndFailuresPreserveCredentials(t *testing.T) {
	for _, outcome := range []string{"success", "rejected", "unavailable", "corrupt ciphertext", "write failure"} {
		t.Run(outcome, func(t *testing.T) {
			h := edgeTestHandlers(t)
			ctx := context.Background()
			key := strings.Repeat("a", 43)
			cipher := storeEdgeConnection(t, h, "https://edge.example", key)
			if outcome == "corrupt ciphertext" {
				cipher = "invalid encrypted key"
				if err := h.d.Settings.Set(ctx, settingEdgeKey, cipher); err != nil {
					t.Fatal(err)
				}
			}
			if outcome == "write failure" {
				if _, err := h.d.DB.Exec(`CREATE TRIGGER edge_check_blocked BEFORE INSERT ON settings WHEN NEW.key = 'edge.manager_site' BEGIN SELECT RAISE(ABORT, 'edge check blocked'); END`); err != nil {
					t.Fatal(err)
				}
			}
			requests := 0
			client := &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.Host != "edge.example" || r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("check used incorrect credentials")
				}
				if outcome == "unavailable" {
					return nil, errors.New("remote unavailable")
				}
				w := httptest.NewRecorder()
				if outcome == "rejected" {
					w.WriteHeader(http.StatusUnauthorized)
				} else {
					_, _ = io.WriteString(w, `{"ok":true,"data":{"site_id":"demo","manager_id":"mgr-1","label":"GoRun","primary":true}}`)
				}
				return w.Result(), nil
			})}
			w := httptest.NewRecorder()
			h.settingsEdgeCheckWithClient(w, httptest.NewRequest("POST", "/admin/settings/edge/check", nil), client)
			wantStatus := http.StatusUnprocessableEntity
			if outcome == "success" {
				wantStatus = http.StatusSeeOther
			} else if outcome == "write failure" {
				wantStatus = http.StatusInternalServerError
			}
			if w.Code != wantStatus {
				t.Fatalf("check status = %d, want %d", w.Code, wantStatus)
			}
			if outcome == "corrupt ciphertext" && requests != 0 {
				t.Fatal("corrupt ciphertext triggered an external request")
			}
			if h.d.Settings.GetString(ctx, settingEdgeKey, "") != cipher || h.d.Settings.GetString(ctx, settingEdgeOrigin, "") != "https://edge.example" {
				t.Fatal("check changed connection settings")
			}
			for _, setting := range []string{settingEdgeManager, settingEdgeLabel, settingEdgeSite, settingEdgeChecked} {
				value := h.d.Settings.GetString(ctx, setting, "")
				if (value != "") != (outcome == "success") {
					t.Errorf("unexpected saved result for %s", setting)
				}
			}
			if outcome == "success" {
				if _, err := time.Parse(time.RFC3339, h.d.Settings.GetString(ctx, settingEdgeChecked, "")); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), cipher) {
				t.Fatal("check response leaked credentials")
			}
			var audited int
			if err := h.d.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='settings.edge_check'`).Scan(&audited); err != nil {
				t.Fatal(err)
			}
			if (audited == 1) != (outcome == "success") || audited > 1 {
				t.Fatalf("unexpected success audits: %d", audited)
			}
		})
	}
}

func TestEdgeInvalidStoredSettingsFailWithoutMutation(t *testing.T) {
	for _, tc := range []struct{ name, key, raw string }{
		{"origin object", settingEdgeOrigin, `{"secret":"must not appear"}`},
		{"null key", settingEdgeKey, `null`},
		{"numeric hint", settingEdgeHint, `42`},
		{"identity array", settingEdgeSite, `["old-site"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := edgeTestHandlers(t)
			ctx := context.Background()
			key := strings.Repeat("a", 43)
			cipher := storeEdgeConnection(t, h, "https://edge.example", key)
			if _, err := h.d.DB.Exec(`UPDATE settings SET value_json=? WHERE key=?`, tc.raw, tc.key); err != nil {
				t.Fatal(err)
			}
			before, _ := h.d.Settings.All(ctx, "edge.")
			for _, operation := range []string{"page", "save", "check"} {
				w := httptest.NewRecorder()
				r := httptest.NewRequest("POST", "/admin/settings/edge", strings.NewReader(url.Values{"origin": {"https://submitted.example"}, "key": {strings.Repeat("b", 43)}}.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				switch operation {
				case "page":
					h.settingsEdge(w, r)
				case "save":
					h.settingsEdgeSave(w, r)
				case "check":
					h.settingsEdgeCheckWithClient(w, r, &http.Client{Transport: modelTransport(func(*http.Request) (*http.Response, error) {
						t.Error("invalid settings triggered external request")
						return nil, errors.New("unexpected request")
					})})
				}
				if w.Code != http.StatusInternalServerError {
					t.Errorf("%s status = %d, want 500", operation, w.Code)
				}
				for _, secret := range []string{key, cipher, "must not appear"} {
					if strings.Contains(w.Body.String(), secret) {
						t.Fatal("invalid settings response leaked stored data")
					}
				}
			}
			after, _ := h.d.Settings.All(ctx, "edge.")
			if !maps.EqualFunc(before, after, func(a, b json.RawMessage) bool { return string(a) == string(b) }) {
				t.Fatal("invalid settings were overwritten")
			}
		})
	}
}

func TestEdgeSnapshotReadFailure(t *testing.T) {
	h := edgeTestHandlers(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.currentEdge(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("read error became an empty snapshot: %v", err)
	}
}

func TestValidateEdgeOriginRejectsPrivateAndUnqualifiedHosts(t *testing.T) {
	for _, raw := range []string{
		"http://edge.example",
		"https://user:secret@edge.example",
		"https://edge.example/admin",
		"https://edge.example?x=1",
		"https://localhost",
		"https://127.0.0.1",
		"https://10.1.2.3",
		"https://192.168.1.9",
		"https://169.254.169.254",
		"https://edge.local",
		"https://metadata.internal",
		"edge.example",
		"https://example",
	} {
		if _, err := validateEdgeOrigin(raw); err == nil {
			t.Fatalf("%q was accepted", raw)
		}
	}
	got, err := validateEdgeOrigin("https://Edge.Example/")
	if err != nil || got != "https://edge.example" {
		t.Fatalf("normalized origin: %q %v", got, err)
	}
}

func TestEdgeWhoamiAcceptsPrimaryAndRejectsRedirects(t *testing.T) {
	const key = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/manager" || r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"data":{"site_id":"demo","manager_id":"mgr-1","label":"GoRun","primary":true}}`)
	}))
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	info, err := edgeWhoami(context.Background(), client, srv.URL, key)
	if err != nil || info.SiteID != "demo" || info.ManagerID != "mgr-1" || info.Label != "GoRun" {
		t.Fatalf("whoami: %+v %v", info, err)
	}
	if _, err := edgeWhoami(context.Background(), client, srv.URL, key+"nope"); err == nil {
		t.Fatal("wrong key was accepted")
	}
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1/secret", http.StatusFound)
	}))
	t.Cleanup(redirected.Close)
	other := redirected.Client()
	other.CheckRedirect = client.CheckRedirect
	if _, err := edgeWhoami(context.Background(), other, redirected.URL, key); err == nil {
		t.Fatal("redirect was accepted")
	}
	if strings.Contains(redirected.URL, key) {
		t.Fatal("test leaked the key into the origin")
	}
}
