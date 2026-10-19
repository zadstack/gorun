package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/settings"
)

const updateAttemptKey = "system.update.attempt_at"

var updateResourcePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// A deployment destination comes exclusively from server configuration. Request
// parameters cannot change the host, token, resource, repository or branch.
func updateEndpoint(c config.Config) (string, error) {
	u, err := url.Parse(c.UpdateURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(c.UpdateToken, "\r\n") || strings.TrimSpace(c.UpdateToken) == "" || !updateResourcePattern.MatchString(c.UpdateResource) {
		return "", errors.New("deployment integration is not configured")
	}
	u.Path = "/api/v1/deploy"
	return u.String(), nil
}

func (h *handlers) updatesPage(w http.ResponseWriter, r *http.Request) {
	_, err := updateEndpoint(h.d.Config)
	var attempt string
	_ = h.d.Settings.Get(r.Context(), updateAttemptKey, &attempt)
	h.tpl.Render(w, r, "pages/system/updates.html", struct {
		Version, Attempt string
		Configured       bool
	}{api.CoreVersion, attempt, err == nil})
}

func (h *handlers) updateDeploy(w http.ResponseWriter, r *http.Request) {
	// Serialize the entire backup/dispatch, including the durable cooldown check.
	h.updateMu.Lock()
	defer h.updateMu.Unlock()
	endpoint, err := updateEndpoint(h.d.Config)
	if err != nil || r.FormValue("confirm") != "yes" {
		http.Error(w, "Update is not configured or confirmed", http.StatusBadRequest)
		return
	}
	var previous string
	if err := h.d.Settings.Get(r.Context(), updateAttemptKey, &previous); err != nil && !errors.Is(err, settings.ErrNotFound) {
		h.serverError(w, r, err)
		return
	}
	if last, err := time.Parse(time.RFC3339, previous); err == nil && time.Since(last) < 10*time.Minute {
		httpx.SetFlash(w, "error", "admin.updates.cooldown")
		http.Redirect(w, r, "/admin/system/updates", http.StatusSeeOther)
		return
	}
	// A failed backup must prevent deployment. Keep the database and media in the
	// existing persistent data volume; secrets remain in the deployment platform.
	backupMu.Lock()
	now := time.Now().UTC()
	result, err := backup.Create(h.d.Config.DataDir, h.d.Config.DBPath(), now)
	if err == nil {
		_, err = verifiedBackup(result, now)
	}
	backupMu.Unlock()
	if err != nil {
		h.d.Audit.Record(r.Context(), "system.update.backup_failed", "system", "update", nil)
		httpx.SetFlash(w, "error", "admin.updates.backup_failed")
		http.Redirect(w, r, "/admin/system/updates", http.StatusSeeOther)
		return
	}
	if err := h.d.Settings.Set(r.Context(), updateAttemptKey, now.Format(time.RFC3339)); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.d.Audit.Record(r.Context(), "system.update.requested", "system", "update", map[string]any{"backup": result.Stamp, "version": api.CoreVersion})
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	accepted := dispatchUpdate(r.Context(), h.d.Config, endpoint, client)
	if accepted {
		h.d.Audit.Record(r.Context(), "system.update.accepted", "system", "update", nil)
		httpx.SetFlash(w, "success", "admin.updates.accepted")
	} else {
		h.d.Audit.Record(r.Context(), "system.update.unconfirmed", "system", "update", nil)
		httpx.SetFlash(w, "error", "admin.updates.unconfirmed")
	}
	http.Redirect(w, r, "/admin/system/updates", http.StatusSeeOther)
}

func dispatchUpdate(ctx context.Context, c config.Config, endpoint string, client *http.Client) bool {
	payload, _ := json.Marshal(map[string]string{"uuid": c.UpdateResource})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.UpdateToken)
	// An operator-selected private Coolify host is allowed without weakening the
	// application's user-controlled URL fetch policy. Never forward credentials
	// through redirects, and never print upstream response bodies or URL errors.
	resp, err := client.Do(req)
	accepted := false
	if err == nil {
		defer resp.Body.Close()
		var body struct {
			Deployments []struct {
				Resource string `json:"resource_uuid"`
				ID       string `json:"deployment_uuid"`
			} `json:"deployments"`
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 && json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body) == nil {
			for _, deployment := range body.Deployments {
				if deployment.Resource == c.UpdateResource && deployment.ID != "" {
					accepted = true
				}
			}
		}
	}
	return accepted
}
