package content

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"strings"
	"testing"
)

func TestPageAccessCASPermissionAndPublicPagination(t *testing.T) {
	s := newTestService(t)
	if err := db.Migrate(context.Background(), s.db, "auth", auth.New().Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := auth.ReloadRoles(context.Background(), s.db); err != nil {
		t.Fatal(err)
	}
	mustType(t, s, Type{Slug: "page", Public: true, Name: map[string]string{"en": "Page"}})
	a := mustCreate(t, s, Input{Type: "page", Title: "Visible needle", Body: "needle", Status: "published"})
	b := mustCreate(t, s, Input{Type: "page", Title: "Protected needle", Body: "secret needle", Status: "published"})
	admin := auth.WithUser(context.Background(), &auth.User{ID: "owner", Role: "admin", Status: "active"})
	input := PageAccessInput{Mode: "password", Password: "private fixture password", Message: "Members only"}
	if _, err := s.SavePageAccess(context.Background(), b.ID, input, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized save: %v", err)
	}
	p, err := s.SavePageAccess(admin, b.ID, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(p)
	if strings.Contains(string(encoded), "argon2") || strings.Contains(string(encoded), input.Password) {
		t.Fatal("hash leaked in JSON")
	}
	if p.Revision != 1 || !auth.VerifyPassword(p.PasswordHash, input.Password) {
		t.Fatal("password not hashed/revision missing")
	}
	if _, err = s.SavePageAccess(admin, b.ID, PageAccessInput{Mode: "public"}, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	page, err := s.Query(context.Background(), Query{Search: "needle", Status: "published", PublicOnly: true, PerPage: 1})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != a.ID {
		t.Fatalf("public count/query leak: total=%d err=%v", page.Total, err)
	}
	staff, err := s.Query(admin, Query{Status: "published"})
	if err != nil || staff.Total != 2 {
		t.Fatal("staff access lost", err)
	}
	if _, err = s.SavePageAccess(admin, b.ID, PageAccessInput{Mode: "public", ClearPassword: true}, 1); err != nil {
		t.Fatal(err)
	}
	p, err = s.PageAccess(context.Background(), b.ID)
	if err != nil || p.PasswordHash != "" || p.Revision != 2 {
		t.Fatal("clear failed", err)
	}
	page, err = s.Query(context.Background(), Query{Status: "published", PublicOnly: true})
	if err != nil || page.Total != 2 {
		t.Fatal("public restore failed", err)
	}
	hostctx := WithPublicExclusions(context.Background(), []string{strings.ToLower(b.ID)})
	page, err = s.Query(hostctx, Query{Status: "published", PublicOnly: true, PerPage: 1})
	if err != nil || page.Total != 1 || page.Items[0].ID != a.ID || PubliclyReadable(hostctx, s, b.ID) {
		t.Fatal("host exclusion leak", err)
	}
	mustType(t, s, Type{Slug: "private-record", Public: false, Name: map[string]string{"en": "Private"}})
	private := mustCreate(t, s, Input{Type: "private-record", Title: "Private record"})
	if _, err = s.SavePageAccess(admin, private.ID, PageAccessInput{Mode: "public"}, 0); !errors.Is(err, ErrForbidden) {
		t.Fatal("private type accepted", err)
	}
	if _, err = s.SavePageAccess(admin, b.ID, PageAccessInput{Mode: "password", ClearPassword: true}, 2); err == nil {
		t.Fatal("password mode with empty hash")
	}

}

func TestPageAccessPickerTemplateFilterBeforePagination(t *testing.T) {
	s := newTestService(t)
	mustType(t, s, Type{Slug: "page", Public: true, Name: map[string]string{"en": "Page"}, Templates: []string{"builder", "landing"}})
	selected := mustCreate(t, s, Input{Type: "page", Title: "Builder page", Template: "builder", Status: "published"})
	mustCreate(t, s, Input{Type: "page", Title: "Landing page", Template: "landing", Status: "published"})
	p, err := s.Query(context.Background(), Query{Type: "page", Template: "builder", PerPage: 1})
	if err != nil || p.Total != 1 || len(p.Items) != 1 || p.Items[0].ID != selected.ID {
		t.Fatal("template count/filter", err)
	}
	p, err = s.Query(context.Background(), Query{Type: "page", Template: "builder' OR 1=1 --", PerPage: 1})
	if err != nil || p.Total != 0 {
		t.Fatal("template SQL boundary", err)
	}
}

func TestPageAccessRollbackInventoryDoesNotConfuseSchemaWithEnforcement(t *testing.T) {
	s := newTestService(t)
	var exists, count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='content_page_access'`).Scan(&exists); err != nil || exists != 1 {
		t.Fatal("page policy inventory unavailable", err)
	}
	// The count-only recovery check includes draft rules too: an older release
	// must never gain permission to publish them by ignoring this table.
	mustType(t, s, Type{Slug: "page", Public: true, Name: map[string]string{"en": "Page"}})
	it := mustCreate(t, s, Input{Type: "page", Title: "Rollback fixture", Status: "draft"})
	for _, mode := range []string{"blocked", "password", "public"} {
		if _, err := s.db.Exec(`INSERT INTO content_page_access(content_id,mode,message,password_hash,revision,updated_at) VALUES(?,?,'','',1,'fixture') ON CONFLICT(content_id) DO UPDATE SET mode=excluded.mode`, it.ID, mode); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM content_page_access WHERE mode!='public'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count > 0) != (mode != "public") {
			t.Fatal("rollback privacy inventory", mode, count)
		}
	}
}
