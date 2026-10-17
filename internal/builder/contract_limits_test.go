package builder

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
)

func TestLayoutRESTBodyLimitReturns413(t *testing.T) {
	f := newLayoutFixture(t)
	admin := f.user(t, "admin")
	it := f.page(t, admin, "Body limit")
	key := f.token(t, admin, []string{"content.edit", "content.publish"})
	for _, tc := range []struct{ method, suffix string }{{"PUT", "/draft"}, {"POST", "/publish"}} {
		status, body := f.request(t, http.DefaultClient, tc.method, "/api/v1/builder/"+it.ID+tc.suffix, key, "", strings.Repeat(" ", 1<<20)+`{}`)
		if status != 413 || !strings.Contains(string(body), `"code":"payload_too_large"`) {
			t.Errorf("%s oversize: %d %s", tc.method, status, body)
		}
	}
}

func TestLayoutRESTStaleCASPreservesNewestDraftAndPublicContent(t *testing.T) {
	f := newLayoutFixture(t)
	admin := f.user(t, "admin")
	it := f.page(t, admin, "CAS fixture")
	key := f.token(t, admin, []string{"content.edit", "content.publish"})
	path := "/api/v1/builder/" + it.ID
	status, body := f.request(t, http.DefaultClient, "GET", path, key, "", nil)
	if status != 200 {
		t.Fatalf("load: %d %s", status, body)
	}
	old := decodeLayoutResponse(t, body)
	section := DefaultSection("hero", "en")
	section.Title = "Newest human draft"
	doc := Document{Version: 1, Sections: []Section{section}}
	status, body = f.request(t, http.DefaultClient, "PUT", path+"/draft", key, "", saveRequest{Document: doc, BaseUpdatedAt: old.UpdatedAt})
	if status != 200 {
		t.Fatalf("save: %d %s", status, body)
	}
	newest := decodeLayoutResponse(t, body)
	doc.Sections[0].Title = "Stale overwrite"
	status, body = f.request(t, http.DefaultClient, "PUT", path+"/draft", key, "", saveRequest{Document: doc, BaseUpdatedAt: old.UpdatedAt})
	if status != 409 || !strings.Contains(string(body), `"code":"conflict"`) {
		t.Fatalf("stale save: %d %s", status, body)
	}
	status, body = f.request(t, http.DefaultClient, "POST", path+"/publish", key, "", layoutPublishRequest{UpdatedAt: old.UpdatedAt})
	if status != 409 || !strings.Contains(string(body), `"code":"conflict"`) {
		t.Fatalf("stale publish: %d %s", status, body)
	}
	status, body = f.request(t, http.DefaultClient, "GET", path, key, "", nil)
	if status != 200 {
		t.Fatalf("reload: %d %s", status, body)
	}
	current := decodeLayoutResponse(t, body)
	if current.UpdatedAt != newest.UpdatedAt || current.Document.Sections[0].Title != "Newest human draft" {
		t.Fatalf("CAS lost newer draft: %+v", current)
	}
	content, err := f.cs.Get(auth.WithUser(context.Background(), admin), it.ID)
	if err != nil || content.Body != it.Body || content.Status != it.Status {
		t.Fatalf("stale publish changed public state: %+v %v", content, err)
	}
}
