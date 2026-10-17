package ext

import (
	"os"
	"testing"
)

func testEnv() validateEnv {
	return validateEnv{
		perms:        map[string]bool{"content.view": true, "content.create": true},
		takenTypes:   map[string]bool{"page": true, "post": true},
		allowPrivate: false,
	}
}

func validManifest() Manifest {
	return Manifest{
		Slug:        "hello",
		Name:        map[string]string{"en": "Hello", "ar": "مرحبا"},
		Version:     "1.2.3",
		Permissions: []string{"content.view"},
		Hooks:       []HookSpec{{Name: "content.before_save", Kind: KindFilter, TimeoutMS: 500}, {Name: "content.published", Kind: KindEvent}},
		Endpoint:    Endpoint{URL: "https://hooks.example.com/gorun", Auth: "hmac"},
	}
}

func TestManifestValidation(t *testing.T) {
	vm := validManifest()
	if errs := vm.validate(testEnv()); len(errs) != 0 {
		t.Fatalf("valid manifest rejected: %v", errs)
	}
	cases := []struct {
		name  string
		edit  func(m *Manifest)
		field string
	}{
		{"missing slug", func(m *Manifest) { m.Slug = "" }, "slug"},
		{"bad slug", func(m *Manifest) { m.Slug = "Hello World" }, "slug"},
		{"missing version", func(m *Manifest) { m.Version = "" }, "version"},
		{"non-semver version", func(m *Manifest) { m.Version = "1.2" }, "version"},
		{"missing name", func(m *Manifest) { m.Name = nil }, "name"},
		{"unknown hook kind", func(m *Manifest) { m.Hooks[0].Kind = "webhook" }, "hooks.0.kind"},
		{"bad hook name", func(m *Manifest) { m.Hooks[1].Name = "Bad Name!" }, "hooks.1.name"},
		{"timeout too large", func(m *Manifest) { m.Hooks[0].TimeoutMS = 60000 }, "hooks.0.timeout_ms"},
		{"duplicate hook", func(m *Manifest) { m.Hooks[1] = m.Hooks[0] }, "hooks.1.name"},
		{"action and event on same name", func(m *Manifest) {
			m.Hooks = []HookSpec{{Name: "x.y", Kind: KindAction}, {Name: "x.y", Kind: KindEvent}}
		}, "hooks.1.name"},
		{"invalid permission", func(m *Manifest) { m.Permissions = []string{"content.view", "root.everything"} }, "permissions.1"},
		{"wildcard permission", func(m *Manifest) { m.Permissions = []string{"*"} }, "permissions.0"},
		{"hooks without endpoint", func(m *Manifest) { m.Endpoint = Endpoint{} }, "endpoint.url"},
		{"endpoint scheme", func(m *Manifest) { m.Endpoint.URL = "ftp://example.com/x" }, "endpoint.url"},
		{"endpoint private", func(m *Manifest) { m.Endpoint.URL = "http://127.0.0.1:9000/hook" }, "endpoint.url"},
		{"endpoint localhost", func(m *Manifest) { m.Endpoint.URL = "http://localhost:9000/hook" }, "endpoint.url"},
		{"endpoint auth", func(m *Manifest) { m.Endpoint.Auth = "basic" }, "endpoint.auth"},
		{"system type collision", func(m *Manifest) {
			m.ContentTypes = []ContentType{{Slug: "page", Name: map[string]string{"en": "Page"}}}
		}, "content_types.0.slug"},
		{"invalid type schema", func(m *Manifest) {
			m.ContentTypes = []ContentType{{Slug: "Bad Slug", Name: map[string]string{"en": "X"}}}
		}, "content_types.0.slug"},
		{"bad setting key", func(m *Manifest) { m.Settings = []SettingField{{Key: "Bad Key", Type: "text"}} }, "settings.0.key"},
		{"unknown setting type", func(m *Manifest) { m.Settings = []SettingField{{Key: "greeting", Type: "hologram"}} }, "settings.0.type"},
		{"admin nav url", func(m *Manifest) {
			m.AdminNav = []NavLink{{Label: map[string]string{"en": "X"}, URL: "javascript:alert(1)"}}
		}, "admin_nav.0.url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			m.Hooks = append([]HookSpec(nil), m.Hooks...)
			tc.edit(&m)
			errs := m.validate(testEnv())
			if _, ok := errs[tc.field]; !ok {
				t.Fatalf("want error on %q, got %v", tc.field, errs)
			}
		})
	}

	t.Run("private endpoint allowed with AllowPrivateNet", func(t *testing.T) {
		m := validManifest()
		m.Endpoint.URL = "http://127.0.0.1:9000/hook"
		env := testEnv()
		env.allowPrivate = true
		if errs := m.validate(env); len(errs) != 0 {
			t.Fatalf("errs: %v", errs)
		}
	})
	t.Run("own types may be redeclared on reinstall", func(t *testing.T) {
		m := validManifest()
		m.ContentTypes = []ContentType{{Slug: "greeting", Name: map[string]string{"en": "Greeting"}}}
		env := testEnv()
		env.takenTypes["greeting"] = true
		env.ownTypes = map[string]bool{"greeting": true}
		if errs := m.validate(env); len(errs) != 0 {
			t.Fatalf("errs: %v", errs)
		}
	})
	t.Run("timeout defaults", func(t *testing.T) {
		m := validManifest()
		m.Hooks[0].TimeoutMS = 0
		m.normalize()
		if m.Hooks[0].TimeoutMS != defaultTimeoutMS {
			t.Fatalf("timeout = %d", m.Hooks[0].TimeoutMS)
		}
	})
	t.Run("sample manifest parses and validates", func(t *testing.T) {
		raw, err := os.ReadFile("testdata/sample-extension/manifest.json")
		if err != nil {
			t.Fatal(err)
		}
		m, err := ParseManifest(raw)
		if err != nil {
			t.Fatal(err)
		}
		env := testEnv()
		env.allowPrivate = true
		if errs := m.validate(env); len(errs) != 0 {
			t.Fatalf("errs: %v", errs)
		}
	})
	t.Run("parse rejects unknown fields and garbage", func(t *testing.T) {
		if _, err := ParseManifest([]byte(`{"slug":"x","bogus":1}`)); err == nil {
			t.Fatal("unknown field accepted")
		}
		if _, err := ParseManifest([]byte(`not json`)); err == nil {
			t.Fatal("garbage accepted")
		}
		if _, err := ParseManifest([]byte(`[]`)); err == nil {
			t.Fatal("array accepted")
		}
	})
}
