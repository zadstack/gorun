package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

// TestMain points every boot at the repository's themes: the binary reads
// ./themes, and tests run in pkg/cli.
func TestMain(m *testing.M) {
	os.Setenv(theme.EnvThemesDir, "../../themes")
	os.Exit(m.Run())
}

func TestNotInstalledExitsTwo(t *testing.T) {
	var ee *exitError
	if err := notInstalled("reindex"); !errors.As(err, &ee) || ee.code != 2 || ee.Error() != "reindex: not available in this build (module not installed)" {
		t.Fatalf("notInstalled: %v", err)
	}
}

func TestExistingCommandsStillDispatch(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Fatalf("version: %v", err)
	}
	if err := run([]string{"help"}); err != nil {
		t.Fatalf("help: %v", err)
	}
	if err := run([]string{"user", "create"}); err == nil || !strings.Contains(err.Error(), "--email and --password") {
		t.Fatalf("user create without flags: %v", err)
	}
	if err := run([]string{"user"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("user: %v", err)
	}
	if err := run([]string{"healthcheck", "--addr", "127.0.0.1:1"}); err == nil {
		t.Fatal("healthcheck against a closed port must fail")
	}
	var ee *exitError
	if err := run([]string{"nope"}); err == nil || errors.As(err, &ee) {
		t.Fatalf("unknown command must be a plain error (exit 1): %v", err)
	}
	for _, name := range []string{"reindex", "mcp", "import-wxr", "backup"} {
		if !strings.Contains(usage, "  "+name+" ") {
			t.Errorf("usage does not list %s", name)
		}
	}
}

func TestReindexBootsAndRebuilds(t *testing.T) {
	kernel.LogOutput = io.Discard
	dir := t.TempDir()
	ctx := context.Background()
	cfg, err := config.Load([]string{"--data", dir})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.Boot(ctx, cfg, Modules())
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	for _, title := range []string{"one", "two", "أحمد"} {
		if _, err := cs.Create(ctx, content.Input{Type: "post", Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	app.Close()

	for _, args := range [][]string{{"--data", dir}, {"--no-jobs", "--data", dir}} {
		var out bytes.Buffer
		if err := reindex(args, &out); err != nil {
			t.Fatalf("reindex %v: %v", args, err)
		}
		if !regexp.MustCompile(`^reindexed 3 items across \d+ content types in \S+\n$`).MatchString(out.String()) {
			t.Fatalf("output %q", out.String())
		}
	}

	// reindex rebuilds the Arabic-normalised index too, even when its row count is in step
	// (drift the boot-time self-heal does not see, such as an edit by an older binary).
	d, err := db.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.ExecContext(ctx, `UPDATE content_fts_v2 SET title = 'stale'`); err != nil {
		t.Fatal(err)
	}
	if err := reindex([]string{"--no-jobs", "--data", dir}, io.Discard); err != nil {
		t.Fatal(err)
	}
	var rows, folded int
	if err := d.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE title = 'احمد') FROM content_fts_v2`).Scan(&rows, &folded); err != nil {
		t.Fatal(err)
	}
	if rows != 3 || folded != 1 {
		t.Fatalf("content_fts_v2 after reindex: %d rows, %d folded", rows, folded)
	}
}
