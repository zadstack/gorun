package media_test

import (
	"bytes"
	"context"
	"github.com/zadstack/gorun/internal/media"
	"net/http"
	"strings"
	"testing"
)

func TestLibraryMetadataIsReadOnlyProjectedAndPermissionGuarded(t *testing.T) {
	s := boot(t, "admin")
	m, e := mediaSvc(t, s).Upload(context.Background(), "metadata.png", bytes.NewReader(pngBytes(t, 5, 6)), media.UploadMeta{Alt: "Visible description"})
	if e != nil {
		t.Fatal(e)
	}
	var before int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&before)
	response, body := s.do("GET", "/admin/x/media/"+m.ID+"/details", nil, "")
	if response.StatusCode != 200 || !strings.Contains(response.Header.Get("Cache-Control"), "no-store") || !strings.Contains(body, "Visible description") || strings.Contains(body, "storage_key") || strings.Contains(body, "source_url") {
		t.Fatal("metadata projection", response.StatusCode, body)
	}
	var after int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&after)
	if before != after {
		t.Fatal("metadata GET mutated audit")
	}
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e = anon.Get(s.srv.URL + "/admin/x/media/" + m.ID + "/details")
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatal("anonymous metadata access", response.StatusCode)
	}
	restricted := boot(t, "contributor")
	response, _ = restricted.do("GET", "/admin/x/media/"+m.ID+"/details", nil, "")
	if response.StatusCode != 403 {
		t.Fatal("metadata permission bypass", response.StatusCode)
	}
}
