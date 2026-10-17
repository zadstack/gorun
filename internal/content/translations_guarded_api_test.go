package content_test

import (
	"encoding/json"
	"testing"
)

func TestAPITranslationExpectedIDsAreOptionalButEnforced(t *testing.T) {
	app := bootApp(t)
	c := newAPIClient(t, app, mustUser(t, app, "guard-admin@example.com", "admin"))
	create := func(locale string) string {
		t.Helper()
		code, env := c.do("POST", "/v1/content", map[string]any{"type": "page", "title": "Guard " + locale, "locale": locale})
		if code != 201 {
			t.Fatal(code)
		}
		var row map[string]any
		if err := json.Unmarshal(env.Data, &row); err != nil {
			t.Fatal(err)
		}
		return row["id"].(string)
	}
	en, ar := create("en"), create("ar")
	path := "/v1/content/" + en + "/translations/link"
	for _, expected := range [][]string{{}, {en}, {en, en}, {en, ar, "unknown"}} {
		code, env := c.do("POST", path, map[string]any{"id": ar, "expected_ids": expected})
		if code != 409 || env.Error.Code != "conflict" {
			t.Fatalf("guard: %d %s", code, env.Error.Code)
		}
	}
	for _, id := range []string{en, ar} {
		code, env := c.do("GET", "/v1/content/"+id+"/translations", nil)
		var rows []map[string]any
		json.Unmarshal(env.Data, &rows)
		if code != 200 || len(rows) != 1 {
			t.Fatal("conflict changed membership")
		}
	}
	code, _ := c.do("POST", path, map[string]any{"id": ar, "expected_ids": []string{ar, en}})
	if code != 200 {
		t.Fatalf("valid pair: %d", code)
	}
	code, _ = c.do("POST", path, map[string]any{"id": ar, "expected_ids": []string{en}})
	if code != 409 {
		t.Fatal("already linked bypassed guard")
	}
	code, _ = c.do("POST", path, map[string]any{"id": ar})
	if code != 200 {
		t.Fatal("omitted guard broke legacy compatibility")
	}
	author := newAPIClient(t, app, mustUser(t, app, "guard-author@example.com", "author"))
	code, _ = author.do("POST", path, map[string]any{"id": ar, "expected_ids": []string{en, ar}})
	if code != 403 {
		t.Fatalf("ownership: %d", code)
	}
}

func TestAPITranslationExplicitNullCannotFallBackToLegacy(t *testing.T) {
	app := bootApp(t)
	c := newAPIClient(t, app, mustUser(t, app, "null-guard@example.com", "admin"))
	create := func(locale string) string {
		t.Helper()
		code, env := c.do("POST", "/v1/content", map[string]any{"type": "page", "title": "Null guard " + locale, "locale": locale})
		if code != 201 {
			t.Fatal(code)
		}
		var row map[string]any
		if err := json.Unmarshal(env.Data, &row); err != nil {
			t.Fatal(err)
		}
		return row["id"].(string)
	}
	en, ar, foreign := create("en"), create("ar"), create("en")
	if _, err := app.Deps.DB.Exec(`UPDATE content SET locale='fr',translation_group=? WHERE id=?`, en, foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Deps.DB.Exec(`UPDATE content SET translation_group=? WHERE id=?`, en, en); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, body := range []map[string]any{{"id": ar, "expected_ids": nil}, {"id": ar, "EXPECTED_IDS": nil}, {"id": ar, "expected_ids": []string{en, ar}, "unknown": true}} {
		code, _ := c.do("POST", "/v1/content/"+en+"/translations/link", body)
		if code != 400 {
			t.Fatalf("invalid guarded body must reject before mutation: %d", code)
		}
	}
	var after int
	if err := app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("rejection wrote audit")
	}
	for id, want := range map[string]string{en: en, foreign: en, ar: ""} {
		var got string
		if err := app.Deps.DB.QueryRow(`SELECT coalesce(translation_group,'') FROM content WHERE id=?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatal("rejection changed group")
		}
	}
	code, _ := c.do("POST", "/v1/content/"+en+"/translations/link", map[string]any{"id": ar})
	if code != 200 {
		t.Fatal("omitted field must retain legacy behavior")
	}
}
