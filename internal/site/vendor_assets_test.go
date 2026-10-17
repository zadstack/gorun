package site_test

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/web"
)

func TestAdminDependenciesAreEmbedded(t *testing.T) {
	e := boot(t)
	e.setting(site.SettingMaintenance, true)
	manifest, err := web.FS.ReadFile("admin/static/cdn-provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var provenance struct {
		Assets []struct {
			Package     string `json:"package"`
			LocalPath   string `json:"local_path"`
			LicensePath string `json:"license_local_path"`
			Integrity   string `json:"integrity"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(manifest, &provenance); err != nil {
		t.Fatal(err)
	}
	if len(provenance.Assets) == 0 {
		t.Fatal("missing dependency provenance")
	}
	for _, a := range provenance.Assets {
		t.Run(a.Package, func(t *testing.T) {
			if !strings.HasPrefix(a.LocalPath, "web/admin/static/vendor/") {
				t.Fatalf("dependency not embedded: %q", a.LocalPath)
			}
			path := "/static/admin/" + strings.TrimPrefix(a.LocalPath, "web/admin/static/")
			resp, body := e.get(path)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s: %d", path, resp.StatusCode)
			}
			digest := sha512.Sum384([]byte(body))
			if got := "sha384-" + base64.StdEncoding.EncodeToString(digest[:]); got != a.Integrity {
				t.Fatalf("HTTP bytes do not match SRI: %s", got)
			}
			license, err := web.FS.ReadFile(strings.TrimPrefix(a.LicensePath, "web/"))
			if err != nil || len(license) == 0 {
				t.Fatalf("missing upstream license: %v", err)
			}
		})
	}
	remoteScript := regexp.MustCompile(`<script\b[^>]*\bsrc="https?://`)
	if err := fs.WalkDir(web.FS, "admin/templates", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		b, err := web.FS.ReadFile(path)
		if err != nil {
			return err
		}
		if remoteScript.Match(b) {
			t.Errorf("admin template loads remote script: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
