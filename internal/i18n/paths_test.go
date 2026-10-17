package i18n

import "testing"

func TestLocalePaths(t *testing.T) {
	for _, c := range []struct{ loc, site, p, want string }{
		{"en", "ar", "/post/x", "/en/post/x"}, {"en", "ar", "/", "/en/"}, {"ar", "ar", "/post/x", "/post/x"},
		{"", "ar", "/post/x", "/post/x"}, {"fr", "ar", "/x", "/x"}, {"ar", "en", "/", "/ar/"},
	} {
		if got := LocalizePath(c.loc, c.site, c.p); got != c.want {
			t.Errorf("LocalizePath(%q, %q, %q) = %q, want %q", c.loc, c.site, c.p, got, c.want)
		}
	}
	for _, c := range []struct{ p, loc, rest string }{
		{"/en/post/x", "en", "/post/x"}, {"/en", "en", "/"}, {"/en/", "en", "/"}, {"/english", "", "/english"}, {"/", "", "/"},
	} {
		if loc, rest := SplitLocalePath(c.p); loc != c.loc || rest != c.rest {
			t.Errorf("SplitLocalePath(%q) = %q %q", c.p, loc, rest)
		}
	}
}
