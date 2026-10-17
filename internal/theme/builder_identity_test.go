package theme

import (
	"github.com/zadstack/gorun/internal/kernel"
	"testing"
)

func TestBuilderIdentityUsesValidatedGlobalTokens(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": `<style>{{builderStyle}}</style>`})
	e := newEnv(t, root)
	e.setting("builder.identity", map[string]string{"accent": "#167252", "background": "#101010", "text": "#eeeeee", "font": "mono", "width": "wide"})
	rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, rec.Body.String(), "--green:#167252", "--bg:#101010", "--ink:#eeeeee", "--gp-accent:#167252", "--gp-width:1280px", "ui-monospace")
	e.setting("builder.identity", map[string]string{"accent": "red;}</style><script>alert(1)</script>", "background": "url(https://evil.test)", "text": "#eeeeee", "font": "evil;font", "width": "999999px"})
	rec, err = e.render(kernel.KindRequest{Kind: KindHome}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, rec.Body.String(), "evil", "<script>", "999999px", "url(")
}
