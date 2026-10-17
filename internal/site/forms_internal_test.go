package site

import "testing"

func TestSafeBack(t *testing.T) {
	cases := []struct{ field, referer, want string }{
		{"/contact", "", "/contact"},
		{"/a/b?x=1", "", "/a/b?x=1"},
		{"/\t/evil.example", "", "/"},
		{"/\x7f/x", "", "/"},
		{"//evil.example", "", "/"},
		{"/\\evil.example", "", "/"},
		{"https://evil.example/x", "", "/"},
		{"javascript:alert(1)", "", "/"},
		{"/ok\r\nSet-Cookie: x=1", "", "/"},
		{"", "http://example.test/from/here?q=1", "/from/here?q=1"},
		{"", "http://example.test//evil.example", "/"},
		{"", "http://example.test/\t/evil", "/"},
		{"", "", "/"},
	}
	for _, c := range cases {
		if got := safeBack(c.field, c.referer); got != c.want {
			t.Errorf("safeBack(%q, %q) = %q, want %q", c.field, c.referer, got, c.want)
		}
	}
}

func TestFormSourcePathPrivacy(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{"/contact?email=private#token", "/contact"}, {"/ar/contact", "/ar/contact"},
		{"https://evil.test/form", ""}, {"//evil.test/form", ""}, {"/admin/reports/secret", ""},
		{"/ar/api/private", ""}, {"/forms/contact", ""}, {"/%2fexample.test", ""}, {"/%5cevil", ""},
		{"/contact%0aattack", ""}, {"javascript:alert(1)", ""},
	} {
		if got := formSourcePath(c.raw); got != c.want {
			t.Errorf("%q => %q want %q", c.raw, got, c.want)
		}
	}
	sources := []formSource{{Path: "/contact", Title: "Contact"}}
	if got := resolveFormSource("/unpublished?token=secret", sources); got.Path != "" {
		t.Fatal("linked an unrecognized source")
	}
	if got := resolveFormSource("/contact?token=secret", sources); got.Path != "/contact" {
		t.Fatal("lost sanitized source")
	}
}
