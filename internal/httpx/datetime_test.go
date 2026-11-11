package httpx

import "testing"

func TestFormatDateTimeOneOutput(t *testing.T) {
	cases := []struct{ in, class, want string }{
		{"2026-11-10T18:04:58.070Z", "", `<time datetime="2026-11-10T18:04:58Z" dir="ltr" title="2026-11-10 18:04:58 UTC">2026-11-10 18:04</time>`},
		{"2026-11-10T20:04:58+02:00", "text-muted", `<time class="text-muted" datetime="2026-11-10T18:04:58Z" dir="ltr" title="2026-11-10 18:04:58 UTC">2026-11-10 18:04</time>`},
		{"2026-11-10 18:04:58", "", `<time datetime="2026-11-10T18:04:58Z" dir="ltr" title="2026-11-10 18:04:58 UTC">2026-11-10 18:04</time>`},
		{"", "", "—"},
		{"<not a date>", "", "&lt;not a date&gt;"},
	}
	for _, c := range cases {
		if got := string(formatDateTime(c.in, c.class)); got != c.want {
			t.Errorf("formatDateTime(%q, %q) = %s, want %s", c.in, c.class, got, c.want)
		}
	}
	if got := string(formatDateTime("2026-01-02T03:04:05Z", `x" onmouseover="y`)); got != `<time class="x&#34; onmouseover=&#34;y" datetime="2026-01-02T03:04:05Z" dir="ltr" title="2026-01-02 03:04:05 UTC">2026-01-02 03:04</time>` {
		t.Errorf("class not escaped: %s", got)
	}
}

func TestFormatDateMatchesDateTimeText(t *testing.T) {
	for in, want := range map[string]string{"2026-11-10T18:04:58.070Z": "2026-11-10 18:04", "": "—", "soon": "soon"} {
		if got := formatDate(in); got != want {
			t.Errorf("formatDate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInitials(t *testing.T) {
	for in, want := range map[string]string{"Campaign assistant": "CA", "amira": "A", "مساعد الحملة": "ما", "  ": "?", "R&D team 2": "RD"} {
		if got := initials(in); got != want {
			t.Errorf("initials(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDataAttr(t *testing.T) {
	if got := dataAttr("key-create-open"); got != "data-key-create-open" {
		t.Fatalf("dataAttr = %q", got)
	}
	for _, bad := range []string{"", "Key", "x onclick=y", "-x", `x"y`} {
		if got := dataAttr(bad); got != "" {
			t.Fatalf("dataAttr(%q) = %q, want none", bad, got)
		}
	}
}

func TestDict(t *testing.T) {
	m, err := dict("Title", "x", "N", 2)
	if err != nil || m["Title"] != "x" || m["N"] != 2 {
		t.Fatalf("dict = %v, %v", m, err)
	}
	if _, err := dict("odd"); err == nil {
		t.Fatal("odd arguments accepted")
	}
	if _, err := dict(1, 2); err == nil {
		t.Fatal("non-string key accepted")
	}
}
