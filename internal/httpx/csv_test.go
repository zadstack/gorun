package httpx

import "testing"

func TestCSVCell(t *testing.T) {
	for in, want := range map[string]string{
		"":          "",
		"Plain":     "Plain",
		"=1+1":      "'=1+1",
		"+44 20":    "'+44 20",
		"-2":        "'-2",
		"@SUM(A1)":  "'@SUM(A1)",
		"\tcmd":     "'\tcmd",
		"\rcmd":     "'\rcmd",
		"a=b":       "a=b",
		"عربي = نص": "عربي = نص",
	} {
		if got := CSVCell(in); got != want {
			t.Errorf("CSVCell(%q) = %q, want %q", in, got, want)
		}
	}
}
