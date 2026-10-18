package i18n

import "testing"

func TestNormalizeArabic(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"alef hamza above", "أحمد", "احمد"},
		{"alef hamza below", "إحمد", "احمد"},
		{"alef madda", "آمنة", "امنه"},
		{"alef wasla", "ٱلله", "الله"},
		{"plain alef unchanged", "احمد", "احمد"},
		{"taa marbuta", "مدرسة", "مدرسه"},
		{"haa unchanged", "مدرسه", "مدرسه"},
		{"alef maqsura", "يحيى", "يحيي"},
		{"waw hamza", "مؤمن", "مومن"},
		{"yaa hamza", "رئيس", "رييس"},
		{"standalone hamza kept", "سماء", "سماء"},
		{"harakat and shadda", "مُحَمَّد", "محمد"},
		{"tanween and sukun", "كتابًا مُسْلِمٌ", "كتابا مسلم"},
		{"superscript alef", "هٰذا", "هذا"},
		{"quranic marks", "الرَّحِيمِ \u06dd ذَٰلِكَ ۖ", "الرحيم \u06dd ذلك "},
		{"tatweel", "تطويـــل", "تطويل"},
		{"decomposed hamza above", "أحمد", "احمد"},
		{"decomposed yaa hamza", "رئيس", "رييس"},
		{"arabic-indic digits", "٠١٢٣٤٥٦٧٨٩", "0123456789"},
		{"persian digits", "۰۱۲۳۴۵۶۷۸۹", "0123456789"},
		{"mixed", "GoRun مدرسة أحمد 2026", "GoRun مدرسه احمد 2026"},
		{"latin unchanged", "Café Ünïcode ABC", "Café Ünïcode ABC"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeArabic(c.in)
			if got != c.want {
				t.Fatalf("NormalizeArabic(%q) = %q, want %q", c.in, got, c.want)
			}
			if again := NormalizeArabic(got); again != got {
				t.Fatalf("not idempotent: %q -> %q", got, again)
			}
		})
	}
}

// Text with nothing to fold comes back as the same string (no copy).
func TestNormalizeArabicNoop(t *testing.T) {
	for _, s := range []string{"hello world", "احمد ومدرسه", "123"} {
		if got := NormalizeArabic(s); got != s {
			t.Fatalf("%q -> %q", s, got)
		}
	}
}

func BenchmarkNormalizeArabic(b *testing.B) {
	s := "مُحَمَّد أحمد في المدرسة الكبيرة يقرأ كتاباً جديداً عن GoRun"
	for b.Loop() {
		NormalizeArabic(s)
	}
}
