package update

import (
	"net/http"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/config"
)

// ConfiguredSource validates the operator's release index URL. HTTPS is
// required except for a loopback address, which lets a local supervisor
// publish the index on the same host.
func ConfiguredSource(c config.Config) (Source, error) {
	raw := strings.TrimSpace(c.Update.SourceURL)
	if raw == "" {
		return nil, ErrNotConfigured
	}
	client := &http.Client{Timeout: 20 * time.Second}
	if strings.HasPrefix(raw, "http://127.0.0.1") || strings.HasPrefix(raw, "http://localhost") || strings.HasPrefix(raw, "http://[::1]") {
		return NewLoopbackReleaseIndex(raw, c.Update.SourceToken, c.Update.Artifact, client)
	}
	return NewReleaseIndex(raw, c.Update.SourceToken, c.Update.Artifact, client)
}
