package theme

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

// externalScript matches a <script> that loads from another
// origin; the public CSP allows neither CDN scripts nor eval by default.
var externalScript = regexp.MustCompile(`(?i)<script[^>]+src="(https?:)?//`)

// The bundled themes must keep working under the default public CSP, which
// allows only same-origin and nonced scripts.
func TestBundledThemesLoadNoThirdPartyScripts(t *testing.T) {
	err := fs.WalkDir(os.DirFS("../../themes"), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		b, err := fs.ReadFile(os.DirFS("../../themes"), path)
		if err != nil {
			return err
		}
		if m := externalScript.Find(b); m != nil {
			t.Errorf("%s loads a third-party script (%s); vendor it under /static or self-host it", path, m)
		}
		if strings.Contains(string(b), "x-data") {
			t.Errorf("%s uses Alpine, which needs 'unsafe-eval' the public CSP no longer grants", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
