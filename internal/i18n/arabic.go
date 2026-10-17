package i18n

import "strings"

// NormalizeArabic folds Arabic spelling variants so that search matches what a reader
// considers the same word. It is applied to both the indexed text and the query, and is
// idempotent. Text outside the Arabic block is returned unchanged (Latin case folding is
// left to the search engine).
//
// Rules:
//   - alef with hamza or madda, and alef wasla (أ إ آ ٱ) → bare alef ا
//   - alef maqsura ى → ي, and taa marbuta ة → ه (the two common spelling swaps)
//   - waw with hamza ؤ → و, and yaa with hamza ئ → ي: writers often drop the seat's hamza,
//     so the bare letter is what a query is most likely to contain. The standalone hamza ء
//     is a letter of its own and is kept.
//   - tatweel ـ, the harakat and other combining marks U+064B–U+065F (tanween, shadda,
//     sukun, and the combining hamza above/below, so decomposed forms fold like the
//     precomposed ones), the superscript alef U+0670 and the combining Quranic annotation
//     marks U+06D6–U+06DC, U+06DF–U+06E4, U+06E7–U+06E8, U+06EA–U+06ED are removed. The
//     visible Quranic symbols (۝ ۞ ۩) are kept.
//   - Arabic-Indic ٠-٩ and Extended (Persian) ۰-۹ digits → ASCII 0-9.
func NormalizeArabic(s string) string {
	i := strings.IndexFunc(s, needsFold)
	if i < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:i])
	for _, r := range s[i:] {
		if f, ok := foldArabic(r); ok {
			b.WriteRune(f)
		} else if !dropArabic(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// needsFold reports whether r is changed or removed by NormalizeArabic.
func needsFold(r rune) bool {
	_, ok := foldArabic(r)
	return ok || dropArabic(r)
}

// foldArabic maps a letter or digit to its normal form.
func foldArabic(r rune) (rune, bool) {
	switch {
	case r == 'أ', r == 'إ', r == 'آ', r == 'ٱ':
		return 'ا', true
	case r == 'ى', r == 'ئ':
		return 'ي', true
	case r == 'ة':
		return 'ه', true
	case r == 'ؤ':
		return 'و', true
	case r >= '٠' && r <= '٩':
		return '0' + (r - '٠'), true
	case r >= '۰' && r <= '۹':
		return '0' + (r - '۰'), true
	}
	return 0, false
}

// dropArabic reports whether r is a tatweel or a combining mark that NormalizeArabic removes.
func dropArabic(r rune) bool {
	switch {
	case r == 'ـ', r == 'ٰ':
		return true
	case r >= 'ً' && r <= 'ٟ':
		return true
	case r >= 'ۖ' && r <= 'ۜ', r >= '۟' && r <= 'ۤ',
		r >= 'ۧ' && r <= 'ۨ', r >= '۪' && r <= 'ۭ':
		return true
	}
	return false
}
