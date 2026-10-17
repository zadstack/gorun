package webhooks

import (
	"context"
	"strings"
	"testing"
)

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://hooks.example.com/in?token=s3cret&x=1": "https://hooks.example.com/in?…",
		"https://hooks.example.com/in":                  "https://hooks.example.com/in",
		"https://hooks.example.com:8443/a/b?k=v":        "https://hooks.example.com:8443/a/b?…",
		"https://hooks.example.com?k=v":                 "https://hooks.example.com?…",
		"::not a url":                                   "",
	} {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
	list := []Webhook{{ID: "a", URL: "https://x.example.com/h?token=s3cret"}}
	red := Redacted(list)
	if red[0].URL != "https://x.example.com/h?…" || list[0].URL != "https://x.example.com/h?token=s3cret" {
		t.Fatalf("Redacted must return a redacted copy: %+v / %+v", red, list)
	}
}

func TestAuditMetaRedactsURLQuery(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	hook, _, err := s.Create(ctx, Input{Name: strp("q"), URL: strp("https://q.example.com/h?token=s3cret"), Events: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	if hook.URL != "https://q.example.com/h?token=s3cret" {
		t.Fatal("storage keeps the full URL")
	}
	newURL := "https://q.example.com/h?token=other"
	if _, err := s.Update(ctx, hook.ID, Input{URL: &newURL}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, hook.ID); got.URL != newURL {
		t.Fatalf("stored: %q", got.URL)
	}
	rows, err := s.db.Query(`SELECT meta_json FROM audit_log WHERE action IN ('webhook.create', 'webhook.update')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var meta string
		_ = rows.Scan(&meta)
		n++
		if strings.Contains(meta, "s3cret") || strings.Contains(meta, "other") || !strings.Contains(meta, "https://q.example.com/h?…") {
			t.Fatalf("audit meta: %s", meta)
		}
	}
	if n != 2 {
		t.Fatalf("audit rows: %d", n)
	}
}
