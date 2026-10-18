package content_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	adminmod "github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

type site struct {
	t   *testing.T
	srv *httptest.Server
	app *kernel.App
	c   *http.Client
}

var csrfRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

// loggedIn boots the app and logs in a user with role.
func loggedIn(t *testing.T, role string, extra ...kernel.Module) *site {
	t.Helper()
	app := bootApp(t, extra...)
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	s := &site{t: t, srv: srv, app: app, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	mustUser(t, app, role+"@example.com", role)
	_, page := s.do("GET", "/admin/login", nil, nil)
	resp, _ := s.do("POST", "/admin/login", url.Values{"_csrf": {s.csrf(page)}, "email": {role + "@example.com"}, "password": {"correct horse battery"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	return s
}

func (s *site) csrf(page string) string {
	s.t.Helper()
	m := csrfRe.FindStringSubmatch(page)
	if m == nil {
		s.t.Fatalf("no csrf token in page:\n%s", page)
	}
	return m[1]
}

func (s *site) do(method, path string, form url.Values, hdr map[string]string) (*http.Response, string) {
	s.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, s.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func TestAdminCreateTypeFromForm(t *testing.T) {
	s := loggedIn(t, "admin")
	resp, page := s.do("GET", "/admin/types/new", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(page, "gpTypeBuilder") {
		t.Fatalf("type builder: %d", resp.StatusCode)
	}
	token := s.csrf(page)
	fields := `[{"key":"price","label":{"en":"Price","ar":"السعر"},"type":"number","required":true,"indexed":true,"min":0},
		{"key":"specs","label":{"en":"Specs"},"type":"repeater","sub_fields":[{"key":"name","type":"text"}]}]`
	form := url.Values{"_csrf": {token}, "slug": {"product"}, "name_en": {"Products"}, "name_ar": {"المنتجات"}, "singular_en": {"Product"},
		"public": {"1"}, "has_archive": {"1"}, "supports": {"title", "body", "seo"}, "taxonomies": {"category"}, "fields_json": {fields}}
	resp, _ = s.do("POST", "/admin/types", form, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/types/product" {
		t.Fatalf("create type: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	typ, err := contentService(t, s.app).GetType(context.Background(), "product")
	if err != nil {
		t.Fatal(err)
	}
	if typ.Name["ar"] != "المنتجات" || !typ.Public || !typ.HasArchive || len(typ.Fields) != 2 || !typ.Fields[0].Indexed ||
		typ.Fields[1].SubFields[0].Key != "name" || len(typ.Taxonomies) != 1 || len(typ.Supports) != 3 {
		t.Fatalf("saved type: %+v", typ)
	}

	// Server-side schema validation re-renders the builder with errors.
	form.Set("slug", "gadget")
	form.Set("fields_json", `[{"key":"Bad Key","type":"text"},{"key":"notes","type":"richtext","indexed":true}]`)
	resp, page = s.do("POST", "/admin/types", form, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "fields.0.key") || !strings.Contains(page, "fields.1.indexed") {
		t.Fatalf("invalid schema: %d", resp.StatusCode)
	}
	form.Set("fields_json", `{not json`)
	if resp, _ = s.do("POST", "/admin/types", form, nil); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad JSON: %d", resp.StatusCode)
	}
	if resp, page = s.do("GET", "/admin/types", nil, nil); resp.StatusCode != 200 || !strings.Contains(page, "/admin/types/product") {
		t.Fatalf("types list: %d", resp.StatusCode)
	}
}

var allFieldsType = content.Type{Slug: "kitchen", Name: map[string]string{"en": "Kitchen sinks"}, Public: true,
	Supports: []string{"title", "body", "excerpt", "seo", "featured_image", "revisions"}, Fields: []content.Field{
		{Key: "f_text", Type: "text", Label: map[string]string{"en": "Text"}, Required: true},
		{Key: "f_textarea", Type: "textarea"},
		{Key: "f_richtext", Type: "richtext"},
		{Key: "f_number", Type: "number", Indexed: true},
		{Key: "f_date", Type: "date"},
		{Key: "f_datetime", Type: "datetime"},
		{Key: "f_boolean", Type: "boolean"},
		{Key: "f_select", Type: "select", Options: []content.Option{{Value: "a", Label: map[string]string{"en": "Alpha"}}, {Value: "b"}}},
		{Key: "f_multiselect", Type: "multiselect", Options: []content.Option{{Value: "x"}, {Value: "y"}}},
		{Key: "f_repeater", Type: "repeater", SubFields: []content.Field{{Key: "name", Type: "text"}, {Key: "qty", Type: "number"}}},
		{Key: "f_relationship", Type: "relationship", TargetType: "post", Multiple: true},
		{Key: "f_image", Type: "image"},
		{Key: "f_file", Type: "file"},
		{Key: "f_json", Type: "json"},
	}}

func TestAdminContentEditorRendersAllFieldPartials(t *testing.T) {
	s := loggedIn(t, "admin", media.New()) // the media module serves the picker the editor opens
	cs := contentService(t, s.app)
	ctx := context.Background()
	if err := cs.SaveType(ctx, allFieldsType); err != nil {
		t.Fatal(err)
	}
	related, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Related post"})
	resp, page := s.do("GET", "/admin/content/kitchen/new", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("new editor: %d", resp.StatusCode)
	}
	for _, ft := range content.FieldTypes {
		if !strings.Contains(page, `data-field-type="`+string(ft)+`"`) {
			t.Errorf("editor misses the %s partial", ft)
		}
	}
	for _, want := range []string{"/static/admin/vendor/quill-2.0.3.js", "/admin/x/content/pick?type=post", `name="fields[f_text]"`, "Alpha", `name="featured_media_id"`, `name="seo[title]"`} {
		if !strings.Contains(page, want) {
			t.Errorf("editor misses %q", want)
		}
	}

	// Media inputs: the real picker (one modal per page, a trigger per input
	// whose target is the hidden input's id).
	for _, want := range []string{
		`id="gp-media-picker-body"`, `/admin/x/media/picker?target=`,
		`<input type="hidden" x-ref="input" id="fields-f_image" name="fields[f_image]"`,
		`data-media-open data-target="fields-f_image"`, `data-media-open data-target="fields-f_file"`,
		`data-media-open data-target="featured-media"`, `data-media-open data-target="seo-og-image"`,
		`@media:selected.window=`, `fetch('/admin/x/media/' + encodeURIComponent(value.split(',')[0]) + '/thumb'`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("editor misses media picker markup %q", want)
		}
	}
	if strings.Count(page, `id="gp-media-picker-body"`) != 1 || strings.Contains(page, "content.media.") {
		t.Error("the picker modal must be included exactly once and all media copy translated")
	}
	if resp, body := s.do("GET", "/admin/x/media/picker?target=featured-media", nil, map[string]string{"HX-Request": "true"}); resp.StatusCode != 200 || !strings.Contains(body, `data-target="featured-media"`) {
		t.Fatalf("picker body: %d %s", resp.StatusCode, body)
	}

	if _, picks := s.do("GET", "/admin/x/content/pick?type=post&q=Related", nil, map[string]string{"HX-Request": "true"}); !strings.Contains(picks, `data-id="`+related.ID+`"`) {
		t.Fatalf("picker results: %s", picks)
	}

	// Submit the editor form: values of every type round-trip.
	form := url.Values{"_csrf": {s.csrf(page)}, "title": {"مطبخ"}, "body": {"<p>hi</p><script>x()</script>"}, "status": {"draft"},
		"fields[f_text]": {"hello"}, "fields[f_number]": {"42"}, "fields[f_date]": {"2026-10-10"}, "fields[f_datetime]": {"2026-10-10T09:30"},
		"fields[f_boolean]": {"0", "1"}, "fields[f_select]": {"a"}, "fields[f_multiselect]": {"", "x", "y"},
		"fields[f_repeater][]": {""}, "fields[f_repeater][5][name]": {"bolt"}, "fields[f_repeater][5][qty]": {"3"},
		"fields[f_repeater][17][name]": {"nut"}, "fields[f_relationship]": {"", related.ID}, "fields[f_image]": {"01MEDIA"},
		"fields[f_json]": {`{"a":1}`}, "seo[title]": {"SEO"}, "intent": {"save"}}
	resp, _ = s.do("POST", "/admin/content/kitchen", form, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create from editor: %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	id := loc[strings.LastIndex(loc, "/")+1:]
	it, err := cs.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	f := it.Fields
	rows, _ := f["f_repeater"].([]any)
	if it.Title != "مطبخ" || strings.Contains(it.Body, "script") || f["f_number"] != 42.0 || f["f_boolean"] != true ||
		f["f_datetime"] != "2026-10-10T09:30:00Z" || len(f["f_multiselect"].([]any)) != 2 || len(rows) != 2 ||
		rows[0].(map[string]any)["name"] != "bolt" || f["f_image"] != "01MEDIA" || it.SEO["title"] != "SEO" {
		b, _ := json.Marshal(it)
		t.Fatalf("saved item: %s", b)
	}
	resp, page = s.do("GET", loc, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(page, `value="bolt"`) || !strings.Contains(page, "2026-10-10T09:30") ||
		!strings.Contains(page, `id="fields-f_image" name="fields[f_image]" value="01MEDIA"`) {
		t.Fatalf("edit page: %d", resp.StatusCode)
	}

	// A missing required field re-renders with the error, keeping input.
	form.Set("fields[f_text]", "")
	form.Set("title", "kept title")
	resp, page = s.do("POST", loc, form, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "field-error") || !strings.Contains(page, "kept title") {
		t.Fatalf("invalid save: %d", resp.StatusCode)
	}

	// Review Focus 4: retype fields after content exists; the editor still renders.
	broken := allFieldsType
	broken.Fields = []content.Field{{Key: "f_text", Type: "number"}, {Key: "f_repeater", Type: "select", Options: []content.Option{{Value: "q"}}},
		{Key: "f_json", Type: "repeater", SubFields: []content.Field{{Key: "z", Type: "text"}}}, {Key: "f_boolean", Type: "multiselect", Options: []content.Option{{Value: "q"}}}}
	if err := cs.SaveType(ctx, broken); err != nil {
		t.Fatal(err)
	}
	if resp, _ = s.do("GET", loc, nil, nil); resp.StatusCode != 200 {
		t.Fatalf("editor after schema change: %d", resp.StatusCode)
	}

	// The repeater row endpoint and the HTMX list.
	if err := cs.SaveType(ctx, allFieldsType); err != nil {
		t.Fatal(err)
	}
	resp, page = s.do("GET", "/admin/x/content/kitchen/row?field=f_repeater", nil, map[string]string{"HX-Request": "true"})
	if resp.StatusCode != 200 || !strings.Contains(page, `name="fields[f_repeater][`) {
		t.Fatalf("row: %d %s", resp.StatusCode, page)
	}
	resp, page = s.do("GET", "/admin/content/kitchen?search=%D9%85%D8%B7%D8%A8%D8%AE", nil, map[string]string{"HX-Request": "true"})
	if resp.StatusCode != 200 || !strings.Contains(page, `id="content-list"`) || !strings.Contains(page, "مطبخ") || strings.Contains(page, "<html") {
		t.Fatalf("htmx list: %d", resp.StatusCode)
	}
}

func TestAdminMediaSlotWithoutPicker(t *testing.T) {
	// No media module: inputs keep their value but offer no picker.
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	if err := cs.SaveType(context.Background(), allFieldsType); err != nil {
		t.Fatal(err)
	}
	resp, page := s.do("GET", "/admin/content/kitchen/new", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(page, `name="featured_media_id"`) || !strings.Contains(page, `name="fields[f_image]"`) {
		t.Fatalf("editor: %d", resp.StatusCode)
	}
	if strings.Contains(page, "data-media-open") || strings.Contains(page, "gp-media-picker-body") {
		t.Fatal("picker offered without the media module")
	}
	// With the media module but without media.upload (contributor): no picker either.
	c := loggedIn(t, "contributor", media.New())
	_, page = c.do("GET", "/admin/content/post/new", nil, nil)
	if !strings.Contains(page, `name="fields[`) && !strings.Contains(page, `name="title"`) {
		t.Fatalf("contributor editor did not render:\n%s", page)
	}
	if strings.Contains(page, "data-media-open") || strings.Contains(page, "gp-media-picker-body") {
		t.Fatal("picker offered to a user without media.upload")
	}
}

func TestAdminTrashBulkAndOwnership(t *testing.T) {
	s := loggedIn(t, "author")
	cs := contentService(t, s.app)
	ctx := context.Background()
	other, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Not mine"})
	if resp, _ := s.do("GET", "/admin/content/post/"+other.ID, nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("author opening another's item: %d", resp.StatusCode)
	}
	resp, page := s.do("GET", "/admin/content/post", nil, nil)
	if resp.StatusCode != 200 || strings.Contains(page, "Not mine") {
		t.Fatalf("author list shows only own items: %d", resp.StatusCode)
	}
	_, page = s.do("GET", "/admin/content/post/new", nil, nil)
	form := url.Values{"_csrf": {s.csrf(page)}, "title": {"Mine"}, "intent": {"save"}, "status": {"draft"}}
	if resp, _ = s.do("POST", "/admin/content/post", form, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("author create: %d", resp.StatusCode)
	}
	// authors lack content.delete in the default role seed
	if resp, _ = s.do("POST", "/admin/content/post/bulk", url.Values{"_csrf": {s.csrf(page)}, "ids": {other.ID}}, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("author bulk trash: %d", resp.StatusCode)
	}

	admin := loggedIn(t, "admin", adminmod.New()) // the admin module owns the /admin dashboard
	acs := contentService(t, admin.app)
	a, _ := acs.Create(ctx, content.Input{Type: "post", Title: "A"})
	b, _ := acs.Create(ctx, content.Input{Type: "post", Title: "B"})
	_, page = admin.do("GET", "/admin/content/post", nil, nil)
	resp, _ = admin.do("POST", "/admin/content/post/bulk", url.Values{"_csrf": {admin.csrf(page)}, "action": {"trash"}, "ids": {a.ID, b.ID}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("bulk: %d", resp.StatusCode)
	}
	for _, id := range []string{a.ID, b.ID} {
		if it, _ := acs.Get(ctx, id); it.Status != "trash" {
			t.Fatalf("bulk did not trash %s", id)
		}
	}
	resp, page = admin.do("GET", "/admin", nil, nil)
	// the content_counts widget renders a per-type table with a row for posts
	if resp.StatusCode != 200 || !regexp.MustCompile(`(?s)<table id="content-counts".*?href="/admin/content/post".*?</table>`).MatchString(page) {
		t.Fatalf("dashboard content_counts widget: %d\n%s", resp.StatusCode, page)
	}
}

func TestAdminTrashRestoreAndTypeDelete(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	it, _ := cs.Create(ctx, content.Input{Type: "post", Title: "v1"})
	in := content.InputFromItem(it)
	in.Title = "v2"
	cs.Update(ctx, it.ID, in)
	_, page := s.do("GET", "/admin/content/post/"+it.ID, nil, nil)
	token := s.csrf(page)
	revs, _ := cs.Revisions(ctx, it.ID)
	resp, _ := s.do("POST", "/admin/content/post/"+it.ID+"/revisions/"+revs[len(revs)-1].ID+"/restore", url.Values{"_csrf": {token}}, nil)
	if got, _ := cs.Get(ctx, it.ID); resp.StatusCode != http.StatusSeeOther || got.Title != "v1" {
		t.Fatalf("restore: %d %q", resp.StatusCode, got.Title)
	}
	if resp, _ = s.do("POST", "/admin/content/post/"+it.ID+"/delete", url.Values{"_csrf": {token}}, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("hard delete outside trash: %d", resp.StatusCode)
	}
	s.do("POST", "/admin/content/post/"+it.ID+"/trash", url.Values{"_csrf": {token}}, nil)
	if got, _ := cs.Get(ctx, it.ID); got.Status != "trash" {
		t.Fatal("not trashed")
	}
	if resp, page = s.do("GET", "/admin/content/post?status=trash", nil, nil); !strings.Contains(page, "v1") {
		t.Fatalf("trash list: %d", resp.StatusCode)
	}
	s.do("POST", "/admin/content/post/"+it.ID+"/delete", url.Values{"_csrf": {token}}, nil)
	if _, err := cs.Get(ctx, it.ID); err == nil {
		t.Fatal("not deleted")
	}
	cs.SaveType(ctx, content.Type{Slug: "tmp", Name: map[string]string{"en": "Tmp"}})
	if resp, page = s.do("GET", "/admin/types/tmp", nil, nil); resp.StatusCode != 200 || !strings.Contains(page, "/admin/types/tmp/delete") {
		t.Fatalf("type edit page: %d", resp.StatusCode)
	}
	s.do("POST", "/admin/types/tmp", url.Values{"_csrf": {token}, "name_en": {"Temp"}, "fields_json": {"[]"}}, nil)
	if typ, _ := cs.GetType(ctx, "tmp"); typ.Name["en"] != "Temp" {
		t.Fatalf("type update: %+v", typ)
	}
	s.do("POST", "/admin/types/tmp/delete", url.Values{"_csrf": {token}}, nil)
	if _, err := cs.GetType(ctx, "tmp"); err == nil {
		t.Fatal("type not deleted")
	}
	if resp, _ = s.do("POST", "/admin/types/post/delete", url.Values{"_csrf": {token}}, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("system type delete redirects: %d", resp.StatusCode)
	}
	if resp, _ = s.do("GET", "/admin/content/nope", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown type list: %d", resp.StatusCode)
	}
}

// The editor lists the supported locales: the current one, a link to an existing translation, and a
// button that copies the item into a missing locale; a linked item can leave its translations.
func TestAdminTranslationsPanel(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	it, _ := cs.Create(ctx, content.Input{Type: "post", Title: "عربي", Locale: "ar"})
	_, page := s.do("GET", "/admin/content/post/"+it.ID, nil, nil)
	action := `formaction="/admin/content/post/` + it.ID + `/translations"`
	if !strings.Contains(page, action) || !strings.Contains(page, `name="locale" value="en"`) || strings.Contains(page, `value="ar" formaction`) {
		t.Fatalf("panel before: %s", page)
	}
	resp, _ := s.do("POST", "/admin/content/post/"+it.ID+"/translations", url.Values{"_csrf": {s.csrf(page)}, "locale": {"en"}}, nil)
	list, _ := cs.Translations(ctx, it.ID)
	if len(list) != 2 || resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d %d", resp.StatusCode, len(list))
	}
	var en content.Item
	for _, m := range list {
		if m.Locale == "en" {
			en = m
		}
	}
	if loc := resp.Header.Get("Location"); loc != "/admin/content/post/"+en.ID {
		t.Fatalf("redirect to the new draft: %q", loc)
	}
	_, page = s.do("GET", "/admin/content/post/"+it.ID, nil, nil)
	if !strings.Contains(page, `href="/admin/content/post/`+en.ID+`"`) || strings.Contains(page, action) {
		t.Fatalf("panel after: %s", page)
	}
	// a second copy into the same locale is refused with a flash, nothing is created
	resp, _ = s.do("POST", "/admin/content/post/"+it.ID+"/translations", url.Values{"_csrf": {s.csrf(page)}, "locale": {"en"}}, nil)
	if list, _ = cs.Translations(ctx, it.ID); len(list) != 2 || resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("taken: %d %d", resp.StatusCode, len(list))
	}
	resp, _ = s.do("POST", "/admin/content/post/"+en.ID+"/translations/unlink", url.Values{"_csrf": {s.csrf(page)}}, nil)
	if list, _ = cs.Translations(ctx, it.ID); len(list) != 1 || resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("unlink: %d %d", resp.StatusCode, len(list))
	}
}

// Content → Comments lists comments by status (pending first) and moderates them.
func TestAdminCommentModeration(t *testing.T) {
	s := loggedIn(t, "editor")
	cs := contentService(t, s.app)
	ctx := context.Background()
	typ, _ := cs.GetType(ctx, "post")
	typ.Supports = append(typ.Supports, "comments")
	if err := cs.SaveType(ctx, *typ); err != nil {
		t.Fatal(err)
	}
	it, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Open", Status: "published"})
	comments, _ := kernel.Get[content.CommentService](s.app.Deps, content.ServiceComments)
	c, err := comments.SubmitComment(context.Background(), content.CommentInput{ContentID: it.ID, AuthorName: "Sara",
		AuthorEmail: "sara@example.com", Body: "<b>hi</b>"})
	if err != nil {
		t.Fatal(err)
	}
	resp, page := s.do("GET", "/admin/comments", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	if !strings.Contains(page, "&lt;b&gt;hi&lt;/b&gt;") || !strings.Contains(page, "sara@example.com") || !strings.Contains(page, `href="/admin/content/post/`+it.ID+`"`) {
		t.Fatalf("pending list:\n%s", page)
	}
	resp, _ = s.do("POST", "/admin/comments/"+c.ID+"/status", url.Values{"_csrf": {s.csrf(page)}, "status": {"approved"}}, nil)
	if got, _ := comments.GetComment(ctx, c.ID); resp.StatusCode != http.StatusSeeOther || got.Status != content.CommentApproved {
		t.Fatalf("approve: %d %+v", resp.StatusCode, got)
	}
	resp, _ = s.do("POST", "/admin/comments/"+c.ID+"/delete", url.Values{"_csrf": {s.csrf(page)}}, nil)
	if _, err := comments.GetComment(ctx, c.ID); resp.StatusCode != http.StatusSeeOther || err == nil {
		t.Fatalf("delete: %d %v", resp.StatusCode, err)
	}
	author := loggedIn(t, "author")
	if resp, _ = author.do("GET", "/admin/comments", nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("author: %d", resp.StatusCode)
	}
}
