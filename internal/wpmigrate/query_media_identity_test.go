package wpmigrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/media"
)

func TestWXRMediaQueryIdentityKeepsDistinctInlineBytes(t *testing.T) {
	e := newEnv(t)
	firstBytes, secondBytes := tinyPNG(), redPNG()
	if bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("fixture PNGs must differ")
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/image.png" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		switch r.URL.Query().Get("id") {
		case "first":
			w.Write(firstBytes)
		case "second":
			w.Write(secondBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer source.Close()
	firstURL, secondURL := source.URL+"/image.png?id=first&kind=original", source.URL+"/image.png?id=second&kind=original"
	xml := `<?xml version="1.0"?><rss version="2.0" xmlns:wp="http://wordpress.org/export/1.2/" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Query identity</title><link>https://source.test</link><wp:wxr_version>1.2</wp:wxr_version><wp:base_site_url>https://source.test</wp:base_site_url><wp:base_blog_url>https://source.test</wp:base_blog_url>`
	for i, raw := range []string{firstURL, secondURL} {
		xml += fmt.Sprintf(`<item><title>Attachment %d</title><link>%s</link><wp:post_id>%d</wp:post_id><wp:post_type>attachment</wp:post_type><wp:status>inherit</wp:status><wp:post_date_gmt>2020-01-01 12:00:00</wp:post_date_gmt><wp:attachment_url>%s</wp:attachment_url></item>`, i+1, html.EscapeString(raw), i+1, html.EscapeString(raw))
	}
	body := `<img src="` + html.EscapeString(firstURL) + `"><picture><source type="image/png" srcset="` + html.EscapeString(firstURL) + ` 1x, ` + html.EscapeString(secondURL) + ` 2x"><img src="` + html.EscapeString(secondURL) + `"></picture>`
	xml += `<item><title>Query article</title><link>https://source.test/query-article</link><wp:post_id>10</wp:post_id><wp:post_type>post</wp:post_type><wp:post_name>query-article</wp:post_name><wp:status>publish</wp:status><wp:post_date_gmt>2020-01-01 12:00:00</wp:post_date_gmt><content:encoded><![CDATA[` + body + `]]></content:encoded><wp:postmeta><wp:meta_key>_thumbnail_id</wp:meta_key><wp:meta_value>1</wp:meta_value></wp:postmeta></item></channel></rss>`
	path := filepath.Join(t.TempDir(), "query-identity.xml")
	if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewWXR(e.ctx(), e.app.Deps, WXRFile{Path: path}, Options{})
	if err != nil || preview.MediaCount != 2 {
		t.Fatalf("dry-run: %+v %v", preview, err)
	}
	if e.count(`SELECT count(*) FROM media`) != 0 || e.count(`SELECT count(*) FROM content`) != 0 {
		t.Fatal("dry-run wrote content/media")
	}
	result, err := ImportWXRFile(e.ctx(), e.app.Deps, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	first := e.item(result.MigrationID, "media", "attachment", "1")
	second := e.item(result.MigrationID, "media", "attachment", "2")
	if first.LocalID == second.LocalID || first.LocalURL == second.LocalURL {
		t.Fatal("import collapsed distinct query identities")
	}
	readLocal := func(raw string) []byte {
		t.Helper()
		response, err := e.srv.Client().Get(e.srv.URL + raw)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("local media HTTP status=%d err=%v", response.StatusCode, err)
		}
		return data
	}
	for i, attachment := range []*itemRow{first, second} {
		expected := [][]byte{firstBytes, secondBytes}[i]
		if !bytes.Equal(readLocal(attachment.LocalURL), expected) {
			t.Fatalf("attachment %d downloaded the wrong bytes", i+1)
		}
	}
	post := e.item(result.MigrationID, "content", "post", "10")
	for importRound := range 3 {
		if importRound > 0 {
			if importRound == 2 {
				xml = strings.Replace(xml, "Query article", "Updated query article", 1)
			}
			if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ImportWXRFile(e.ctx(), e.app.Deps, path, Options{}); err != nil {
				t.Fatal(err)
			}
		}
		item, err := e.contentSvc().Get(e.ctx(), post.LocalID)
		if err != nil || item.FeaturedMediaID != first.LocalID {
			t.Fatalf("featured media identity: %+v %v", item, err)
		}
		refs := inlineMediaReferences(item.Body)
		expected := []*itemRow{first, first, second, second}
		if len(refs) != len(expected) {
			t.Fatalf("inline references: %v", refs)
		}
		for i, ref := range refs {
			if ref != expected[i].LocalURL {
				t.Errorf("round %d inline reference %d selects wrong query identity: got %s want %s", importRound, i, ref, expected[i].LocalURL)
			}
			wantBytes := firstBytes
			if expected[i].LocalID == second.LocalID {
				wantBytes = secondBytes
			}
			if sha256.Sum256(readLocal(ref)) != sha256.Sum256(wantBytes) {
				t.Errorf("round %d inline reference %d returns wrong PNG bytes despite HTTP200", importRound, i)
			}
		}
	}
	stored, err := e.contentSvc().Get(e.ctx(), post.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	wrongBody := strings.ReplaceAll(stored.Body, first.LocalURL, second.LocalURL)
	if _, err := e.svc.db.Exec(`UPDATE content SET body=? WHERE id=?`, wrongBody, post.LocalID); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWXRFile(e.ctx(), e.app.Deps, path, Options{}); err != nil {
		t.Fatal(err)
	}
	unchanged, err := e.contentSvc().Get(e.ctx(), post.LocalID)
	if err != nil || unchanged.Body != wrongBody {
		t.Fatalf("unchanged finished rerun unexpectedly rebuilt localized content: %v", err)
	}
	xml = strings.Replace(xml, "Updated query article", "Reviewed source repair", 1)
	if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWXRFile(e.ctx(), e.app.Deps, path, Options{}); err != nil {
		t.Fatal(err)
	}
	repaired, err := e.contentSvc().Get(e.ctx(), post.LocalID)
	if err != nil || repaired.Title != "Reviewed source repair" || inlineMediaReferences(repaired.Body)[0] != first.LocalURL {
		t.Fatalf("explicit changed-source re-import did not rebuild correct mapping: %v", err)
	}
	if !bytes.Equal(readLocal(inlineMediaReferences(repaired.Body)[0]), firstBytes) {
		t.Fatal("changed-source repair still returns wrong inline PNG bytes")
	}
	t.Log("Unchanged completed rerun preserves already-localized body; changed source payload repairs its mapping using existing IDs.")
	if e.count(`SELECT count(*) FROM media`) != 2 || e.count(`SELECT count(*) FROM content`) != 1 {
		t.Fatal("re-import/update duplicated content or media")
	}
}

func queryRewriteFixture(t *testing.T) (*env, *migrationRow) {
	t.Helper()
	e := newEnv(t)
	source, err := e.svc.CreateSource(e.ctx(), SourceInput{Name: "Query fixture", URL: "https://source.test/blog"})
	if err != nil {
		t.Fatal(err)
	}
	migration, err := e.svc.CreateMigration(e.ctx(), source.ID, Options{}, e.adm.ID)
	if err != nil {
		t.Fatal(err)
	}
	row, err := e.svc.getMigration(e.ctx(), migration.ID)
	if err != nil {
		t.Fatal(err)
	}
	return e, row
}

func queryFixtureUpload(t *testing.T, e *env, raw string, data []byte) *media.Media {
	t.Helper()
	item, err := e.svc.media.Upload(e.ctx(), "fixture.png", bytes.NewReader(data), media.UploadMeta{SourceURL: raw, AuthorID: e.adm.ID})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func queryFixtureCurrent(t *testing.T, e *env, migration *migrationRow, item *media.Media, raw string, sizes ...string) {
	t.Helper()
	payload, err := json.Marshal(WPMedia{URL: raw, Sizes: sizes})
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprint(e.count(`SELECT count(*) FROM migration_items`) + 1)
	if _, err := e.svc.upsertItem(e.ctx(), migration.ID, itemRow{Kind: "media", WPType: "attachment", WPID: id, WPURL: raw, Payload: payload}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.db.Exec(`UPDATE migration_items SET status='done',local_id=?,local_url=? WHERE migration_id=? AND wp_id=?`, item.ID, item.URL, migration.ID, id); err != nil {
		t.Fatal(err)
	}
}

func TestQueryMediaAliasesPreserveExactIdentity(t *testing.T) {
	e, migration := queryRewriteFixture(t)
	firstRaw := "https://source.test/blog/غلاف.png?id=first"
	secondRaw := "https://source.test/blog/غلاف.png?id=second"
	first := queryFixtureUpload(t, e, firstRaw, tinyPNG())
	second := queryFixtureUpload(t, e, secondRaw, redPNG())
	custom := "https://cdn.test/custom-size.png?id=first"
	queryFixtureCurrent(t, e, migration, first, firstRaw, custom)
	queryFixtureCurrent(t, e, migration, second, secondRaw, "https://cdn.test/custom-size.png?id=second")
	rw, err := e.svc.newRewriter(e.ctx(), migration)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, raw string
		item      *media.Media
	}{
		{"relative", "غلاف.png?id=first", first},
		{"root-relative", "/blog/غلاف.png?id=second", second},
		{"protocol-relative", "//source.test/blog/غلاف.png?id=second", second},
		{"existing aliases", "http://www.source.test/blog/غلاف.png?id=first", first},
		{"encoded Arabic", "https://source.test/blog/%D8%BA%D9%84%D8%A7%D9%81.png?id=first", first},
		{"WP size", "https://source.test/blog/غلاف-300x200.png?id=second", second},
		{"scaled", "https://source.test/blog/غلاف-scaled.png?id=first", first},
		{"custom size CDN", custom, first},
		{"unknown ambiguous query", "https://source.test/blog/غلاف.png?id=unknown", nil},
		{"unknown ambiguous size query", "https://source.test/blog/غلاف-300x200.png?id=unknown", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := ""
			if tc.item != nil {
				want = tc.item.URL
			}
			if got := rw.mappedMedia(e.ctx(), tc.raw); got != want {
				t.Fatalf("query alias mapped to wrong identity: got %s want %s", got, want)
			}
		})
	}
}

func TestQueryMediaCacheBustingFallbackIsUnambiguous(t *testing.T) {
	e, migration := queryRewriteFixture(t)
	raw := "https://source.test/blog/Cover.png?id=only"
	item := queryFixtureUpload(t, e, raw, tinyPNG())
	queryFixtureCurrent(t, e, migration, item, raw, "https://cdn.test/custom.png?id=size-one", "https://cdn.test/custom.png?id=size-two")
	rw, err := e.svc.newRewriter(e.ctx(), migration)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{raw + "&cache=123", "https://cdn.test/custom.png?cache=456"} {
		if got := rw.mappedMedia(e.ctx(), ref); got != item.URL {
			t.Fatalf("one-ID cache-busting fallback changed: got %s want %s", got, item.URL)
		}
	}
	if got := rw.mappedMedia(e.ctx(), "https://source.test/blog/cover.png?id=only"); got != "" {
		t.Fatal("media path case was normalized")
	}
}

func TestQueryMediaPriorExactSourceWinsBeforeCurrentFallback(t *testing.T) {
	for _, name := range []string{"shared", "غلاف"} {
		t.Run(name, func(t *testing.T) {
			e, migration := queryRewriteFixture(t)
			firstRaw, secondRaw := "http://source.test/blog/"+name+".png?id=first", "http://source.test/blog/"+name+".png?id=second"
			first := queryFixtureUpload(t, e, firstRaw, tinyPNG())
			prior := queryFixtureUpload(t, e, secondRaw, redPNG())
			queryFixtureCurrent(t, e, migration, first, firstRaw)
			rw, err := e.svc.newRewriter(e.ctx(), migration)
			if err != nil {
				t.Fatal(err)
			}
			if got := rw.mappedMedia(e.ctx(), secondRaw); got != prior.URL {
				t.Fatalf("prior exact source hidden by current cache fallback: got %s want %s", got, prior.URL)
			}
			if got := rw.mappedMedia(e.ctx(), "http://source.test/blog/"+name+".png?id=unknown"); got != "" {
				t.Fatal("current and prior query-backed sources were guessed as one asset")
			}
		})
	}
}

func TestQueryMediaRevisionsRemainOneFallbackIdentity(t *testing.T) {
	e, migration := queryRewriteFixture(t)
	raw := "https://source.test/blog/revised.png?id=only"
	queryFixtureUpload(t, e, raw, tinyPNG())
	current := queryFixtureUpload(t, e, raw+media.RevisionMarker+"fixture", redPNG())
	queryFixtureCurrent(t, e, migration, current, raw)
	rw, err := e.svc.newRewriter(e.ctx(), migration)
	if err != nil {
		t.Fatal(err)
	}
	if got := rw.mappedMedia(e.ctx(), raw+"&cache=2"); got != current.URL {
		t.Fatalf("older revision made current source fallback ambiguous: got %s want %s", got, current.URL)
	}
}

func TestQueryMediaEmptyRunDoesNotRequireNativeMediaRows(t *testing.T) {
	e, migration := queryRewriteFixture(t)
	if _, err := e.svc.db.Exec(`ALTER TABLE media RENAME TO fixture_media_unavailable`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.newRewriter(e.ctx(), migration); err != nil {
		t.Fatalf("empty-media rewrite acquired a new native media dependency: %v", err)
	}
}

func TestQueryMediaEncodedPathSeparatorStaysDistinct(t *testing.T) {
	e, migration := queryRewriteFixture(t)
	firstRaw := "https://source.test/blog/literal%3Fpart.png?id=first"
	secondRaw := "https://source.test/blog/literal?part.png?id=first"
	first := queryFixtureUpload(t, e, firstRaw, tinyPNG())
	second := queryFixtureUpload(t, e, secondRaw, redPNG())
	queryFixtureCurrent(t, e, migration, first, firstRaw)
	queryFixtureCurrent(t, e, migration, second, secondRaw)
	rw, err := e.svc.newRewriter(e.ctx(), migration)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		raw  string
		item *media.Media
	}{{firstRaw, first}, {secondRaw, second}} {
		if got := rw.mappedMedia(e.ctx(), tc.raw); got != tc.item.URL {
			t.Fatalf("encoded path separator collided with query: got %s want %s", got, tc.item.URL)
		}
	}
}

func TestQueryMediaPriorOnlyContentFallbackDoesNotGuess(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			e, migration := queryRewriteFixture(t)
			raw := "https://source.test/blog/قديم.png?id=first"
			prior := queryFixtureUpload(t, e, raw, tinyPNG())
			if ambiguous {
				queryFixtureUpload(t, e, "https://source.test/blog/قديم.png?id=second", redPNG())
			}
			ref := raw + "&cache=123"
			item, err := e.contentSvc().Create(e.ctx(), content.Input{Type: "post", Title: "Prior-only media", Body: `<img src="` + html.EscapeString(ref) + `">`})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.svc.upsertItem(e.ctx(), migration.ID, itemRow{Kind: "content", WPType: "post", WPID: "10"}, false); err != nil {
				t.Fatal(err)
			}
			if _, err := e.svc.db.Exec(`UPDATE migration_items SET status='done',local_id=? WHERE migration_id=? AND kind='content'`, item.ID, migration.ID); err != nil {
				t.Fatal(err)
			}
			rw, err := e.svc.newRewriter(e.ctx(), migration)
			if err != nil {
				t.Fatal(err)
			}
			want := prior.URL
			if ambiguous {
				want = ""
			}
			if got := rw.mappedMedia(e.ctx(), ref); got != want {
				t.Fatalf("prior-only content fallback identity: got %s want %s", got, want)
			}
		})
	}
}

func TestWXRMediaEscapedSlashesKeepOriginBytes(t *testing.T) {
	e := newEnv(t)
	paths := []string{"/literal%2Fpart.png", "/literal%2fpart.png", "/literal/part.png"}
	payloads := [][]byte{tinyPNG(), redPNG(), append(redPNG(), 0)}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i, path := range paths {
			if r.URL.EscapedPath() == path {
				w.Header().Set("Content-Type", "image/png")
				w.Write(payloads[i])
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer source.Close()
	xml := `<?xml version="1.0"?><rss version="2.0" xmlns:wp="http://wordpress.org/export/1.2/" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Escaped paths</title><link>https://source.test</link><wp:wxr_version>1.2</wp:wxr_version>`
	body := ""
	for i, path := range paths {
		raw := source.URL + path + "?id=same"
		xml += fmt.Sprintf(`<item><title>Attachment %d</title><wp:post_id>%d</wp:post_id><wp:post_type>attachment</wp:post_type><wp:status>inherit</wp:status><wp:attachment_url>%s</wp:attachment_url></item>`, i, i+1, html.EscapeString(raw))
		body += `<img src="` + html.EscapeString(raw) + `">`
	}
	xml += `<item><title>Escaped article</title><wp:post_id>10</wp:post_id><wp:post_type>post</wp:post_type><wp:status>publish</wp:status><content:encoded><![CDATA[` + body + `]]></content:encoded></item></channel></rss>`
	file := filepath.Join(t.TempDir(), "escaped-paths.xml")
	if err := os.WriteFile(file, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := ImportWXRFile(e.ctx(), e.app.Deps, file, Options{})
	if err != nil {
		t.Fatal(err)
	}
	post := e.item(result.MigrationID, "content", "post", "10")
	item, err := e.contentSvc().Get(e.ctx(), post.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	refs := inlineMediaReferences(item.Body)
	if len(refs) != len(paths) {
		t.Fatalf("inline references: %v", refs)
	}
	for i, ref := range refs {
		attachment := e.item(result.MigrationID, "media", "attachment", fmt.Sprint(i+1))
		if ref != attachment.LocalURL {
			t.Errorf("origin path %s mapped to %s instead of %s", paths[i], ref, attachment.LocalURL)
		}
		response, err := e.srv.Client().Get(e.srv.URL + ref)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK || !bytes.Equal(data, payloads[i]) {
			t.Errorf("origin path %s returned wrong PNG bytes: status=%d err=%v", paths[i], response.StatusCode, readErr)
		}
	}
}

func TestQueryMediaEscapedSlashFallbackAndPriorIdentity(t *testing.T) {
	for _, prior := range []bool{false, true} {
		t.Run(fmt.Sprint(prior), func(t *testing.T) {
			e, migration := queryRewriteFixture(t)
			paths := []string{"/blog/literal%2Fpart.png", "/blog/literal%2fpart.png", "/blog/literal/part.png"}
			items := []*media.Media{}
			for i, path := range paths {
				raw := "https://source.test" + path + "?id=same"
				item := queryFixtureUpload(t, e, raw, [][]byte{tinyPNG(), redPNG(), append(redPNG(), 0)}[i])
				items = append(items, item)
				if !prior {
					queryFixtureCurrent(t, e, migration, item, raw)
				}
			}
			rw, err := e.svc.newRewriter(e.ctx(), migration)
			if err != nil {
				t.Fatal(err)
			}
			refs := []string{}
			for _, path := range paths {
				refs = append(refs, "https://source.test"+path+"?id=cache")
			}
			if err := rw.indexPriorMediaPaths(e.ctx(), refs); err != nil {
				t.Fatal(err)
			}
			for i, path := range paths {
				for _, ref := range []string{path + "?id=same", path + "?id=cache", strings.TrimSuffix(path, ".png") + "-300x200.png?id=same", strings.TrimSuffix(path, ".png") + "-scaled.png?id=cache"} {
					if got := rw.mappedMedia(e.ctx(), ref); got != items[i].URL {
						t.Errorf("prior=%v %s mapped to %s instead of %s", prior, ref, got, items[i].URL)
					}
				}
			}
		})
	}
}
