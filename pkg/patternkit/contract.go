// Package patternkit validates declarative site-layout pattern packages.
// Patterns are opinionated page structures layered above themes; they must
// not declare identity tokens or HTTP extension endpoints.
package patternkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// ManifestFile is the required package descriptor.
	ManifestFile = "pattern.json"
	// KindPattern is the only accepted kind value.
	KindPattern = "pattern"
)

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)

// Manifest is the on-disk pattern.json shape.
type Manifest struct {
	Kind        string            `json:"kind"`
	Slug        string            `json:"slug"`
	Version     string            `json:"version"`
	Name        map[string]string `json:"name"`
	Description map[string]string `json:"description"`
}

// Issue is one validation failure.
type Issue struct {
	Path string `json:"path,omitempty"`
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

func (i Issue) String() string {
	if i.Path == "" {
		return i.Code + ": " + i.Msg
	}
	return i.Path + ": " + i.Code + ": " + i.Msg
}

// ValidateDir checks a pattern package directory.
func ValidateDir(dir string) []Issue {
	var issues []Issue
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return []Issue{{Path: ManifestFile, Code: "missing_manifest", Msg: err.Error()}}
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return []Issue{{Path: ManifestFile, Code: "invalid_json", Msg: err.Error()}}
	}
	if m.Kind != KindPattern {
		issues = append(issues, Issue{Path: ManifestFile, Code: "kind", Msg: fmt.Sprintf("want %q, got %q", KindPattern, m.Kind)})
	}
	base := filepath.Base(dir)
	if !slugRe.MatchString(m.Slug) {
		issues = append(issues, Issue{Path: ManifestFile, Code: "slug", Msg: "must match ^[a-z][a-z0-9-]{1,40}$"})
	} else if m.Slug != base {
		issues = append(issues, Issue{Path: ManifestFile, Code: "slug_mismatch", Msg: fmt.Sprintf("slug %q must match directory %q", m.Slug, base)})
	}
	if strings.TrimSpace(m.Version) == "" {
		issues = append(issues, Issue{Path: ManifestFile, Code: "version", Msg: "required"})
	}
	if pick(m.Name) == "" {
		issues = append(issues, Issue{Path: ManifestFile, Code: "name", Msg: "need en or ar name"})
	}
	if _, err := os.Stat(filepath.Join(dir, "templates", "front-page.html")); err != nil {
		if _, err2 := os.Stat(filepath.Join(dir, "templates", "home.html")); err2 != nil {
			issues = append(issues, Issue{Path: "templates/", Code: "home", Msg: "need templates/front-page.html or templates/home.html"})
		}
	}
	for _, loc := range []string{"en", "ar"} {
		if _, err := os.Stat(filepath.Join(dir, "lang", loc+".json")); err != nil {
			issues = append(issues, Issue{Path: "lang/" + loc + ".json", Code: "locale", Msg: "required"})
		}
	}
	// Reject identity CSS token selectors — patterns must stay layout-only.
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".css" && ext != ".html" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		s := string(body)
		if strings.Contains(s, ":where(html[data-theme") {
			rel, _ := filepath.Rel(dir, path)
			issues = append(issues, Issue{Path: rel, Code: "identity_tokens", Msg: "patterns must not ship theme identity token selectors"})
		}
		for _, host := range []string{"cdn.tailwindcss.com", "cdn.jsdelivr.net", "unpkg.com"} {
			if strings.Contains(s, host) {
				rel, _ := filepath.Rel(dir, path)
				issues = append(issues, Issue{Path: rel, Code: "remote_script", Msg: "forbidden host " + host})
			}
		}
		return nil
	})
	return issues
}

func pick(m map[string]string) string {
	if v := strings.TrimSpace(m["en"]); v != "" {
		return v
	}
	return strings.TrimSpace(m["ar"])
}
