package admin

import (
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
)

// TestHelper exposes test-only encoding helpers to external test packages.
// It is compiled into the package but only useful from tests.
type TestHelper struct{ App *kernel.App }

// EncryptSecrets encrypts a connection secret bundle with the app's secret key.
func (h *TestHelper) EncryptSecrets(t *testing.T, token, access, secret string) string {
	t.Helper()
	hd := &handlers{d: h.App.Deps}
	enc, err := hd.encryptConnectionSecrets(connectionSecrets{Token: token, AccessKey: access, SecretKey: secret})
	if err != nil {
		t.Fatal(err)
	}
	return enc
}
