package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

const apiDocsBegin = "<!-- BEGIN GENERATED API OPERATIONS -->"
const apiDocsEnd = "<!-- END GENERATED API OPERATIONS -->"

func apiDocsCmd(args []string, out io.Writer) (err error) {
	fs := flag.NewFlagSet("api-docs", flag.ContinueOnError)
	addonHelp := "include an optional addon"
	if len(active.addons) > 0 {
		addonHelp += " (" + strings.Join(active.addons, " or ") + ")"
	}
	addon := fs.String("addon", "", addonHelp)
	check := fs.String("check", "", "verify the generated section of an API Markdown file")
	update := fs.String("update", "", "replace the generated section of an API Markdown file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || (*check != "" && *update != "") {
		return fmt.Errorf("api-docs: use either --check or --update")
	}
	if *addon != "" && !slices.Contains(active.addons, *addon) {
		return fmt.Errorf("api-docs: unknown addon %q", *addon)
	}
	dir, err := os.MkdirTemp("", "gorun-api-docs-")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("api-docs: fixture cleanup: %w", cleanupErr))
		}
	}()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	cfg := config.Config{Env: "dev", DataDir: dir, Addr: ":0", BaseURL: "http://localhost", Theme: "default", SecretKey: key}
	if *addon != "" {
		cfg.Addons = map[string]string{*addon: "enabled"}
	}
	app, err := kernel.BootWithOptions(context.Background(), cfg, Modules(cfg), kernel.BootOptions{StartJobs: false})
	if err != nil {
		return err
	}
	defer app.Close()
	w := httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/openapi.json", nil))
	if w.Code != 200 {
		return fmt.Errorf("api-docs: OpenAPI returned %d", w.Code)
	}
	tables, err := apiOperationTables(w.Body.Bytes())
	if err != nil {
		return err
	}
	if *check == "" && *update == "" {
		_, err = io.WriteString(out, tables)
		return err
	}
	path := *check
	if *update != "" {
		path = *update
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(raw)
	start, end := strings.Index(text, apiDocsBegin), strings.Index(text, apiDocsEnd)
	if start < 0 || end < start {
		return fmt.Errorf("api-docs: generated section markers missing")
	}
	want := text[:start+len(apiDocsBegin)] + "\n" + tables + text[end:]
	if *check != "" {
		if want != text {
			addonFlag := ""
			if *addon != "" {
				addonFlag = " --addon " + *addon
			}
			return fmt.Errorf("api-docs: operation tables are stale; run api-docs%s --update %s", addonFlag, path)
		}
		_, err = fmt.Fprintf(out, "API operation tables match the current OpenAPI registry.\n")
		return err
	}
	return os.WriteFile(path, []byte(want), 0644)
}

func apiOperationTables(raw []byte) (string, error) {
	var doc struct {
		Paths map[string]map[string]struct {
			Summary string   `json:"summary"`
			Tags    []string `json:"tags"`
			Scopes  []string `json:"x-scopes"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	groups := map[string][]string{}
	count := 0
	for path, methods := range doc.Paths {
		for method, op := range methods {
			tag := "other"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			scopes := "any key or session"
			if len(op.Scopes) > 0 {
				scopes = "`" + strings.Join(op.Scopes, "`, `") + "`"
			}
			summary := strings.ReplaceAll(strings.ReplaceAll(op.Summary, "\n", " "), "|", "\\|")
			groups[tag] = append(groups[tag], fmt.Sprintf("| %s | `/api%s` | %s | %s |", strings.ToUpper(method), path, summary, scopes))
			count++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Generated from the current locally booted OpenAPI document: **%d operations** in %d tags.\n\n", count, len(groups))
	tags := make([]string, 0, len(groups))
	for tag := range groups {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		fmt.Fprintf(&b, "### %s\n\n| Method | Path | Summary | Scopes |\n|---|---|---|---|\n", tag)
		sort.Strings(groups[tag])
		for _, row := range groups[tag] {
			b.WriteString(row + "\n")
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}
