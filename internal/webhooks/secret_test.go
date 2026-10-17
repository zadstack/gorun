package webhooks

import (
	"errors"
	"strings"
	"testing"
)

func TestNewSecretPanicsWhenRandomnessFails(t *testing.T) {
	if s := newSecret(); !strings.HasPrefix(s, "whsec_") || len(s) != len("whsec_")+48 {
		t.Fatalf("secret: %q", s)
	}
	orig := randRead
	t.Cleanup(func() { randRead = orig })
	randRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	defer func() {
		if recover() == nil {
			t.Fatal("a crypto/rand failure must panic, never yield a predictable secret")
		}
	}()
	newSecret()
}
