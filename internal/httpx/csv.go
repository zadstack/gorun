package httpx

import "strings"

// CSVBOM starts CSV downloads so spreadsheet apps read them as UTF-8 (Arabic stays readable).
const CSVBOM = "\xef\xbb\xbf"

// CSVCell neutralises spreadsheet formula injection: a value starting with = + - @, a tab or a
// carriage return is prefixed with ' so spreadsheet apps show it as text instead of evaluating
// it (OWASP "CSV injection").
func CSVCell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}
