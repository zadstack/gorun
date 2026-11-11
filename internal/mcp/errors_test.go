package mcp

import (
	"os"
	"strings"
	"testing"
)

// Every error code is documented once, in the "Error codes" table of docs/MCP.md.
func TestErrorCodesAreDocumented(t *testing.T) {
	b, err := os.ReadFile("../../docs/MCP.md")
	if err != nil {
		t.Fatal(err)
	}
	_, table, ok := strings.Cut(string(b), "## Error codes")
	if !ok {
		t.Fatal("docs/MCP.md has no Error codes section")
	}
	for _, code := range errorCodes {
		if !strings.Contains(table, "| `"+code+"` |") {
			t.Errorf("error code %s is not in the docs/MCP.md Error codes table", code)
		}
	}
}

// A URL that cannot be downloaded is fetch_failed, not internal.
func TestUploadMediaFetchFailed(t *testing.T) {
	a := newEnv(t, nil).agent("media.upload")
	errObj := a.fails("upload_media", map[string]any{"url": "http://127.0.0.1:1/missing.png"})
	if code := str(errObj, "code"); code != codeFetchFailed {
		t.Fatalf("unreachable URL: code %q, want %s (%v)", code, codeFetchFailed, errObj)
	}
}
