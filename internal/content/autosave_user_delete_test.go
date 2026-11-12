package content_test

import (
	"context"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// Deleting a user through the auth service (a single DELETE, like every
// other per-user table) also deletes that user's unsaved editor text.
func TestDeletingUserDeletesTheirAutosaves(t *testing.T) {
	app := bootApp(t)
	ctx := context.Background()
	gone := mustUser(t, app, "gone@example.com", "editor")
	stays := mustUser(t, app, "stays@example.com", "editor")
	it, err := contentService(t, app).Create(ctx, content.Input{Type: "post", Title: "Shared"})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*auth.User{gone, stays} {
		if _, err := app.Deps.DB.Exec(`INSERT INTO content_autosaves (content_id, user_id, payload_json, base_edit_token, saved_at)
			VALUES (?, ?, '{"body":["private draft"]}', 'tok', '2026-11-12T00:00:00Z')`, it.ID, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	us, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if err := us.Delete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	var mine, theirs int
	app.Deps.DB.QueryRow(`SELECT count(*) FROM content_autosaves WHERE user_id = ?`, gone.ID).Scan(&mine)
	app.Deps.DB.QueryRow(`SELECT count(*) FROM content_autosaves WHERE user_id = ?`, stays.ID).Scan(&theirs)
	if mine != 0 || theirs != 1 {
		t.Fatalf("autosaves after deleting a user: deleted user %d (want 0), other user %d (want 1)", mine, theirs)
	}
}
