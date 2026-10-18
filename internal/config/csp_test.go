package config

import (
	"reflect"
	"testing"
)

func TestPublicScriptSources(t *testing.T) {
	for _, v := range []string{"https:", "*", "http://cdn.example.com", "'unsafe-inline'", "data:", "https://*",
		"https://u:p@cdn.example.com", "https://cdn.example.com/x?y=1", "javascript:alert(1)"} {
		t.Setenv("GORUN_CSP_SCRIPT_SRC", v)
		if _, e := Load(nil); e == nil {
			t.Fatalf("accepted script source %q", v)
		}
	}
	t.Setenv("GORUN_CSP_SCRIPT_SRC", "https://cdn.jsdelivr.net, https://unpkg.com/alpinejs@3.14.1/ 'unsafe-eval'")
	c, e := Load(nil)
	if e != nil {
		t.Fatal(e)
	}
	got, e := c.PublicScriptSources()
	want := []string{"https://cdn.jsdelivr.net", "https://unpkg.com/alpinejs@3.14.1/", "'unsafe-eval'"}
	if e != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%q %v", got, e)
	}
	t.Setenv("GORUN_CSP_SCRIPT_SRC", "")
	if c, e = Load(nil); e != nil || len(c.CSPScriptSrc) != 0 {
		t.Fatal("empty adds nothing", e)
	}
}

func TestMCPSameOwnerApprovalConfig(t *testing.T) {
	if c, e := Load(nil); e != nil || c.MCPSameOwnerApproval {
		t.Fatal("same-owner approval must be off by default", e)
	}
	t.Setenv("GORUN_MCP_SAME_OWNER_APPROVAL", "maybe")
	if _, e := Load(nil); e == nil {
		t.Fatal("accepted a non-boolean value")
	}
	t.Setenv("GORUN_MCP_SAME_OWNER_APPROVAL", "true")
	if c, e := Load(nil); e != nil || !c.MCPSameOwnerApproval {
		t.Fatal("explicit opt-in ignored", e)
	}
}
