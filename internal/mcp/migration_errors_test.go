package mcp

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/wpmigrate"
)

// lockedBuffer is a bytes.Buffer safe for the server's goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// An unknown migration ID is the agent's mistake: not_found, nothing in the
// error log. A real failure stays internal and is logged exactly once.
func TestMigrationToolsUnknownIDIsNotFound(t *testing.T) {
	logs := &lockedBuffer{}
	kernel.LogOutput = logs
	t.Cleanup(func() { kernel.LogOutput = io.Discard })
	e := newEnv(t, &fakeModule{wpmigrate: true})
	a := e.agent(scopeMigrate)

	for _, tool := range []string{"preview_migration", "get_migration_status"} {
		errObj := a.fails(tool, map[string]any{"migration_id": "missing"})
		if str(errObj, "code") != "not_found" {
			t.Fatalf("%s with an unknown ID: %v, want not_found", tool, errObj)
		}
	}
	if strings.Contains(logs.String(), "mcp: tool failed") {
		t.Fatalf("not_found was logged as a tool failure:\n%s", logs.String())
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'mcp.call' AND meta_json LIKE '%"error_code":"not_found"%'`); n != 2 {
		t.Fatalf("audited not_found calls: %d, want 2", n)
	}

	errObj := a.fails("get_migration_status", map[string]any{"migration_id": "broken"})
	if str(errObj, "code") != "internal" || strings.Contains(str(errObj, "message"), "disk") {
		t.Fatalf("unclassified error: %v", errObj)
	}
	if n := strings.Count(logs.String(), "mcp: tool failed"); n != 1 {
		t.Fatalf("unclassified error logged %d times, want once:\n%s", n, logs.String())
	}
}

func TestToToolErrorMapsWPMigrateNotFound(t *testing.T) {
	te, known := toToolError(wpmigrate.ErrNotFound)
	if !known || te.Code != codeNotFound {
		t.Fatalf("wpmigrate.ErrNotFound: %+v known=%v", te, known)
	}
}
