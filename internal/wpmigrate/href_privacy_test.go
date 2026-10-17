package wpmigrate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
)

func TestUnknownHrefPrivacyAcrossReportSurfaces(t *testing.T) {
	for _, mode := range []string{"http404", "unavailable", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			id := e.wxrMigration(Options{})
			e.run(id)
			item := e.item(id, "content", "page", "1107")
			body := `<a href="/protected/href-private-token/a?sig=query-private-token#fragment-private-token">unknown</a><a href="/protected/%65ncoded-private-token/a?sig=query-private-token">encoded</a><a href="` + e.wp.URL() + `/protected/source-private-token/a?sig=query-private-token">source</a><a href="` + item.LocalURL + `">native</a><a href="/media/known-native.png">media</a>`
			if _, err := e.svc.db.Exec(`UPDATE content SET body=?,status='published' WHERE id=?`, body, item.LocalID); err != nil {
				t.Fatal(err)
			}
			probed := map[string]bool{}
			e.svc.check = func(_ context.Context, path string) int {
				probed[path] = true
				if strings.HasPrefix(path, "/protected/") {
					if mode == "http404" {
						return 404
					}
					return 0
				}
				if path == item.LocalURL {
					return 503
				}
				return 200
			}
			if mode == "disabled" {
				e.svc.check = nil
			}
			report, err := e.svc.Validate(e.ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "disabled" {
				for _, original := range []string{"/protected/href-private-token/a", "/protected/encoded-private-token/a"} {
					if !probed[original] {
						t.Fatal("link probe input was replaced with a redacted ID")
					}
				}
			}
			broken, unavailable := 0, 0
			for _, issue := range report.Errors {
				if issue.Kind == "link" && issue.ContentID == item.LocalID {
					broken++
					if issue.NewRef == "" || issue.OldRef == "" {
						t.Fatal("missing safe link evidence")
					}
				}
			}
			for _, issue := range report.UnverifiedChecks {
				if issue.Kind == "link" && issue.ContentID == item.LocalID {
					unavailable++
					if issue.Path == "" || issue.NewRef == "" || issue.OldRef == "" {
						t.Fatal("missing safe unavailable link evidence")
					}
				}
			}
			brokenLinks := 0
			for _, issue := range report.BrokenLinks {
				if issue.ContentID == item.LocalID {
					brokenLinks++
				}
			}
			wantBroken, wantUnavailable := 3, 0
			if mode != "http404" {
				wantBroken, wantUnavailable = 1, 2
			}
			if brokenLinks != wantBroken || broken != wantBroken || unavailable != wantUnavailable {
				t.Fatalf("link evidence broken=%d errors=%d unavailable=%d", brokenLinks, broken, unavailable)
			}
			preserved := false
			for _, issue := range report.RenderFailures {
				if issue.Path == item.LocalURL {
					preserved = true
				}
			}
			for _, issue := range report.UnverifiedChecks {
				if issue.Kind == "render" && issue.Path == item.LocalURL {
					preserved = true
				}
			}
			if !preserved {
				t.Fatal("known native render path was redacted")
			}
			if safeSourceReference("/media/known-native.png?sig=private") != "/media/known-native.png" {
				t.Fatal("native media path was redacted")
			}
			stored, err := e.svc.report(e.ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(stored)
			if err != nil {
				t.Fatal(err)
			}
			client := apitest.New(t, e.app, apitest.Options{Scopes: []string{Permission}})
			status, data := client.Get("/v1/migrations/" + id + "/report")
			if status != 200 {
				t.Fatal(status)
			}
			public, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			response, page := e.browser().do("GET", "/admin/migrations/"+id+"/validate", nil, "")
			if response.StatusCode != 200 {
				t.Fatal(response.StatusCode)
			}
			for _, surface := range []string{string(raw), report.Markdown, page, string(public)} {
				for _, token := range []string{"href-private-token", "encoded-private-token", "%65ncoded-private-token", "source-private-token", "query-private-token", "fragment-private-token"} {
					if strings.Contains(surface, token) {
						t.Error("report surface exposes a synthetic href capability")
					}
				}
			}
		})
	}
}
