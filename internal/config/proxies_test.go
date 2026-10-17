package config

import "testing"

func TestTrustedProxyConfigurationValidation(t *testing.T) {
	for _, v := range []string{"garbage", "0.0.0.0/0", "::/0", "127.0.0.1/999", "fe80::1%en0", "127.0.0.1,"} {
		t.Setenv("GORUN_TRUSTED_PROXIES", v)
		if _, e := Load(nil); e == nil {
			t.Fatalf("accepted invalid trust %q", v)
		}
	}
	t.Setenv("GORUN_TRUSTED_PROXIES", "127.0.0.1, 10.1.2.3/32, 2001:db8::/32")
	c, e := Load(nil)
	if e != nil {
		t.Fatal(e)
	}
	p, e := c.TrustedProxyNetworks()
	if e != nil || len(p) != 3 {
		t.Fatal(p, e)
	}
	t.Setenv("GORUN_TRUSTED_PROXIES", "")
	c, e = Load(nil)
	if e != nil || len(c.TrustedProxies) != 0 {
		t.Fatal("empty must trust none", e)
	}
}
