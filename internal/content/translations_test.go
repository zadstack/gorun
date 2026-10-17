package content

import (
	"context"
	"errors"
	"slices"
	"sort"
	"testing"
)

func localesOf(items []Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Locale)
	}
	sort.Strings(out)
	return out
}

func wantField(t *testing.T, err error, field string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Fields[field] == "" {
		t.Fatalf("want a validation error on %s, got %v", field, err)
	}
}

// CreateTranslation copies an item into another locale as a draft and links the two.
func TestCreateTranslationCopiesAsDraft(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	src := mustCreate(t, s, Input{Type: "post", Title: "مرحبا", Body: "<p>نص</p>", Status: "published", Locale: "ar"})
	tr, err := s.CreateTranslation(ctx, src.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Locale != "en" || tr.Status != "draft" || tr.Type != "post" || tr.Title != "مرحبا" || tr.Body != "<p>نص</p>" || tr.Slug != src.Slug {
		t.Fatalf("copy: %+v", tr)
	}
	if tr.TranslationGroup == "" {
		t.Fatal("the copy must be linked")
	}
	src, _ = s.Get(ctx, src.ID)
	if src.TranslationGroup != tr.TranslationGroup {
		t.Fatalf("groups: %q %q", src.TranslationGroup, tr.TranslationGroup)
	}
	list, err := s.Translations(ctx, src.ID)
	if err != nil || !slices.Equal(localesOf(list), []string{"ar", "en"}) {
		t.Fatalf("translations: %v %v", localesOf(list), err)
	}
	// the locale is taken now, the source locale is never a translation, unknown locales are refused
	for _, loc := range []string{"en", "ar", "fr", ""} {
		if _, err := s.CreateTranslation(ctx, src.ID, loc); err == nil {
			t.Fatalf("locale %q must be refused", loc)
		} else {
			wantField(t, err, "locale")
		}
	}
	// updating an item keeps its group
	tr, err = s.Update(ctx, tr.ID, Input{Title: "Hello", Locale: "en"})
	if err != nil || tr.TranslationGroup != src.TranslationGroup {
		t.Fatalf("update: %+v %v", tr, err)
	}
}

// LinkTranslation joins two existing items; the locales of a group stay unique.
func TestLinkAndUnlinkTranslations(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	ar := mustCreate(t, s, Input{Type: "post", Title: "عربي", Locale: "ar"})
	en := mustCreate(t, s, Input{Type: "post", Title: "English", Locale: "en"})
	en2 := mustCreate(t, s, Input{Type: "post", Title: "English 2", Locale: "en"})
	page := mustCreate(t, s, Input{Type: "page", Title: "Page", Locale: "en"})
	if err := s.LinkTranslation(ctx, ar.ID, en.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.Translations(ctx, en.ID); !slices.Equal(localesOf(list), []string{"ar", "en"}) {
		t.Fatalf("linked: %v", localesOf(list))
	}
	wantField(t, s.LinkTranslation(ctx, ar.ID, en2.ID), "locale")     // en is taken in the group
	wantField(t, s.LinkTranslation(ctx, ar.ID, page.ID), "type")      // another type
	wantField(t, s.LinkTranslation(ctx, ar.ID, ar.ID), "translation") // itself
	if err := s.LinkTranslation(ctx, ar.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	// a locale change that collides inside the group is refused
	_, err := s.Update(ctx, en.ID, Input{Title: "English", Locale: "ar"})
	wantField(t, err, "locale")

	if err := s.UnlinkTranslation(ctx, en.ID); err != nil {
		t.Fatal(err)
	}
	// a group of one is no group
	for _, id := range []string{ar.ID, en.ID} {
		it, _ := s.Get(ctx, id)
		if it.TranslationGroup != "" {
			t.Fatalf("%s still grouped: %q", id, it.TranslationGroup)
		}
		if list, _ := s.Translations(ctx, id); len(list) != 1 {
			t.Fatalf("alone: %v", localesOf(list))
		}
	}
}

// Trashing a translation leaves its group (so its locale can be translated again);
// deleting one for good does too.
func TestTrashLeavesTheGroup(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	src := mustCreate(t, s, Input{Type: "post", Title: "Source", Locale: "ar"})
	tr, err := s.CreateTranslation(ctx, src.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, tr.ID, false); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.Translations(ctx, src.ID); len(list) != 1 {
		t.Fatalf("after trash: %v", localesOf(list))
	}
	again, err := s.CreateTranslation(ctx, src.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, again.ID, true); err != nil {
		t.Fatal(err)
	}
	if it, _ := s.Get(ctx, src.ID); it.TranslationGroup != "" {
		t.Fatalf("a group of one after delete: %q", it.TranslationGroup)
	}
}

// A translated child page sits under the parent's translation in that locale (top level when
// the parent has none).
func TestCreateTranslationMapsTheParent(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	parent := mustCreate(t, s, Input{Type: "page", Title: "الشركة", Locale: "ar"})
	child := mustCreate(t, s, Input{Type: "page", Title: "الفريق", Locale: "ar", ParentID: parent.ID})
	orphan, err := s.CreateTranslation(ctx, child.ID, "en")
	if err != nil || orphan.ParentID != "" {
		t.Fatalf("no parent translation: %+v %v", orphan, err)
	}
	if err := s.Delete(ctx, orphan.ID, true); err != nil {
		t.Fatal(err)
	}
	enParent, err := s.CreateTranslation(ctx, parent.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	enChild, err := s.CreateTranslation(ctx, child.ID, "en")
	if err != nil || enChild.ParentID != enParent.ID {
		t.Fatalf("parent: %+v %v", enChild, err)
	}
}

// A top-level page may not take a supported locale code as its slug: /en/ is the English site.
func TestTopLevelPageSlugAvoidsLocaleCodes(t *testing.T) {
	s := newTestService(t)
	p := mustCreate(t, s, Input{Type: "page", Title: "x", Slug: "en"})
	if p.Slug == "en" {
		t.Fatal("a top-level page took the slug en")
	}
	parent := mustCreate(t, s, Input{Type: "page", Title: "Parent"})
	if c := mustCreate(t, s, Input{Type: "page", Title: "x", Slug: "en", ParentID: parent.ID}); c.Slug != "en" {
		t.Fatalf("a child page may: %q", c.Slug)
	}
	if c := mustCreate(t, s, Input{Type: "post", Title: "x", Slug: "ar"}); c.Slug != "ar" {
		t.Fatalf("a post may (/post/ar): %q", c.Slug)
	}
}
