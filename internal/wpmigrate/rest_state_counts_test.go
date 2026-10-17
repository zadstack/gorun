package wpmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoveryCountsReadableContentByTypeAndSourceState(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	e.wp.override("/wp-json/wp/v2/posts", func(_ []byte) []byte {
		rows := []map[string]any{}
		for i, state := range []string{"publish", "draft", "pending", "future", "private"} {
			rows = append(rows, map[string]any{"id": i + 1, "status": state, "slug": state, "title": map[string]any{"raw": state}, "content": map[string]any{"raw": "<p>Fixture</p>"}})
		}
		b, _ := json.Marshal(rows)
		return b
	})
	src, err := e.svc.CreateSource(e.ctx(), SourceInput{URL: e.wp.URL(), Username: fixtureUser, AppPassword: fixturePass})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := e.svc.Discover(e.ctx(), src.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		ContentStateCounts map[string]map[string]int `json:"content_state_counts"`
	}
	if err = json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	expected := map[string]int{"publish": 1, "draft": 1, "pending": 1, "future": 1, "private": 1}
	if !reflect.DeepEqual(result.ContentStateCounts["post"], expected) {
		t.Fatalf("readable state counts=%v, want%v", result.ContentStateCounts["post"], expected)
	}
}

func TestWizardShowsReadableAndSelectedStateCountsHTTP(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	e.wp.override("/wp-json/wp/v2/posts", func(_ []byte) []byte {
		rows := []map[string]any{}
		for i, state := range []string{"publish", "draft", "pending", "future", "private"} {
			rows = append(rows, map[string]any{"id": i + 1, "status": state, "slug": state, "title": map[string]any{"raw": state}, "content": map[string]any{"raw": "<p>Fixture</p>"}})
		}
		b, _ := json.Marshal(rows)
		return b
	})
	id := e.restMigration(true, Options{})
	if err := e.svc.SaveMapping(e.ctx(), id, Mapping{Types: map[string]TypeMap{"post": {Target: "post"}}}); err != nil {
		t.Fatal(err)
	}
	b := e.browser()
	for _, step := range []string{"scan", "preview"} {
		response, body := b.post("/admin/migrations/"+id+"/"+step, nil)
		if response.StatusCode != 200 {
			t.Fatalf("%s status%d", step, response.StatusCode)
		}
		if !strings.Contains(body, "data-content-state-counts") {
			t.Fatalf("%s lacks content-state totals", step)
		}
		for _, state := range []string{"publish", "draft", "pending", "future", "private"} {
			if !strings.Contains(body, `data-source-type="post" data-source-state="`+state+`" data-readable-count="1"`) {
				t.Fatalf("%s lacks one post in state %s", step, state)
			}
		}
		if strings.Contains(body, "wpmigrate.content_states.") {
			t.Fatalf("%s contains untranslated content-state labels", step)
		}
	}
}

func TestPreviewCountsSelectedContentByTypeAndSourceState(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	e.wp.override("/wp-json/wp/v2/posts", func(_ []byte) []byte {
		rows := []map[string]any{}
		for i, state := range []string{"publish", "draft", "pending", "future", "private"} {
			rows = append(rows, map[string]any{"id": i + 1, "status": state, "slug": state, "date_gmt": "2035-01-01T00:00:00", "title": map[string]any{"raw": state}, "content": map[string]any{"raw": "<p>Fixture</p>"}})
		}
		b, _ := json.Marshal(rows)
		return b
	})
	id := e.restMigration(true, Options{})
	if err := e.svc.SaveMapping(e.ctx(), id, Mapping{Types: map[string]TypeMap{"post": {Target: "post"}}}); err != nil {
		t.Fatal(err)
	}
	preview, err := e.svc.Preview(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		ContentStateCounts map[string]map[string]int `json:"content_state_counts"`
	}
	if err = json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	expected := map[string]map[string]int{"post": {"publish": 1, "draft": 1, "pending": 1, "future": 1, "private": 1}}
	if len(preview.ContentStates) != 5 || !reflect.DeepEqual(result.ContentStateCounts, expected) {
		t.Fatalf("selected state counts=%v, want%v; rows%d", result.ContentStateCounts, expected, len(preview.ContentStates))
	}
}

func TestRESTRejectsContentStateOutsideRequestedScope(t *testing.T) {
	for _, test := range []struct {
		name, state string
		auth        bool
	}{
		{"authenticated_attachment_state", "inherit", true},
		{"authenticated_custom_state", "custom_review", true},
		{"anonymous_draft", "draft", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixtureServer(t)
			fixture.override("/wp-json/wp/v2/posts", func(_ []byte) []byte {
				b, _ := json.Marshal([]map[string]any{{"id": 1, "status": test.state, "slug": "unsafe-state", "title": map[string]any{"raw": "Fixture"}, "content": map[string]any{"raw": "<p>Private source</p>"}}})
				return b
			})
			username, password := "", ""
			if test.auth {
				username, password = fixtureUser, fixturePass
			}
			client, err := NewClient(fixture.URL(), username, password, fixture.srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			rows, err := newRESTSource(client).Content(context.Background(), []string{"post"})
			if !errors.Is(err, ErrUnavailable) || len(rows) != 0 {
				t.Fatalf("outside-scope state accepted: rows%d err%v", len(rows), err)
			}
		})
	}
}

func TestDiscoveryDoesNotCountUnexpectedContentStateAsReadable(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	e.wp.override("/wp-json/wp/v2/posts", func(_ []byte) []byte {
		b, _ := json.Marshal([]map[string]any{{"id": 1, "status": "inherit", "title": map[string]any{"raw": "Fixture"}}})
		return b
	})
	src, err := e.svc.CreateSource(e.ctx(), SourceInput{URL: e.wp.URL(), Username: fixtureUser, AppPassword: fixturePass})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := e.svc.Discover(e.ctx(), src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, known := rep.Counts["post"]; known {
		t.Fatal("unexpected state was counted as a readable collection")
	}
	if _, known := rep.ContentStateCounts["post"]; known {
		t.Fatal("unexpected state received an accepted state count")
	}
	for _, finding := range rep.Unavailable {
		if finding.Kind == "type:post" {
			return
		}
	}
	t.Fatal("missing unavailable type:post finding")
}

func TestImportUnexpectedContentStateFailsWithoutPublishing(t *testing.T) {
	e := newEnv(t)
	id := e.fastExamplePress(true, Options{})
	e.wp.override("/wp-json/wp/v2/posts", func(_ []byte) []byte {
		b, _ := json.Marshal([]map[string]any{{"id": 1, "status": "inherit", "slug": "unexpected-state", "title": map[string]any{"raw": "Fixture"}, "content": map[string]any{"raw": "<p>Hidden content</p>"}}})
		return b
	})
	if state := e.run(id); state.Status != StatusFailed {
		t.Fatalf("unexpected source state finished as %s", state.Status)
	}
	if count := e.count("SELECT count(*) FROM content"); count != 0 {
		t.Fatalf("unexpected state created %d content rows", count)
	}
}
