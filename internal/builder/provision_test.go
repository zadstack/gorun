package builder

import (
	"github.com/zadstack/gorun/internal/content"
	"testing"
)

func TestProvisionStudioPublishesOnceAndRefusesExistingContent(t *testing.T) {
	for _, kind := range []string{"company", "portfolio", "editorial"} {
		t.Run(kind, func(t *testing.T) {
			h, cs, ctx := starterTest(t)
			in := ProvisionInput{Title: "Customer Studio", Template: kind, Locale: "en", Email: "owner@example.test"}
			if err := ProvisionStudio(ctx, h.d, in); err != nil {
				t.Fatal(err)
			}
			before, err := cs.Query(ctx, content.Query{Type: "page", PerPage: 100})
			if err != nil || before.Total < 4 {
				t.Fatalf("incomplete: %+v %v", before, err)
			}
			for _, p := range before.Items {
				if p.Status != "published" || p.Template != "builder" {
					t.Fatalf("unpublished %+v", p)
				}
			}
			if err := ProvisionStudio(ctx, h.d, in); err != nil {
				t.Fatal(err)
			}
			after, _ := cs.Query(ctx, content.Query{Type: "page", PerPage: 100})
			if after.Total != before.Total {
				t.Fatal("duplicate pages")
			}
			in.Title = "Different request"
			if err := ProvisionStudio(ctx, h.d, in); err == nil {
				t.Fatal("different request replaced site")
			}
		})
	}
	h, cs, ctx := starterTest(t)
	if _, err := cs.Create(ctx, content.Input{Type: "page", Title: "Existing", Slug: "existing", Status: "published"}); err != nil {
		t.Fatal(err)
	}
	if err := ProvisionStudio(ctx, h.d, ProvisionInput{Title: "New", Template: "company", Locale: "en"}); err == nil {
		t.Fatal("existing site overwritten")
	}
}
