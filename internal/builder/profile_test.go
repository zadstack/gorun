package builder

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

func profileMedia(t *testing.T, h *starterHandler, ctx context.Context) (media.Service, *media.Media) {
	t.Helper()
	if err := db.Migrate(ctx, h.d.DB, "media", media.New().Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := media.New().Register(h.d); err != nil {
		t.Fatal(err)
	}
	ms, _ := kernel.Get[media.Service](h.d, kernel.ServiceMedia)
	var body bytes.Buffer
	if err := png.Encode(&body, image.NewRGBA(image.Rect(0, 0, 10, 10))); err != nil {
		t.Fatal(err)
	}
	item, err := ms.Upload(ctx, "profile.png", &body, media.UploadMeta{Alt: "Actual workshop image"})
	if err != nil {
		t.Fatal(err)
	}
	return ms, item
}
func fillProfile(f starterForm) starterForm {
	f.Description = "Clear plans for useful products."
	f.About = "We are an independent workshop focused on practical design."
	f.Services = "Research\nIdentity design\nProduct development"
	f.ContactEmail = "hello@example.test"
	f.ContactPhone = "+20 100 123 4567"
	f.ContactAddress = "Cairo, Egypt"
	return f
}
func TestStarterProfilePopulatesDraftsAndPublishesExplicitSettings(t *testing.T) {
	for _, kind := range []string{"company", "portfolio", "magazine"} {
		t.Run(kind, func(t *testing.T) {
			h, cs, ctx := starterTest(t)
			_, im := profileMedia(t, h, ctx)
			for key, value := range map[string]string{"site.logo": "old-logo", "contact.email": "old@example.test", "contact.phone": "Old phone", "contact.address": "Old address"} {
				if err := h.d.Settings.Set(ctx, key, value); err != nil {
					t.Fatal(err)
				}
			}
			issued, err := h.issue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			f := fillProfile(starterInput(issued.Token, kind))
			f.Logo = im.ID
			f.HeroImage = im.ID
			run, err := h.create(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			home, err := cs.Get(ctx, run.Pages["home"])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(home.Body, f.Description) || !strings.Contains(home.Body, im.URL) || home.Excerpt != f.Description || home.FeaturedMediaID != im.ID || home.SEO["description"] != f.Description {
				t.Fatalf("profile home missing: %+v", home)
			}
			about, err := cs.Get(ctx, run.Pages["about"])
			if err != nil || !strings.Contains(about.Body, f.About) {
				t.Fatalf("about missing: %v", err)
			}
			contact, err := cs.Get(ctx, run.Pages["contact"])
			if err != nil || !strings.Contains(contact.Body, f.ContactEmail) || !strings.Contains(contact.Body, f.ContactPhone) || !strings.Contains(contact.Body, f.ContactAddress) {
				t.Fatalf("contact missing: %v", err)
			}
			if kind != "magazine" {
				services, err := cs.Get(ctx, run.Pages["services"])
				if err != nil || !strings.Contains(services.Body, "Research") || !strings.Contains(services.Body, "Product development") {
					t.Fatalf("services missing: %v", err)
				}
			}
			if h.d.Settings.GetString(ctx, "site.logo", "") != "old-logo" || h.d.Settings.GetString(ctx, "contact.email", "") != "old@example.test" {
				t.Fatal("profile published before activation")
			}
			if err := h.activateReviewed(ctx, run.Token, reviewFor(t, h, cs, ctx, run)); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{"site.logo": im.ID, "contact.email": f.ContactEmail, "contact.phone": f.ContactPhone, "contact.address": f.ContactAddress} {
				if got := h.d.Settings.GetString(ctx, key, ""); got != want {
					t.Fatalf("%s=%q want%q", key, got, want)
				}
			}
		})
	}
}
func TestStarterRejectsInvalidProfileBeforeCapturingRun(t *testing.T) {
	for _, patch := range []func(*starterForm){func(f *starterForm) { f.ContactEmail = "Person <hello@example.test>" }, func(f *starterForm) { f.ContactPhone = "javascript:alert(1)" }, func(f *starterForm) { f.Services = strings.Repeat("x", 121) }, func(f *starterForm) { f.Services = "1\n2\n3\n4\n5\n6\n7" }, func(f *starterForm) { f.Description = strings.Repeat("x", 1601) }, func(f *starterForm) { f.About = strings.Repeat("😀", 6000) }, func(f *starterForm) { f.ContactPhone = "1     " }, func(f *starterForm) { f.Logo = "missing-media" }, func(f *starterForm) { f.HeroImage = "missing-media" }} {
		h, _, ctx := starterTest(t)
		issued, err := h.issue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		f := starterInput(issued.Token, "company")
		patch(&f)
		if _, err := h.create(ctx, f); err == nil {
			t.Fatal("invalid profile accepted")
		}
		run, err := h.run(ctx, issued.Token)
		if err != nil || run.Kind != "" || len(run.Pages) != 0 {
			t.Fatalf("invalid profile poisoned run: %+v %v", run, err)
		}
	}
	h, _, ctx := starterTest(t)
	ms, _ := profileMedia(t, h, ctx)
	file, err := ms.Upload(ctx, "notes.pdf", strings.NewReader("%PDF-1.4\nnot an image"), media.UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := starterInput(issued.Token, "company")
	f.HeroImage = file.ID
	if _, err := h.create(ctx, f); err == nil {
		t.Fatal("non-image hero accepted")
	}
}
func TestStarterResumePreservesCapturedProfileAndBlankLegacySettings(t *testing.T) {
	h, cs, ctx := starterTest(t)
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.DB.Exec(`CREATE TRIGGER profile_interrupt BEFORE INSERT ON content WHEN NEW.title='الخدمات' BEGIN SELECT RAISE(ABORT,'interrupt'); END`); err != nil {
		t.Fatal(err)
	}
	f := fillProfile(starterInput(issued.Token, "company"))
	if _, err := h.create(ctx, f); err == nil {
		t.Fatal("expected interruption")
	}
	if _, err := h.d.DB.Exec(`DROP TRIGGER profile_interrupt`); err != nil {
		t.Fatal(err)
	}
	changed := f
	changed.Description = "Replacement profile"
	changed.About = "Replacement about"
	changed.Logo = "invalid changed logo"
	run, err := h.create(ctx, changed)
	if err != nil {
		t.Fatal(err)
	}
	about, err := cs.Get(ctx, run.Pages["about"])
	if err != nil || !strings.Contains(about.Body, f.About) || strings.Contains(about.Body, changed.About) {
		t.Fatal("resume changed captured profile")
	}
	for key, value := range map[string]string{"site.logo": "legacy-logo", "contact.email": "legacy@example.test", "contact.phone": "legacy-phone", "contact.address": "legacy-address"} {
		if err := h.d.Settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	blank, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old, err := h.create(ctx, starterInput(blank.Token, "portfolio"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.activateReviewed(ctx, old.Token, reviewFor(t, h, cs, ctx, old)); err != nil {
		t.Fatal(err)
	}
	if h.d.Settings.GetString(ctx, "site.logo", "") != "legacy-logo" || h.d.Settings.GetString(ctx, "contact.email", "") != "legacy@example.test" || h.d.Settings.GetString(ctx, "contact.phone", "") != "legacy-phone" || h.d.Settings.GetString(ctx, "contact.address", "") != "legacy-address" {
		t.Fatal("blank profile cleared legacy settings")
	}
}

func TestProfileBoundaryTextAndSmallServiceListsRemainRenderable(t *testing.T) {
	for _, services := range []string{"Consulting", "Consulting\nDesign", strings.Repeat("A", 120) + "\nDesign\nDevelopment"} {
		h, cs, ctx := starterTest(t)
		issued, err := h.issue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		f := starterInput(issued.Token, "company")
		f.About = strings.Repeat("ع", 6000)
		f.Services = services
		run, err := h.create(ctx, f)
		if err != nil {
			t.Fatalf("valid boundary draft: %v", err)
		}
		about, err := cs.Get(ctx, run.Pages["about"])
		if err != nil || !strings.Contains(about.Body, f.About) {
			t.Fatal("valid Arabic boundary not rendered")
		}
		draft, err := h.svc.Load(ctx, run.Pages["services"])
		if err != nil {
			t.Fatal(err)
		}
		for _, section := range draft.Document.Sections {
			if section.Kind == "services" && len(section.Items) != len(strings.Split(services, "\n")) {
				t.Fatal("service count changed")
			}
		}
	}
}

func TestProfileActivationRollsBackExplicitSettingsAndAllPages(t *testing.T) {
	h, cs, ctx := starterTest(t)
	_, im := profileMedia(t, h, ctx)
	for key, value := range map[string]string{"site.logo": "previous-logo", "contact.email": "previous@example.test", "contact.phone": "previous-phone"} {
		if err := h.d.Settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := fillProfile(starterInput(issued.Token, "company"))
	f.Logo = im.ID
	run, err := h.create(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.DB.Exec(`CREATE TRIGGER profile_settings_failure BEFORE INSERT ON settings WHEN NEW.key='contact.phone' BEGIN SELECT RAISE(ABORT,'settings failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := h.activateReviewed(ctx, run.Token, reviewFor(t, h, cs, ctx, run)); err == nil {
		t.Fatal("expected transactional failure")
	}
	for _, id := range run.Pages {
		item, err := cs.Get(ctx, id)
		if err != nil || item.Status != "draft" {
			t.Fatal("partial profile publication")
		}
	}
	for key, want := range map[string]string{"site.logo": "previous-logo", "contact.email": "previous@example.test", "contact.phone": "previous-phone"} {
		if h.d.Settings.GetString(ctx, key, "") != want {
			t.Fatalf("partial setting %s", key)
		}
	}
	stored, err := h.run(ctx, run.Token)
	if err != nil || stored.Active {
		t.Fatal("failed run marked active")
	}
}
func TestProfileDeletedImagePreventsPublication(t *testing.T) {
	h, cs, ctx := starterTest(t)
	ms, im := profileMedia(t, h, ctx)
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := starterInput(issued.Token, "company")
	f.Logo = im.ID
	run, err := h.create(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Delete(ctx, im.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.activateReviewed(ctx, run.Token, reviewFor(t, h, cs, ctx, run)); !errors.Is(err, ErrProfile) {
		t.Fatalf("deleted image result: %v", err)
	}
	for _, id := range run.Pages {
		item, err := cs.Get(ctx, id)
		if err != nil || item.Status != "draft" {
			t.Fatal("deleted image published")
		}
	}
	if h.d.Settings.GetString(ctx, "site.logo", "") != "" {
		t.Fatal("deleted logo activated")
	}
}
