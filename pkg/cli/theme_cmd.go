package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/pkg/themekit"
)

const themeUsage = "usage: gorun theme info | doctor [slug] [--json] [--warnings] | activate <slug> | show <template> | save <template> [--draft] (body on stdin) | reset <template> | reparent <child> <parent> [--apply] [--data dir]"

type themeInfo struct {
	Active    string          `json:"active"`
	Themes    []themeEntry    `json:"themes"`
	Templates []templateEntry `json:"templates"`
}

type themeEntry struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Parent  string `json:"parent"`
}

type templateEntry struct {
	Name   string `json:"name"`
	Source string `json:"source"` // theme | parent | override
	Draft  bool   `json:"draft"`  // an unpublished draft exists
}

// themeCmd works with the active theme like Appearance in the admin does,
// through the theme service (so activation and saving are validated the
// same way): info (JSON), doctor (pkg/themekit contract, no DB), activate,
// show/save/reset a template override.
func themeCmd(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(themeUsage)
	}
	sub, args := args[0], args[1:]
	draft, args := boolFlag(args, "draft")
	apply, args := boolFlag(args, "apply")
	asJSON, args := boolFlag(args, "json")
	withWarnings, args := boolFlag(args, "warnings")
	var name, parent string
	switch sub {
	case "info":
		// no positional args
	case "doctor":
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			name, args = args[0], args[1:]
		}
	default:
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return errors.New(themeUsage)
		}
		name, args = args[0], args[1:]
	}
	if sub == "reparent" {
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return errors.New(themeUsage)
		}
		parent, args = args[0], args[1:]
	}
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	if sub == "doctor" {
		return themeDoctor(cfg, name, asJSON, withWarnings, out)
	}
	ctx := context.Background()
	app, err := kernel.BootWithOptions(ctx, cfg, []kernel.Module{settingsmod.New(), theme.New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		return err
	}
	defer app.Close()
	svc, ok := kernel.Get[theme.Service](app.Deps, kernel.ServiceTheme)
	if !ok {
		return errors.New("theme: service missing")
	}
	audit := func(action string, meta map[string]any) {
		meta["via"] = "cli"
		app.Deps.Audit.Record(ctx, action, "theme", name, meta)
	}
	switch sub {
	case "info":
		info := themeInfo{Active: svc.Active(ctx).Slug, Themes: []themeEntry{}, Templates: []templateEntry{}}
		list, err := svc.List(ctx)
		if err != nil {
			return err
		}
		for _, t := range list {
			info.Themes = append(info.Themes, themeEntry{t.Slug, t.Name, t.Version, t.Parent})
		}
		files, err := svc.Templates(ctx)
		if err != nil {
			return err
		}
		drafts := map[string]bool{}
		if ovs, err := svc.Overrides(ctx); err == nil {
			for _, o := range ovs {
				if o.Draft {
					drafts[o.Name] = true
				}
			}
		}
		for _, f := range files {
			info.Templates = append(info.Templates, templateEntry{f.Name, f.Source, drafts[f.Name]})
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	case "activate":
		if err := svc.Activate(ctx, name); err != nil {
			return err
		}
		audit("theme.activate", map[string]any{})
		fmt.Fprintf(out, "active theme: %s\n", name)
	case "show":
		body, err := svc.ReadTemplate(ctx, name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(out, body)
		return err
	case "save":
		body, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if err != nil {
			return err
		}
		if err := svc.SaveOverride(ctx, name, string(body), draft); err != nil {
			return err
		}
		audit("theme.override.save", map[string]any{"draft": draft})
		fmt.Fprintf(out, "saved %s%s\n", name, map[bool]string{true: " (draft)"}[draft])
	case "reparent":
		roots := append([]string{theme.ThemesDir(cfg)}, theme.ExtraThemesDirs()...)
		rep, err := theme.Reparent(roots, name, parent, apply, time.Now())
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(rep); encErr != nil {
			return encErr
		}
		if err != nil {
			return err
		}
		if rep.Applied {
			audit("theme.reparent", map[string]any{"from": rep.OldParent, "to": parent, "backup": rep.BackupPath})
			fmt.Fprintln(out, "theme.json updated; restart the server to load the new parent")
		}
	case "reset":
		if err := svc.DeleteOverride(ctx, name); err != nil {
			return err
		}
		audit("theme.override.delete", map[string]any{})
		fmt.Fprintf(out, "reset %s to the theme's file\n", name)
	default:
		return errors.New(themeUsage)
	}
	return nil
}

// doctorReport is the JSON shape for `gorun theme doctor --json`.
type doctorReport struct {
	themekit.Result
	Warnings []themekit.Issue `json:"warnings,omitempty"`
}

// themeDoctor runs pkg/themekit validators without booting the kernel.
// Contract failures use ValidateDir / DoctorRoots (same as catalog CI).
// --warnings adds soft lang-key scans that do not fail the exit code.
func themeDoctor(cfg config.Config, slug string, asJSON, withWarnings bool, out io.Writer) error {
	roots := append([]string{theme.ThemesDir(cfg)}, theme.ExtraThemesDirs()...)
	var results []themekit.Result
	if slug == "" {
		results = themekit.DoctorRoots(roots)
	} else {
		dir, err := findThemeDirForDoctor(roots, slug)
		if err != nil {
			return err
		}
		results = []themekit.Result{themekit.DoctorTheme(dir, roots)}
	}
	reports := make([]doctorReport, 0, len(results))
	for _, res := range results {
		rep := doctorReport{Result: res}
		if withWarnings {
			dir := ""
			for _, root := range roots {
				cand := filepath.Join(root, res.Slug)
				if _, err := os.Stat(filepath.Join(cand, "theme.json")); err == nil {
					dir = cand
					break
				}
			}
			if dir != "" {
				warns, err := themekit.LangKeyWarnings(dir, roots)
				if err != nil {
					return err
				}
				rep.Warnings = warns
			}
		}
		reports = append(reports, rep)
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(reports); err != nil {
			return err
		}
	} else {
		for _, rep := range reports {
			fmt.Fprint(out, themekit.FormatResult(rep.Result))
			for _, w := range rep.Warnings {
				fmt.Fprintf(out, "  ~ warning: %s\n", w)
			}
		}
	}
	if themekit.AnyFailed(results) {
		return &exitError{code: 1, msg: "theme doctor: catalog contract errors found"}
	}
	return nil
}

func findThemeDirForDoctor(roots []string, slug string) (string, error) {
	for _, root := range roots {
		cand := filepath.Join(root, slug)
		if st, err := os.Stat(filepath.Join(cand, "theme.json")); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(cand); err == nil {
				return abs, nil
			}
			return cand, nil
		}
	}
	return "", fmt.Errorf("theme doctor: theme %q not found", slug)
}
