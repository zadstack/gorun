package api

import (
	"html/template"
	"net/http"

	"github.com/zadstack/gorun/internal/httpx"
)

var docsTemplate = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="{{.Locale}}" dir="{{.Dir}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>GoRun API</title>
</head>
<body>
<script id="api-reference" data-url="/api/v1/openapi.json"></script>
<script src="{{.Script}}" integrity="{{.Integrity}}" crossorigin="anonymous"></script>
</body>
</html>
`))

// docsPage serves the Scalar API reference for /api/v1/openapi.json; the
// page direction follows the site.locale setting (default ar).
func (s *server) docsPage(w http.ResponseWriter, r *http.Request) {
	locale := s.deps.Settings.GetString(r.Context(), "site.locale", "ar")
	dir := "rtl"
	if s.deps.I18n != nil {
		dir = s.deps.I18n.Dir(locale)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := docsTemplate.Execute(w, map[string]string{"Locale": locale, "Dir": dir, "Script": httpx.ScalarScript, "Integrity": httpx.ScalarIntegrity}); err != nil {
		log().Error("api: docs page", "err", err)
	}
}
