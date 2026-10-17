package builder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
)

func TestValidateRejectsStructuralAndLengthViolations(t *testing.T) {
	ok := func() Section { s := DefaultSection("features", "en"); s.ID = "ok"; return s }
	long := strings.Repeat("x", 12001)
	many := make([]Section, maxSections+1)
	for i := range many {
		many[i] = ok()
		many[i].ID = fmt.Sprintf("s%d", i)
	}
	cases := map[string]Document{
		"version":       {Version: 2},
		"too many":      {Version: 1, Sections: many},
		"bad id":        {Version: 1, Sections: []Section{func() Section { s := ok(); s.ID = "bad id"; return s }()}},
		"repeated id":   {Version: 1, Sections: []Section{ok(), ok()}},
		"unknown kind":  {Version: 1, Sections: []Section{func() Section { s := ok(); s.Kind = "carousel"; return s }()}},
		"section text":  {Version: 1, Sections: []Section{func() Section { s := ok(); s.Text = long; return s }()}},
		"too many item": {Version: 1, Sections: []Section{func() Section { s := ok(); s.Items = make([]Entry, maxRepeatedEntries+1); return s }()}},
		"item text":     {Version: 1, Sections: []Section{func() Section { s := ok(); s.Items = []Entry{{Price: long}}; return s }()}},
		"item link":     {Version: 1, Sections: []Section{func() Section { s := ok(); s.Items = []Entry{{Link: "javascript:alert(1)"}}; return s }()}},
		"item image":    {Version: 1, Sections: []Section{func() Section { s := ok(); s.Items = []Entry{{Image: "#fragment"}}; return s }()}},
		"hostless http": {Version: 1, Sections: []Section{func() Section { s := ok(); s.Link = "https:/path"; return s }()}},
		"relative path": {Version: 1, Sections: []Section{func() Section { s := ok(); s.Link = "relative/page"; return s }()}},
	}
	for name, doc := range cases {
		if err := Validate(doc); err == nil {
			t.Errorf("%s accepted", name)
		}
		if _, err := Render(doc); err == nil {
			t.Errorf("%s rendered", name)
		}
		if err := ValidatePublication(doc); err == nil {
			t.Errorf("%s publishable", name)
		}
	}
	if err := Validate(Document{Version: 1, Sections: []Section{ok()}}); err != nil {
		t.Fatalf("valid section rejected: %v", err)
	}
}

func TestProfileMessageCoversEveryFieldInBothLanguages(t *testing.T) {
	en, ar := i18n.WithLocale(context.Background(), "en"), i18n.WithLocale(context.Background(), "ar")
	for _, field := range []string{"description", "about", "services", "contact_email", "contact_phone", "contact_address", "logo"} {
		err := profileFieldError{field}
		if !errors.Is(err, ErrProfile) || !strings.Contains(err.Error(), field) {
			t.Fatalf("%s error does not wrap ErrProfile: %v", field, err)
		}
		english, arabic := profileMessage(en, err), profileMessage(ar, err)
		if english == "" || arabic == "" || english == arabic {
			t.Fatalf("%s messages en=%q ar=%q", field, english, arabic)
		}
		if strings.ContainsFunc(english, func(r rune) bool { return r >= 0x0600 && r <= 0x06FF }) {
			t.Fatalf("%s english message contains Arabic", field)
		}
	}
	if profileMessage(en, errors.New("unrelated")) != "" {
		t.Fatal("non-profile error produced a profile message")
	}
}

func TestProvisionStudioRejectsBadTemplateRoleAndTitle(t *testing.T) {
	h, cs, ctx := starterTest(t)
	if err := ProvisionStudio(ctx, h.d, ProvisionInput{Title: "Site", Template: "blog", Locale: "en"}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported template: %v", err)
	}
	if err := ProvisionStudio(editorCtx(), h.d, ProvisionInput{Title: "Site", Template: "company", Locale: "en"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin provision: %v", err)
	}
	if err := ProvisionStudio(ctx, h.d, ProvisionInput{Title: " ", Template: "editorial", Locale: "en"}); err == nil {
		t.Fatal("blank title accepted")
	}
	page, err := cs.Query(ctx, content.Query{PerPage: 1, IncludeTrash: true})
	if err != nil || page.Total != 0 {
		t.Fatalf("rejected provisioning created content: %d %v", page.Total, err)
	}
}
