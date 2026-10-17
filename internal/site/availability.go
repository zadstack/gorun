package site

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/zadstack/gorun/internal/settings"
)

const SettingAvailability = "site.availability"
const maxPolicyBytes = 2 * 1024 * 1024

// DefaultPolicyDirectory is the root-owned directory GORUN_SITE_POLICY files
// must live in, unless a build sets another one with SetPolicyDirectory.
const DefaultPolicyDirectory = "/var/lib/gorun-site-policies"

var policyDirectory = DefaultPolicyDirectory

// SetPolicyDirectory sets the trusted policy directory for this process. It
// is a build-time choice (call it from main before Boot), not configuration:
// the directory is a trust root, so it never comes from the environment. An
// empty or relative dir is ignored.
func SetPolicyDirectory(dir string) {
	if dir != "" && filepath.IsAbs(dir) {
		policyDirectory = filepath.Clean(dir)
	}
}

type Availability struct {
	SiteID       string     `json:"site_id,omitempty"`
	Revision     int64      `json:"revision"`
	Mode         string     `json:"mode"`
	Message      string     `json:"message"`
	PasswordHash string     `json:"password_hash,omitempty"`
	PageRules    []PageRule `json:"page_rules,omitempty"`
}

var policyID = regexp.MustCompile(`^[0-9a-hjkmnp-tv-z]{26}$`)
var passwordPHC = regexp.MustCompile(`^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`)

func validAvailability(p Availability, platform bool) bool {
	if len(p.Message) > 8000 || len([]rune(p.Message)) > 2000 || strings.ContainsRune(p.Message, 0) || p.Revision < 0 || (platform && p.Mode != "password" && p.PasswordHash != "") {
		return false
	}
	switch p.Mode {
	case "public":
	case "construction":
		if platform {
			return false
		}
	case "blocked", "banner":
		if !platform {
			return false
		}
	case "password":
		if !passwordPHC.MatchString(p.PasswordHash) {
			return false
		}
	default:
		return false
	}
	return true
}

func platformPolicy() (Availability, error) {
	path := os.Getenv("GORUN_SITE_POLICY")
	if path == "" {
		return Availability{Mode: "public"}, nil
	}
	base := strings.TrimSuffix(filepath.Base(path), ".json")
	if filepath.Dir(path) != policyDirectory || !strings.HasSuffix(path, ".json") || !policyID.MatchString(base) {
		return Availability{}, errors.New("invalid policy path")
	}
	dir, e := os.Lstat(policyDirectory)
	if e != nil {
		return Availability{}, e
	}
	if !trustedPolicyNode(dir, true) {
		return Availability{}, errors.New("unsafe policy directory")
	}
	st, e := os.Lstat(path)
	if e != nil {
		return Availability{}, e
	}
	if !trustedPolicyNode(st, false) {
		return Availability{}, errors.New("unsafe policy file")
	}
	f, e := os.Open(path)
	if e != nil {
		return Availability{}, e
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(st, opened) {
		return Availability{}, errors.New("policy changed during open")
	}
	var p Availability
	dec := json.NewDecoder(io.LimitReader(f, maxPolicyBytes+1))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&p); e != nil {
		return Availability{}, e
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || !validAvailability(p, true) || !validPageRules(p.PageRules) || strings.ToLower(p.SiteID) != base {
		return Availability{}, errors.New("invalid policy")
	}
	return p, nil
}
func trustedPolicyNode(st os.FileInfo, directory bool) bool {
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != 0 || st.Mode().Perm()&0022 != 0 || st.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if directory {
		return st.IsDir()
	}
	return st.Mode().IsRegular() && st.Size() <= maxPolicyBytes && st.Mode().Perm()&0007 == 0
}
func (h *handlers) localAvailability(ctx context.Context) (Availability, error) {
	p := Availability{Mode: "public"}
	if h.d.Settings == nil {
		return p, nil
	}
	if e := h.d.Settings.Get(ctx, SettingAvailability, &p); e != nil && !errors.Is(e, settings.ErrNotFound) {
		return Availability{}, e
	}
	if !validAvailability(p, false) {
		return Availability{}, errors.New("invalid availability setting")
	}
	return p, nil
}
func effectiveAvailability(platform, local Availability) Availability {
	if platform.Mode == "blocked" || platform.Mode == "password" {
		return platform
	}
	return local
}
