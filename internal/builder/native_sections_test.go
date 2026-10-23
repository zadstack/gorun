package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"golang.org/x/net/html"
)

func nativeDocument(locale string) Document {
	doc := Document{Version: 1}
	for _, kind := range nativeKinds {
		section := DefaultSection(kind, locale)
		section.ID = "native-" + kind
		section.Library = SectionLibrary
		if section.Native == nil {
			section.Native = &NativeOptions{}
		}
		section.Native.Background = "inherit"
		if kind == "hero" {
			section.Variant = "split"
			section.Native.Eyebrow = "Synthetic pilot"
			section.Native.Facts = []SectionFact{{Label: "Themes", Value: "5"}}
			section.Native.SecondaryLink = "#gp-native-faq"
			section.Native.SecondaryLabel = "Questions"
			section.Image = "/media/synthetic.png"
			section.ImageAlt = "Synthetic screenshot"
		}
		if kind == "features" {
			section.Items[0].Icon = "bolt"
			section.Items[0].Featured = true
		}
		if kind == "faq" {
			section.Variant = "split"
		}
		doc.Sections = append(doc.Sections, section)
	}
	return doc
}
func cloneDocument(t *testing.T, doc Document) Document {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var out Document
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func sameDocument(t *testing.T, want, got Document) {
	t.Helper()
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(got)
	if !bytes.Equal(a, b) {
		t.Fatalf("document changed:\nwant %s\ngot %s", a, b)
	}
}

func TestLegacyVersionOneRenderIsByteIdentical(t *testing.T) {
	raw, err := os.ReadFile("testdata/legacy-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/legacy-v1.html")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatal("legacy HTML differs from PR143 5647469a golden output")
	}
}

func TestNativeMarkupSurvivesRealSanitizerAndUsesExistingLibrary(t *testing.T) {
	for _, locale := range []string{"ar", "en"} {
		t.Run(locale, func(t *testing.T) {
			doc := nativeDocument(locale)
			doc.Sections[1].Items[0].Text = `<script>alert(1)</script>`
			rendered, err := Render(doc)
			if err != nil {
				t.Fatal(err)
			}
			cleaned := content.Sanitize(rendered)
			for _, marker := range []string{`class="gp-s-hero gp-s-hero-split gp-s-bg"`, `class="gp-s-facts"`, `<dt>Themes</dt>`, `class="gp-s-card gp-s-card-featured"`, `gp-s-icon-bolt`, `class="gp-s-steps"`, `class="gp-s-compare"`, `tabindex="0"`, `role="region"`, `aria-label="` + doc.Sections[3].Title + `"`, `scope="col"`, `scope="row"`, `class="gp-s-yes"`, `class="gp-s-no"`, `gp-s-faq-cols`, `<details>`, `<summary>`, `class="gp-s-preview-ui"`, `<figcaption>`} {
				if !strings.Contains(cleaned, marker) {
					t.Errorf("native structure lost %s", marker)
				}
			}
			if strings.Contains(cleaned, "<script>") || strings.Contains(cleaned, "gp-builder") {
				t.Fatal("unsafe HTML or legacy wrapper in native document")
			}
			if strings.Count(cleaned, "<h1>") != 1 {
				t.Fatal("native document must have one h1")
			}
		})
	}
}

func TestMixedSectionsHaveOneHeadingAndNoLegacyAncestorForNative(t *testing.T) {
	native := nativeDocument("en").Sections
	for _, first := range []string{"native", "legacy"} {
		t.Run(first, func(t *testing.T) {
			legacy := Section{ID: "legacy", Kind: "text", Title: "Legacy title"}
			sections := []Section{native[0], legacy, native[1]}
			if first == "legacy" {
				sections = []Section{legacy, native[0], native[1]}
			}
			rendered, err := Render(Document{Version: 1, Sections: sections})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(rendered, "<div>") || strings.Count(rendered, "<h1>") != 1 {
				t.Fatal("mixed wrapper/heading changed", rendered)
			}
			tree, err := html.Parse(strings.NewReader(rendered))
			if err != nil {
				t.Fatal(err)
			}
			var visit func(*html.Node, bool)
			visit = func(node *html.Node, inLegacy bool) {
				for _, a := range node.Attr {
					if a.Key == "class" {
						for _, class := range strings.Fields(a.Val) {
							if class == "gp-builder" {
								inLegacy = true
							}
							if strings.HasPrefix(class, "gp-s-") && inLegacy {
								t.Fatalf("native class %s inherits legacy builder background", class)
							}
						}
					}
				}
				for c := node.FirstChild; c != nil; c = c.NextSibling {
					visit(c, inLegacy)
				}
			}
			visit(tree, false)
		})
	}
}

func TestNativeValidationRejectsUnsupportedOrUnboundedStructure(t *testing.T) {
	cases := map[string]func(*Document){
		"blank_question":              func(d *Document) { d.Sections[4].Items[0].Title = " " },
		"blank_button":                func(d *Document) { d.Sections[0].Link = "/go"; d.Sections[0].LinkLabel = " " },
		"blank_feature_link":          func(d *Document) { d.Sections[1].Items[0].Link = "/go"; d.Sections[1].Items[0].Title = " " },
		"blank_comparison_title":      func(d *Document) { d.Sections[3].Title = " " },
		"blank_row":                   func(d *Document) { d.Sections[3].Native.Rows[0].Label = " " },
		"blank_fact_label":            func(d *Document) { d.Sections[0].Native.Facts[0].Label = " " },
		"blank_fact_value":            func(d *Document) { d.Sections[0].Native.Facts[0].Value = " " },
		"scene":                       func(d *Document) { d.Sections[0].HeroScene = "orbit" },
		"feed":                        func(d *Document) { d.Sections[1].Feed = "posts" },
		"missing_comparison_options":  func(d *Document) { d.Sections[3].Native = nil },
		"unknown_library":             func(d *Document) { d.Sections[0].Library = "other" },
		"native_options_on_legacy":    func(d *Document) { d.Sections[0].Library = "" },
		"native_kind_without_library": func(d *Document) { d.Sections[2].Library = ""; d.Sections[2].Native = nil },
		"unrendered_item":             func(d *Document) { d.Sections[2].Items[0].Link = "/ignored" },
		"icon":                        func(d *Document) { d.Sections[1].Items[0].Icon = `bolt onclick=x` },
		"status":                      func(d *Document) { d.Sections[3].Native.Rows[0].Cells[0].Status = "maybe" },
		"column_count":                func(d *Document) { d.Sections[3].Native.Columns = make([]string, 7) },
		"cell_count":                  func(d *Document) { d.Sections[3].Native.Rows[0].Cells = nil },
		"row_limit":                   func(d *Document) { d.Sections[3].Native.Rows = make([]ComparisonRow, 25) },
		"fact_limit":                  func(d *Document) { d.Sections[0].Native.Facts = make([]SectionFact, 9) },
		"background":                  func(d *Document) { d.Sections[0].Native.Background = "custom-class" },
		"secondary_url":               func(d *Document) { d.Sections[0].Native.SecondaryLink = "javascript:alert(1)" },
		"long_label":                  func(d *Document) { d.Sections[0].Native.Facts[0].Label = strings.Repeat("x", 301) },
		"long_cell":                   func(d *Document) { d.Sections[3].Native.Rows[0].Cells[0].Text = strings.Repeat("x", 12001) },
		"unlabeled_illustration":      func(d *Document) { d.Sections[5].Native.Caption = "" },
		"screenshot_and_illustration": func(d *Document) { d.Sections[5].Image = "/real.png" },
		"unsupported_variant":         func(d *Document) { d.Sections[2].Variant = "split" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			doc := cloneDocument(t, nativeDocument("en"))
			change(&doc)
			if err := Validate(doc); err == nil {
				t.Fatal("unsupported native option accepted")
			}
		})
	}
}

func TestNativeDraftSaveReloadPublishAndConflict(t *testing.T) {
	s, cs, ctx := builderTest(t)
	item, err := cs.Create(ctx, content.Input{Type: "page", Title: "Native page", Body: "<p>Original body</p>", Excerpt: "Keep excerpt", Locale: "ar", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := s.Load(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	doc := nativeDocument("ar")
	saved, err := s.Save(ctx, item.ID, doc, initial.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	sameDocument(t, doc, loaded.Document)
	fresh, _ := cs.Get(ctx, item.ID)
	if fresh.Body != item.Body {
		t.Fatal("draft save published native HTML")
	}
	if _, err := s.Save(ctx, item.ID, doc, initial.UpdatedAt); !errors.Is(err, ErrConflict) {
		t.Fatal("stale native save accepted", err)
	}
	if err := s.Publish(ctx, item.ID, saved.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	fresh, err = cs.Get(ctx, item.ID)
	if err != nil || fresh.Excerpt != "Keep excerpt" || fresh.Locale != "ar" || !strings.Contains(fresh.Body, "gp-s-compare") {
		t.Fatalf("published content wrong %+v %v", fresh, err)
	}
	loaded, _ = s.Load(ctx, item.ID)
	sameDocument(t, doc, loaded.Document)
}

func TestNativeRESTStructurePermissionsAndStrictNestedJSON(t *testing.T) {
	f := newLayoutFixture(t)
	admin := f.user(t, "admin")
	viewer := f.user(t, "author")
	item := f.page(t, admin, "Native API")
	token := f.token(t, admin, []string{"content.edit", "content.publish"})
	path := "/api/v1/builder/" + item.ID
	schemaStatus, schemaBody := f.request(t, http.DefaultClient, "GET", "/api/v1/openapi.json", "", "", nil)
	if schemaStatus != 200 {
		t.Fatal("OpenAPI unavailable")
	}
	for _, marker := range []string{`"NativeOptions"`, `"ComparisonCell"`, `"builder_comparison_columns":6`, `"builder_native_facts":8`} {
		if !bytes.Contains(schemaBody, []byte(marker)) {
			t.Fatalf("OpenAPI missing native contract %s", marker)
		}
	}
	code, body := f.request(t, http.DefaultClient, "GET", path, token, "", nil)
	if code != 200 {
		t.Fatalf("get %d %s", code, body)
	}
	draft := decodeLayoutResponse(t, body)
	doc := nativeDocument("en")
	code, body = f.request(t, http.DefaultClient, "PUT", path+"/draft", token, "", saveRequest{Document: doc, BaseUpdatedAt: draft.UpdatedAt})
	if code != 200 {
		t.Fatalf("native save %d %s", code, body)
	}
	saved := decodeLayoutResponse(t, body)
	sameDocument(t, doc, saved.Document)
	code, body = f.request(t, http.DefaultClient, "GET", path, token, "", nil)
	if code != 200 {
		t.Fatal(code)
	}
	sameDocument(t, doc, decodeLayoutResponse(t, body).Document)
	viewToken := f.token(t, viewer, []string{"content.view"})
	code, _ = f.request(t, http.DefaultClient, "PUT", path+"/draft", viewToken, "", saveRequest{Document: doc, BaseUpdatedAt: saved.UpdatedAt})
	if code != 403 {
		t.Fatalf("viewer native save status=%d", code)
	}
	raw, _ := json.Marshal(saveRequest{Document: doc, BaseUpdatedAt: saved.UpdatedAt})
	raw = bytes.Replace(raw, []byte(`"background":"inherit"`), []byte(`"background":"inherit","unknown":true`), 1)
	code, _ = f.request(t, http.DefaultClient, "PUT", path+"/draft", token, "", string(raw))
	if code != 400 {
		t.Fatalf("unknown nested field status=%d", code)
	}
	code, _ = f.request(t, http.DefaultClient, "POST", path+"/publish", token, "", layoutPublishRequest{UpdatedAt: saved.UpdatedAt})
	if code != 200 {
		t.Fatalf("native publish status=%d", code)
	}
	fresh, err := f.cs.Get(auth.WithUser(context.Background(), admin), item.ID)
	if err != nil || !strings.Contains(fresh.Body, "gp-s-steps") {
		t.Fatal("native REST publication missing", err)
	}
}
