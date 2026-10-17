package theme

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Reparenting moves a child theme (usually a brand theme built on a
// retired parent such as studio-editorial) onto another parent. The report
// shows, before anything changes, which inherited files would now come from
// a different theme and which part/component references would no longer
// resolve; Apply backs up theme.json and rewrites only its "parent".

// ReparentReport describes the effect of moving Child from OldParent to NewParent.
type ReparentReport struct {
	Child      string            `json:"child"`
	OldParent  string            `json:"old_parent"`
	NewParent  string            `json:"new_parent"`
	Changed    []InheritedSource `json:"changed"`   // inherited files whose source theme changes
	Missing    []string          `json:"missing"`   // part/component references the new chain cannot resolve
	Unchanged  int               `json:"unchanged"` // inherited files with the same source
	Applied    bool              `json:"applied"`   // theme.json was rewritten
	BackupPath string            `json:"backup,omitempty"`
}

// InheritedSource is one inherited file and where it comes from before/after.
type InheritedSource struct {
	Name   string `json:"name"` // e.g. templates/single.html
	Before string `json:"before"`
	After  string `json:"after"` // "" when no theme in the new chain provides it
}

var themeRef = regexp.MustCompile(`\{\{-?\s*(part|component)\s+"([A-Za-z0-9_.-]+)"`)

// themeFiles lists templates/, parts/ and components/ files of a theme dir.
func themeFiles(dir string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, sub := range []string{"templates", "parts", "components"} {
		root := filepath.Join(dir, sub)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return fs.SkipDir
			}
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
				return err
			}
			rel, _ := filepath.Rel(dir, p)
			out[filepath.ToSlash(rel)] = true
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// chainFrom is slug followed by its ancestors (cycle-safe).
func chainFrom(roots []string, slug string) ([]Info, error) {
	var chain []Info
	for slug != "" {
		if slices.ContainsFunc(chain, func(i Info) bool { return i.Slug == slug }) {
			return nil, fmt.Errorf("%w: parent cycle at %q", ErrInvalidTheme, slug)
		}
		info, err := findInfo(roots, slug)
		if err != nil {
			return nil, err
		}
		chain = append(chain, info)
		slug = info.Parent
	}
	return chain, nil
}

func sourceOf(chain []Info, files []map[string]bool, name string) string {
	for i, info := range chain {
		if files[i][name] {
			return info.Slug
		}
	}
	return ""
}

// Reparent reports (and with apply, performs) moving child onto parent.
func Reparent(roots []string, child, parent string, apply bool, now time.Time) (ReparentReport, error) {
	ci, err := findInfo(roots, child)
	if err != nil {
		return ReparentReport{}, err
	}
	rep := ReparentReport{Child: child, OldParent: ci.Parent, NewParent: parent, Changed: []InheritedSource{}, Missing: []string{}}
	if parent == child {
		return rep, fmt.Errorf("%w: a theme cannot be its own parent", ErrInvalidTheme)
	}
	newChain, err := chainFrom(roots, parent)
	if err != nil {
		return rep, err
	}
	if slices.ContainsFunc(newChain, func(i Info) bool { return i.Slug == child }) {
		return rep, fmt.Errorf("%w: %q already descends from %q", ErrInvalidTheme, parent, child)
	}
	var oldChain []Info
	if ci.Parent != "" {
		if oldChain, err = chainFrom(roots, ci.Parent); err != nil {
			// A missing old parent is exactly the retired case; report everything as new.
			oldChain = nil
		}
	}
	load := func(chain []Info) ([]map[string]bool, error) {
		out := make([]map[string]bool, len(chain))
		for i, info := range chain {
			f, err := themeFiles(info.Dir)
			if err != nil {
				return nil, err
			}
			out[i] = f
		}
		return out, nil
	}
	own, err := themeFiles(ci.Dir)
	if err != nil {
		return rep, err
	}
	oldFiles, err := load(oldChain)
	if err != nil {
		return rep, err
	}
	newFiles, err := load(newChain)
	if err != nil {
		return rep, err
	}
	names := map[string]bool{}
	for _, m := range append(slices.Clone(oldFiles), newFiles...) {
		for n := range m {
			if !own[n] {
				names[n] = true
			}
		}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	slices.Sort(sorted)
	for _, n := range sorted {
		before, after := sourceOf(oldChain, oldFiles, n), sourceOf(newChain, newFiles, n)
		if before == after {
			rep.Unchanged++
			continue
		}
		if before != "" {
			rep.Changed = append(rep.Changed, InheritedSource{Name: n, Before: before, After: after})
		}
	}
	// References made by the child's own files must resolve in child + new chain.
	resolvable := func(rel string) bool {
		if own[rel] {
			return true
		}
		return sourceOf(newChain, newFiles, rel) != ""
	}
	seen := map[string]bool{}
	for rel := range own {
		body, err := os.ReadFile(filepath.Join(ci.Dir, filepath.FromSlash(rel)))
		if err != nil {
			return rep, err
		}
		for _, m := range themeRef.FindAllStringSubmatch(string(body), -1) {
			target := map[string]string{"part": "parts/", "component": "components/"}[m[1]] + m[2] + ".html"
			if !resolvable(target) && !seen[target] {
				seen[target] = true
				rep.Missing = append(rep.Missing, target)
			}
		}
	}
	slices.Sort(rep.Missing)
	if !apply {
		return rep, nil
	}
	if len(rep.Missing) > 0 {
		return rep, fmt.Errorf("%w: %d references would not resolve under %q", ErrInvalidTheme, len(rep.Missing), parent)
	}
	path := filepath.Join(ci.Dir, "theme.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return rep, err
	}
	next, ok := setParent(raw, parent)
	if !ok {
		return rep, fmt.Errorf("%w: theme.json needs a top-level \"parent\" (a string or null) to update", ErrInvalidTheme)
	}
	info, err := os.Stat(path)
	if err != nil {
		return rep, err
	}
	if err := checkWritable(ci.Dir); err != nil {
		return rep, err
	}
	backup := path + ".bak-" + now.UTC().Format("20060102T150405Z")
	if err := os.WriteFile(backup, raw, info.Mode().Perm()); err != nil {
		return rep, err
	}
	if err := writeFile(path, string(next)); err != nil {
		return rep, err
	}
	rep.Applied, rep.BackupPath = true, backup
	return rep, nil
}

// setParent rewrites only the top-level "parent" value (a string or null),
// keeping every other byte of the file, so nested "parent" keys (in a
// setting, say) and the file's layout stay as they are. It fails when raw
// is not a JSON object or has no top-level "parent".
func setParent(raw []byte, parent string) ([]byte, bool) {
	if !json.Valid(raw) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		keyEnd := dec.InputOffset()
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, false
		}
		if key, _ := tok.(string); key != "parent" {
			continue
		}
		if v := string(val); v != "null" && !strings.HasPrefix(v, `"`) {
			return nil, false
		}
		end := int(dec.InputOffset())
		// The value starts after the colon and any whitespace that follow the key.
		start := int(keyEnd)
		for start < end && (raw[start] == ':' || raw[start] == ' ' || raw[start] == '\t' || raw[start] == '\n' || raw[start] == '\r') {
			start++
		}
		quoted, err := json.Marshal(parent)
		if err != nil {
			return nil, false
		}
		out := append(append(slices.Clone(raw[:start]), quoted...), raw[end:]...)
		return out, true
	}
	return nil, false
}

// checkWritable fails unless files can be created in dir.
func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".reparent-check-*")
	if err != nil {
		return fmt.Errorf("%w: theme directory %s is not writable: %v", ErrInvalidTheme, dir, err)
	}
	name := f.Name()
	return errors.Join(f.Close(), os.Remove(name))
}
