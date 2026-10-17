package theme

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

// A missing themes directory or active theme fails the boot with a clear
// message instead of a 500 on every public page.
func TestBootFailsFastWithoutThemes(t *testing.T) {
	boot := func(root, active string) error {
		cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), Theme: active}
		app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), New(WithThemesDir(root))})
		if err == nil {
			app.Close()
		}
		return err
	}
	missing, _ := filepath.Abs(filepath.Join(t.TempDir(), "nope"))
	if err := boot(missing, "default"); err == nil || !strings.Contains(err.Error(), "themes directory not found") ||
		!strings.Contains(err.Error(), "GORUN_THEMES") || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing dir: %v", err)
	}
	empty := t.TempDir()
	os.MkdirAll(filepath.Join(empty, "other"), 0o755)
	if err := boot(empty, "default"); err == nil || !strings.Contains(err.Error(), `active theme "default" not found`) {
		t.Fatalf("missing active theme: %v", err)
	}
	t.Setenv(EnvThemesExtra, extraThemes)
	if err := boot("../../themes", "custom-fixture"); err != nil {
		t.Fatalf("repo themes: %v", err)
	}
}
