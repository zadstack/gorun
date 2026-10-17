package wpmigrate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

// fastExamplePress limits media to a few PNGs so full-pipeline runs stay quick.
func (e *env) fastExamplePress(withAuth bool, opts Options) string {
	e.t.Helper()
	if e.wp == nil {
		e.wp = newFixtureServer(e.t)
	}
	e.wp.limitMedia(4, "image/png")
	return e.restMigration(withAuth, opts)
}

func TestPipelineFullRESTRun(t *testing.T) {
	e := newEnv(t)
	id := e.fastExamplePress(false, Options{})
	st := e.run(id)
	if st.Status != StatusValidated {
		t.Fatalf("status = %s (%+v) failed=%v", st.Status, st.Counts, e.failedItems(id))
	}
	if f := e.failedItems(id); len(f) != 0 {
		t.Fatalf("failed items: %+v", f[0])
	}
	if got := st.Counts["content"][ItemDone]; got != 11+27-4 { // cart, checkout, my-account, shop skipped
		t.Fatalf("content done = %d (%v)", got, st.Counts["content"])
	}
	if st.Counts["product"][ItemSkipped] != 36 {
		t.Fatalf("products without commerce must be skipped: %v", st.Counts["product"])
	}
	if st.Counts["media"][ItemDone] != 4 || st.Counts["term"][ItemDone] != 8 {
		t.Fatalf("counts = %v", st.Counts)
	}
	if got := e.app.Deps.Settings.GetString(e.ctx(), "site.title", ""); got != "EXAMPLE PRESS" {
		t.Fatalf("site.title = %q", got)
	}
	if got := e.app.Deps.Settings.GetString(e.ctx(), "site.tagline", ""); got == "" {
		t.Fatal("site.tagline not imported")
	}
}

// TestPipelineIdempotentRerunUpdatesChangedOnly: a second Start only
// touches the item whose WordPress "modified" changed.
func TestPipelineIdempotentRerunUpdatesChangedOnly(t *testing.T) {
	e := newEnv(t)
	id := e.fastExamplePress(false, Options{})
	if st := e.run(id); st.Status != StatusValidated {
		t.Fatalf("first run: %s", st.Status)
	}
	contentBefore := e.count(`SELECT count(*) FROM content`)
	revBefore := e.count(`SELECT count(*) FROM revisions`)
	mediaBefore := e.count(`SELECT count(*) FROM media`)
	termsBefore := e.count(`SELECT count(*) FROM terms`)
	redirectsBefore := e.count(`SELECT count(*) FROM redirects`)
	post := e.item(id, "content", "post", "1154")
	revs := func() map[string]int {
		out := map[string]int{}
		rows, _ := e.app.Deps.DB.Query(`SELECT content_id, count(*) FROM revisions GROUP BY content_id`)
		defer rows.Close()
		for rows.Next() {
			var cid string
			var n int
			_ = rows.Scan(&cid, &n)
			out[cid] = n
		}
		return out
	}
	revsBefore := revs()

	// WordPress edits one post.
	e.wp.override("/wp-json/wp/v2/posts", func(b []byte) []byte {
		var list []map[string]any
		_ = json.Unmarshal(b, &list)
		for _, p := range list {
			if p["id"].(float64) == 1154 {
				p["modified_gmt"] = "2026-10-01T09:00:00"
				p["title"] = map[string]any{"rendered": "عنوان معدّل"}
			}
		}
		out, _ := json.Marshal(list)
		return out
	})
	st := e.run(id)
	if st.Status != StatusValidated {
		t.Fatalf("rerun: %s", st.Status)
	}
	if n := e.count(`SELECT count(*) FROM content`); n != contentBefore {
		t.Fatalf("rerun created content: %d → %d", contentBefore, n)
	}
	if n := e.count(`SELECT count(*) FROM media`); n != mediaBefore {
		t.Fatalf("rerun duplicated media: %d → %d", mediaBefore, n)
	}
	if n := e.count(`SELECT count(*) FROM terms`); n != termsBefore {
		t.Fatalf("rerun duplicated terms: %d → %d", termsBefore, n)
	}
	if n := e.count(`SELECT count(*) FROM redirects`); n != redirectsBefore {
		t.Fatalf("rerun duplicated redirects: %d → %d", redirectsBefore, n)
	}
	// Exactly one content update (+ its rewrite) happened.
	if n := e.count(`SELECT count(*) FROM revisions`); n > revBefore+2 || n == revBefore {
		t.Fatalf("revisions %d → %d: want only the edited post updated", revBefore, n)
	}
	changed := 0
	for cid, n := range revs() {
		if n != revsBefore[cid] {
			changed++
			if cid != post.LocalID {
				t.Errorf("unchanged item %s was rewritten", cid)
			}
		}
	}
	if changed != 1 {
		t.Fatalf("changed items = %d, want 1", changed)
	}
	it, err := e.contentSvc().Get(e.ctx(), post.LocalID)
	if err != nil || it.Title != "عنوان معدّل" {
		t.Fatalf("edited post = %+v %v", it, err)
	}

	// A media item WordPress changed is downloaded again.
	var first string
	e.wp.override("/wp-json/wp/v2/media", nil)
	e.wp.limitMedia(4, "image/png")
	raw, _ := e.svc.items(e.ctx(), id, "media", ItemDone)
	var wm WPMedia
	_ = json.Unmarshal(raw[0].Payload, &wm)
	first = wm.URL
	hits := e.wp.hitCount(pathOf(first))
	base := e.wp.overrides["/wp-json/wp/v2/media"]
	e.wp.override("/wp-json/wp/v2/media", func(b []byte) []byte {
		var list []map[string]any
		_ = json.Unmarshal(base(b), &list)
		for _, it := range list {
			if it["id"].(float64) == float64(wm.ID) {
				it["modified_gmt"] = "2026-10-02T00:00:00"
			}
		}
		out, _ := json.Marshal(list)
		return out
	})
	if st := e.run(id); st.Status != StatusValidated || len(e.failedItems(id)) != 0 {
		t.Fatalf("third run: %s %+v", st.Status, e.failedItems(id))
	}
	if n := e.wp.hitCount(pathOf(first)); n != hits+1 {
		t.Fatalf("changed media downloads %d → %d, want one more", hits, n)
	}
	if n := e.count(`SELECT count(*) FROM media`); n != mediaBefore {
		t.Fatalf("media rows %d → %d", mediaBefore, n)
	}
}

// TestPipelineResumeAfterPause pauses mid-run; queued item jobs leave their
// items pending; Resume finishes without duplicates.
func TestPipelineResumeAfterPause(t *testing.T) {
	e := newEnv(t)
	id := e.fastExamplePress(false, Options{})
	if err := e.svc.Start(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	// Run until the content step has queued its items, then pause.
	for i := 0; i < 500; i++ {
		if _, err := e.q.drain(e.ctx(), 1); err != nil {
			t.Fatal(err)
		}
		m, _ := e.svc.getMigration(e.ctx(), id)
		if m.Step == "content" && e.svc.countPending(e.ctx(), id, "content", -1) > 0 && e.q.len() > 3 {
			break
		}
	}
	if _, err := e.q.drain(e.ctx(), 2); err != nil { // two items get imported
		t.Fatal(err)
	}
	if err := e.svc.Pause(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	st := e.drain(id) // the remaining queued jobs are no-ops now
	if st.Status != StatusPaused || st.Counts["content"][ItemPending] == 0 || st.Counts["content"][ItemDone] < 2 {
		t.Fatalf("after pause: %s %v", st.Status, st.Counts["content"])
	}
	if err := e.svc.Pause(e.ctx(), id); err == nil {
		t.Fatal("pausing a paused migration must fail")
	}
	contentMid := e.count(`SELECT count(*) FROM content`)
	if err := e.svc.Resume(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	st = e.drain(id)
	if st.Status != StatusValidated || st.Counts["content"][ItemPending] != 0 || len(e.failedItems(id)) != 0 {
		t.Fatalf("after resume: %s %v", st.Status, st.Counts)
	}
	if n := e.count(`SELECT count(*) FROM content`); n != st.Counts["content"][ItemDone] || n <= contentMid {
		t.Fatalf("content rows = %d, done items = %d (mid %d)", n, st.Counts["content"][ItemDone], contentMid)
	}
	logs, _ := e.svc.Log(e.ctx(), id, 500)
	var msgs []string
	for _, l := range logs {
		msgs = append(msgs, l.Message)
	}
	joined := strings.Join(msgs, ",")
	if !strings.Contains(joined, "wpmigrate.pause") || !strings.Contains(joined, "wpmigrate.resume") {
		t.Fatalf("log lacks pause/resume: %s", joined)
	}
}

// TestRetryFailedItems: a media download fails, Retry requeues only failed
// items and the rewrite pass then links the recovered file.
func TestRetryFailedItems(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	id := e.wxrMigration(Options{})
	e.wp.fail("/wp-content/uploads/2026/08/cover.png", 500)
	st := e.run(id)
	failed := e.failedItems(id)
	if st.Status != StatusValidated || len(failed) != 1 || failed[0].Kind != "media" || !strings.Contains(failed[0].Error, "500") {
		t.Fatalf("first run: %s failed=%+v", st.Status, failed)
	}
	// Finished with a failure is amber, not green.
	_, page := e.browser().do("GET", "/admin/x/migrations/"+id+"/progress", nil, "", "HX-Request", "true")
	if !strings.Contains(page, "border-amber bg-amber-soft text-amber-text px-1.5 py-0.5 text-xs\" data-status=\"validated\"") || !strings.Contains(page, "1") {
		t.Fatalf("validated with failures must be amber:\n%.1500s", page)
	}
	post := e.item(id, "content", "post", "70")
	body := mustContent(t, e, post.LocalID).Body
	if !strings.Contains(body, "/wp-content/uploads/2026/08/cover-1024x576.png") {
		t.Fatalf("unresolved media should stay as is:\n%s", body)
	}
	if err := e.svc.RetryFailed(e.ctx(), "nope"); err == nil {
		t.Fatal("retry of an unknown migration must fail")
	}
	e.wp.fail("/wp-content/uploads/2026/08/cover.png", 0)
	doneBefore := e.count(`SELECT count(*) FROM migration_items WHERE migration_id = ? AND status = 'done' AND kind = 'content'`, id)
	if err := e.svc.RetryFailed(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	st = e.drain(id)
	if st.Status != StatusValidated || len(e.failedItems(id)) != 0 {
		t.Fatalf("after retry: %s %+v", st.Status, e.failedItems(id))
	}
	if n := e.count(`SELECT count(*) FROM migration_items WHERE migration_id = ? AND status = 'done' AND kind = 'content'`, id); n != doneBefore {
		t.Fatalf("retry re-imported content: %d → %d", doneBefore, n)
	}
	mediaItem := e.item(id, "media", "attachment", "862")
	body = mustContent(t, e, post.LocalID).Body
	if strings.Contains(body, "/wp-content/uploads/") || !strings.Contains(body, mediaItem.LocalURL) {
		t.Fatalf("retry did not rewrite the recovered media (%s):\n%s", mediaItem.LocalURL, body)
	}
	if err := e.svc.RetryFailed(e.ctx(), id); err == nil {
		t.Fatal("nothing left to retry must be an error")
	}
}

// TestDryRunWritesNothing: Preview plans the whole import without writing
// content, media, terms, users, menus or redirects.
func TestDryRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	id := e.fastExamplePress(true, Options{})
	tables := []string{"content", "media", "terms", "users", "menus", "menu_items", "redirects"}
	before := map[string]int{}
	for _, tb := range tables {
		before[tb] = e.count(`SELECT count(*) FROM ` + tb)
	}
	res, err := e.svc.Preview(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range tables {
		if n := e.count(`SELECT count(*) FROM ` + tb); n != before[tb] {
			t.Errorf("dry run wrote %s: %d → %d", tb, before[tb], n)
		}
	}
	if got := e.app.Deps.Settings.GetString(e.ctx(), "site.title", ""); got == "EXAMPLE PRESS" {
		t.Error("dry run wrote site.title")
	}
	if n := e.count(`SELECT count(*) FROM migration_items WHERE migration_id = ? AND status NOT IN ('preview', 'skipped')`, id); n != 0 {
		t.Fatalf("%d items not in preview/skipped", n)
	}
	if n := e.count(`SELECT count(*) FROM migration_items WHERE migration_id = ? AND status = 'preview' AND would_json IS NULL`, id); n != 0 {
		t.Fatalf("%d preview items without a plan", n)
	}
	m, _ := e.svc.Migration(e.ctx(), id)
	if m.Status != StatusPreviewed || !m.DryRun {
		t.Fatalf("migration = %+v", m)
	}
	if res.Counts["content"]["create"] != 34 || res.Counts["media"]["download"] != 4 || res.MediaBytes <= 0 {
		t.Fatalf("preview counts = %v bytes=%d", res.Counts, res.MediaBytes)
	}
	if res.Counts["user"]["create"] != 2 || res.Counts["menu"]["create"] != 1 || res.Counts["redirect"]["create"] == 0 {
		t.Fatalf("preview counts = %v", res.Counts)
	}
	// Six page slugs exist twice under different parents in the fixture.
	dups := 0
	for _, c := range res.SlugConflicts {
		if c.Reason == "duplicate_in_source" && strings.HasSuffix(c.FinalSlug, "-2") {
			dups++
		}
	}
	if dups != 6 {
		t.Fatalf("duplicate slug conflicts = %d: %+v", dups, res.SlugConflicts)
	}
	if res.Shortcodes["ep_plans"] == 0 || !contains(res.Notes, "wpmigrate.preview.needs_commerce") {
		t.Fatalf("preview shortcodes/notes = %v %v", res.Shortcodes, res.Notes)
	}
	// The real run afterwards turns preview items into imports.
	st := e.run(id)
	if st.Status != StatusValidated || st.Counts["content"][ItemDone] != 34 {
		t.Fatalf("real run after preview: %s %v", st.Status, st.Counts["content"])
	}
	if n := e.count(`SELECT count(*) FROM users WHERE role = 'author'`); n != 2 {
		t.Fatalf("authors created = %d", n)
	}
	// A preview after the real run leaves imported items alone.
	done := e.count(`SELECT count(*) FROM migration_items WHERE migration_id = ? AND status = 'done'`, id)
	if _, err := e.svc.Preview(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM migration_items WHERE migration_id = ? AND status = 'done'`, id); n != done {
		t.Fatalf("preview after import changed done items: %d → %d", done, n)
	}
	if n := e.count(`SELECT count(*) FROM migration_items WHERE migration_id = ? AND status = 'preview'`, id); n != 0 {
		t.Fatalf("preview flipped %d imported items", n)
	}
	if n := e.count(`SELECT count(*) FROM menus`); n != before["menus"]+1 {
		t.Fatalf("menus = %d", n)
	}
}

func mustContent(t *testing.T, e *env, id string) *content.Item {
	t.Helper()
	it, err := e.contentSvc().Get(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// TestRetryAfterPauseBeforeFetchImportsEverything: pausing before the first
// step fetched anything, then Retry, must still fetch every step.
func TestRetryAfterPauseBeforeFetchImportsEverything(t *testing.T) {
	e := newEnv(t)
	id := e.fastExamplePress(false, Options{})
	if err := e.svc.Start(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Pause(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	if st := e.drain(id); st.Status != StatusPaused || st.Total != 0 {
		t.Fatalf("paused before fetch: %s total=%d", st.Status, st.Total)
	}
	if err := e.svc.RetryFailed(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	st := e.drain(id)
	if st.Status != StatusValidated || st.Counts["content"][ItemDone] != 34 || st.Counts["media"][ItemDone] != 4 || st.Counts["term"][ItemDone] != 8 {
		t.Fatalf("after retry: %s %v", st.Status, st.Counts)
	}
}

// TestRetryAfterFailedContentFetch: the content step's fetch fails (step
// failed); Retry fetches it again and imports the content.
func TestRetryAfterFailedContentFetch(t *testing.T) {
	e := newEnv(t)
	id := e.fastExamplePress(false, Options{})
	e.wp.fail("/wp-json/wp/v2/posts", 500)
	st := e.run(id)
	if st.Status != StatusFailed || st.Counts["content"][ItemDone] != 0 {
		t.Fatalf("first run: %s %v", st.Status, st.Counts["content"])
	}
	e.wp.fail("/wp-json/wp/v2/posts", 0)
	if err := e.svc.RetryFailed(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	st = e.drain(id)
	if st.Status != StatusValidated || st.Counts["content"][ItemDone] != 34 {
		t.Fatalf("after retry: %s %v", st.Status, st.Counts["content"])
	}
	if n := e.count(`SELECT count(*) FROM media`); n != 4 {
		t.Fatalf("media re-imported: %d", n)
	}
}
