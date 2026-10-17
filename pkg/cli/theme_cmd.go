package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/theme"
)

const themeUsage = "usage: gorun theme info | activate <slug> | show <template> | save <template> [--draft] (body on stdin) | reset <template> | reparent <child> <parent> [--apply] [--data dir]"

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
// same way): info (JSON), activate, show/save/reset a template override.
func themeCmd(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(themeUsage)
	}
	sub, args := args[0], args[1:]
	draft, args := boolFlag(args, "draft")
	apply, args := boolFlag(args, "apply")
	var name, parent string
	if sub != "info" {
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
