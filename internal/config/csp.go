package config

import (
	"fmt"
	"net/url"
	"strings"
)

// maxScriptSources bounds GORUN_CSP_SCRIPT_SRC.
const maxScriptSources = 32

// PublicScriptSources validates the operator's extra script-src entries for
// public pages (never the admin). The bundled themes need none; a custom
// theme that still loads a CDN or a self-hosted standard Alpine build lists
// what it needs, e.g. "https://cdn.jsdelivr.net 'unsafe-eval'". Entries are
// https origins or URL prefixes, or the keyword 'unsafe-eval'; anything
// broader (https:, *, 'unsafe-inline', data:) is refused.
func (c Config) PublicScriptSources() ([]string, error) {
	if len(c.CSPScriptSrc) > maxScriptSources {
		return nil, fmt.Errorf("config: too many csp script sources")
	}
	out := make([]string, 0, len(c.CSPScriptSrc))
	for _, raw := range c.CSPScriptSrc {
		src := strings.TrimSpace(raw)
		if src == "" {
			continue
		}
		if src == "'unsafe-eval'" {
			out = append(out, src)
			continue
		}
		u, err := url.Parse(src)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.Host == "*" || u.User != nil ||
			u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(src, " ;,'\"") {
			return nil, fmt.Errorf("config: invalid csp script source %q (want an https origin or 'unsafe-eval')", src)
		}
		out = append(out, src)
	}
	return out, nil
}
