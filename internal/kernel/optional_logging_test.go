package kernel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"log/slog"
	"testing"

	"github.com/zadstack/gorun/internal/config"
)

type requiredSelectionFixture struct{ name string }

func (m requiredSelectionFixture) Name() string       { return m.name }
func (requiredSelectionFixture) Migrations() fs.FS    { return nil }
func (requiredSelectionFixture) Register(*Deps) error { return nil }

type optionalSelectionFixture struct{ requiredSelectionFixture }

func (optionalSelectionFixture) Enabled(context.Context, *sql.DB) (bool, error) {
	return true, nil
}

func TestConfiguredModesWarnOnlyForNonoptionalModules(t *testing.T) {
	// Modes explicitly supplied for required modules are ignored, even when
	// the requested mode happens to agree with the module's enabled state.
	for _, mode := range []string{AddonModeDisabled, AddonModeEnabled, AddonModeAuto} {
		for _, optional := range []bool{false, true} {
			name := "required/" + mode
			module := Module(requiredSelectionFixture{name: "fixture"})
			if optional {
				name = "optional/" + mode
				module = optionalSelectionFixture{requiredSelectionFixture{name: "fixture"}}
			}
			t.Run(name, func(t *testing.T) {
				var logs bytes.Buffer
				logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
				cfg := config.Config{Addons: map[string]string{"fixture": mode}}
				selected, err := enabledModules(context.Background(), cfg, logger, nil, []Module{requiredSelectionFixture{name: "settings"}, module})
				if err != nil {
					t.Fatal(err)
				}
				wantSelected := 2
				if optional && mode == AddonModeDisabled {
					wantSelected = 1
				}
				if len(selected) != wantSelected {
					t.Fatalf("selected %d modules, want %d", len(selected), wantSelected)
				}
				if optional {
					if logs.Len() != 0 {
						t.Fatalf("valid optional mode warned: %s", logs.String())
					}
					return
				}
				var warning struct {
					Level, Module, Mode string
				}
				if err := json.Unmarshal(logs.Bytes(), &warning); err != nil {
					t.Fatalf("expected one structured warning, got %q: %v", logs.String(), err)
				}
				if warning.Level != "WARN" || warning.Module != "fixture" || warning.Mode != mode {
					t.Fatalf("incorrect ignored-mode warning: %+v", warning)
				}
			})
		}
	}
}

func TestUnconfiguredNonoptionalModuleDoesNotWarn(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	selected, err := enabledModules(context.Background(), config.Config{}, logger, nil, []Module{requiredSelectionFixture{name: "settings"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || logs.Len() != 0 {
		t.Fatalf("unconfigured required module: selected=%d logs=%q", len(selected), logs.String())
	}
}
