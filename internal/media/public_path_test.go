package media

import "testing"

func TestPublicPathUsesOnlyNativeStorageKeys(t *testing.T) {
	for _, key := range []string{"2026/10/logo.png", "remote/0123456789abcdef0123456789abcdef/2026/10/logo.svg", "sites/01m4qaqm4rp3t0ysghn6n7hjws/uploads/2026/10/logo.webp"} {
		if got := PublicPath(key); got != URLPrefix+key {
			t.Fatalf("native path rejected: %q", got)
		}
	}
	for _, key := range []string{"", "../logo.png", "/2026/10/logo.png", "https://outside.invalid/logo.png", "2026/10/logo.png?x=1", "2026/10/%2e%2e.svg", "2026/10/logo.png#x"} {
		if PublicPath(key) != "" {
			t.Fatalf("unsafe path accepted: %q", key)
		}
	}
}
