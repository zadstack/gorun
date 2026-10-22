package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/observe"
)

// Capability evidence levels. A connectivity check (list models, discover
// tools, probe a bucket) proves reachability only. An operation test
// exercises the real operation with an isolated sample under an explicit
// permission: send one mail to the requesting operator, write/read/delete
// one probe object, request one tiny completion. MCP tool invocation would
// execute remote actions, so MCP has no operation test. Nothing here runs
// during a page load or a GET.
const (
	PermCapabilityTest = "system.capability_test"

	kindConnection    = "connection"
	kindOperationS3   = "operation.s3"
	kindOperationAI   = "operation.ai"
	kindOperationMail = "operation.mail"

	connectionFreshness = 12 * time.Hour
	evidenceTTL         = 7 * 24 * time.Hour
	operationTimeout    = 30 * time.Second
	probeObjectName     = "gorun-capability-probe-"
	aiProbeMaxTokens    = 16
)

var errOperationUnsupported = errors.New("capability: no safe operation test for this kind")

func (h *handlers) profileRevision(ctx context.Context, id string) (string, error) {
	c, err := h.loadConnection(ctx, id)
	if connectionMissing(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return c.Version, nil
}

func (h *handlers) profileKeys(kind string) func(context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		list, err := h.listConnections(ctx)
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, c := range list {
			if kind == "" || c.Kind == kind {
				ids = append(ids, c.ID)
			}
		}
		return ids, nil
	}
}

// registerCapabilityObservations declares the connectivity and operation
// kinds. Connectivity is scheduled (read-only discovery); operation tests
// run only on an explicit request.
func (h *handlers) registerCapabilityObservations() {
	h.observe.Register(observe.Spec{Kind: kindConnection, Freshness: connectionFreshness, Timeout: 25 * time.Second, MaxAttempts: 2, MaxInFlight: 2,
		Keys: h.profileKeys(""), Revision: h.profileRevision,
		Run: func(ctx context.Context, id, revision string) (observe.Result, error) {
			c, err := h.loadConnection(ctx, id)
			if err != nil {
				return observe.Result{}, err
			}
			secrets, err := h.connectionSecrets(c)
			if err != nil {
				return observe.Result{}, errors.New("credentials unreadable with the current site secret key")
			}
			result, probeErr := probeConnection(ctx, c, secrets)
			// Keep the legacy fields on the profile in step (compare-and-swap on the revision).
			if err := h.writeConnection(ctx, recordConnectionProbe(c, result, probeErr), revision, "connections.test", false); err != nil && !errors.Is(err, errConnectionConflict) {
				return observe.Result{}, err
			}
			if probeErr != nil {
				return observe.Result{}, probeErr
			}
			return observe.Result{Scope: "connectivity", Data: map[string]any{"count": result.Count, "protocol": result.Protocol}}, nil
		},
		Remediation: "Review connection settings, credentials and provider availability, then run the check again."})
	h.observe.Register(observe.Spec{Kind: kindOperationS3, Freshness: 0, Timeout: operationTimeout, MaxAttempts: 1, Revision: h.profileRevision,
		Run: h.runS3Operation, Remediation: "Grant the credentials write, read and delete permission on the bucket; the probe object is removed after the test."})
	h.observe.Register(observe.Spec{Kind: kindOperationAI, Freshness: 0, Timeout: operationTimeout, MaxAttempts: 1, Revision: h.profileRevision,
		Run: h.runAIOperation, Remediation: "Confirm the model accepts chat completions for this key; the test sends one fixed prompt with a tiny token limit."})
	h.observe.Register(observe.Spec{Kind: kindOperationMail, Freshness: 0, Timeout: operationTimeout, MaxAttempts: 1,
		Revision: h.mailRevision, Enabled: func(ctx context.Context) bool { return h.mail.Configured(ctx) },
		Run: h.runMailOperation, Remediation: "Check the SMTP host, port, credentials and sender; the test sends one message to the requesting operator only."})
}

// mailRevision binds mail evidence to the mail settings and the requesting
// operator (key "user:<id>"), so edited settings or a removed user expire it.
func (h *handlers) mailRevision(ctx context.Context, key string) (string, error) {
	id := strings.TrimPrefix(key, "user:")
	if id == key || id == "" {
		return "", nil
	}
	u, err := h.users.Get(ctx, id)
	if err != nil || u == nil || u.Status != auth.StatusActive {
		return "", nil
	}
	parts := []string{}
	for _, k := range []string{"mail.host", "mail.port", "mail.username", "mail.from", "mail.from_name", "mail.tls"} {
		parts = append(parts, h.d.Settings.GetString(ctx, k, ""))
	}
	parts = append(parts, u.Email)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8]), nil
}

func (h *handlers) runMailOperation(ctx context.Context, key, _ string) (observe.Result, error) {
	u, err := h.users.Get(ctx, strings.TrimPrefix(key, "user:"))
	if err != nil || u == nil {
		return observe.Result{}, errors.New("requesting operator no longer exists")
	}
	locale := u.Locale
	if !validLocale(locale) {
		locale = "en"
	}
	name := u.Name
	if name == "" {
		name = u.Email
	}
	subject, html, text, err := h.mail.Render(locale, "test", map[string]any{"Name": name, "SiteTitle": h.d.Settings.GetString(ctx, "site.title", "")})
	if err != nil {
		return observe.Result{}, errors.New("test message could not be rendered")
	}
	if err := h.mail.Send(ctx, u.Email, subject, html, text); err != nil {
		return observe.Result{}, errors.New("message was not accepted by the mail server")
	}
	h.d.Audit.Record(ctx, "mail.test_sent", "user", u.ID, map[string]any{"capability_test": true})
	return observe.Result{Scope: "operation:send", Data: map[string]any{"recipient": "requesting_operator"}}, nil
}

func (h *handlers) runS3Operation(ctx context.Context, id, _ string) (observe.Result, error) {
	c, err := h.loadConnection(ctx, id)
	if err != nil || c.Kind != "s3" {
		return observe.Result{}, errOperationUnsupported
	}
	s, err := h.connectionSecrets(c)
	if err != nil {
		return observe.Result{}, errors.New("credentials unreadable with the current site secret key")
	}
	// Like the AI client, a loopback endpoint (a local MinIO) may use plain
	// HTTP and a private address; every other endpoint must be public HTTPS.
	local := false
	if u, err := url.Parse(c.Endpoint); err == nil {
		local = isLocalHost(u.Hostname())
	}
	storage, err := media.NewS3Storage(media.S3Config{Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, AccessKey: s.AccessKey, SecretKey: s.SecretKey}, nil, local)
	if err != nil {
		return observe.Result{}, errors.New("bucket configuration incomplete")
	}
	client := httpx.NewSafeClientOpts(httpx.SafeClientOptions{Timeout: 20 * time.Second, AllowPrivate: local})
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	storage.SetClient(client)
	// The key follows the media driver's shape (YYYY/MM/name.ext) and a
	// unique name, so it never collides with real uploads.
	key := time.Now().UTC().Format("2006/01") + "/" + probeObjectName + db.NewID() + ".txt"
	sample := []byte("GoRun capability probe " + db.Now() + "\n")
	steps := map[string]any{"write": false, "read": false, "delete": false, "object": key}
	if err := storage.Put(ctx, key, bytes.NewReader(sample), "text/plain"); err != nil {
		return observe.Result{Data: steps}, errors.New("write failed: " + short(err.Error()))
	}
	steps["write"] = true
	deleted := false
	defer func() {
		// Cleanup policy: the probe object is always removed, even after a failed read.
		if !deleted {
			if err := storage.Delete(context.WithoutCancel(ctx), key); err == nil {
				steps["delete"] = true
			}
		}
	}()
	rc, err := storage.Open(ctx, key)
	if err != nil {
		return observe.Result{Data: steps}, errors.New("read failed: " + short(err.Error()))
	}
	got, _ := io.ReadAll(io.LimitReader(rc, 1024))
	rc.Close()
	if !bytes.Equal(got, sample) {
		return observe.Result{Data: steps}, errors.New("read returned different content")
	}
	steps["read"] = true
	if err := storage.Delete(ctx, key); err != nil {
		return observe.Result{Data: steps}, errors.New("delete failed: " + short(err.Error()))
	}
	deleted, steps["delete"] = true, true
	return observe.Result{Scope: "operation:write_read_delete", Data: steps}, nil
}

func (h *handlers) runAIOperation(ctx context.Context, id, _ string) (observe.Result, error) {
	c, err := h.loadConnection(ctx, id)
	if err != nil || c.Kind != "ai" {
		return observe.Result{}, errOperationUnsupported
	}
	s, err := h.connectionSecrets(c)
	if err != nil {
		return observe.Result{}, errors.New("credentials unreadable with the current site secret key")
	}
	cfg := aiConfig{Provider: c.Provider, BaseURL: strings.TrimRight(c.Endpoint, "/"), Model: c.Model, APIKey: s.Token, NamedConnection: true}
	reply, err := aiCompleteBounded(ctx, cfg, "Reply with the single word OK.", "OK?", aiProbeMaxTokens)
	if err != nil {
		return observe.Result{}, errors.New(short(redactErr(err, s.Token).Error()))
	}
	return observe.Result{Scope: "operation:complete", Data: map[string]any{"reply_chars": len(reply), "max_tokens": aiProbeMaxTokens}}, nil
}

func short(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// --- API ------------------------------------------------------------------------------

var capabilityTestErrors = []kernel.APIErrorResponse{
	{Status: 404, Code: "not_found", Description: "Unknown profile"},
	{Status: 409, Code: "operation_unsupported", Description: "This kind has no safe operation test (MCP tool invocation would execute remote actions)"},
	{Status: 409, Code: "capability_disabled", Description: "The capability is not configured or disabled"},
}

func (h *handlers) capabilityTestAPIOperations() {
	idParam := []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true}}
	check := api.Op("POST", "/site/capabilities/connections/{id}/check", "Queue a read-only connectivity/discovery check of a connection profile; answers 202 with the task reference",
		[]string{"site"}, []string{PermSettings}, nil, observe.Ref{}, h.apiCapabilityCheck)
	check.Params = idParam
	check.Contract = &kernel.APIContract{SuccessStatuses: []int{202}, Errors: capabilityTestErrors[:1]}
	operation := api.Op("POST", "/site/capabilities/connections/{id}/operation", "Queue an isolated operation test (S3: write/read/delete one probe object; AI: one tiny completion); answers 202",
		[]string{"site"}, []string{PermCapabilityTest}, nil, observe.Ref{}, h.apiCapabilityOperation)
	operation.Params = idParam
	operation.Contract = &kernel.APIContract{SuccessStatuses: []int{202}, Errors: capabilityTestErrors}
	mail := api.Op("POST", "/site/capabilities/services/mail/operation", "Queue one test message to the requesting operator's own address; answers 202",
		[]string{"site"}, []string{PermCapabilityTest}, nil, observe.Ref{}, h.apiMailOperation)
	mail.Contract = &kernel.APIContract{SuccessStatuses: []int{202}, Errors: capabilityTestErrors[2:]}
	for _, op := range []kernel.APIOperation{check, operation, mail} {
		h.d.API.Add(op)
	}
}

func (h *handlers) capabilityRefresh(w http.ResponseWriter, r *http.Request, kind, key, action string) {
	ref, err := h.observe.Refresh(r.Context(), kind, key)
	switch {
	case err == nil:
		h.d.Audit.Record(r.Context(), action, "capability", key, map[string]any{"kind": kind, "job": ref.JobID, "deduplicated": ref.Deduped})
		httpx.JSON(w, http.StatusAccepted, ref, nil)
	case errors.Is(err, observe.ErrNotFound), errors.Is(err, observe.ErrUnknownKind):
		api.NotFound(w)
	case errors.Is(err, observe.ErrDisabled):
		api.WriteError(w, http.StatusConflict, "capability_disabled", "the capability is not configured or disabled", nil)
	default:
		api.Fail(w, r, err)
	}
}

func (h *handlers) apiCapabilityCheck(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.loadConnection(r.Context(), id); err != nil {
		if connectionMissing(err) {
			api.NotFound(w)
			return
		}
		api.Fail(w, r, err)
		return
	}
	h.capabilityRefresh(w, r, kindConnection, id, "capability.check")
}

func (h *handlers) apiCapabilityOperation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, err := h.loadConnection(r.Context(), id)
	if err != nil {
		if connectionMissing(err) {
			api.NotFound(w)
			return
		}
		api.Fail(w, r, err)
		return
	}
	kind, ok := operationKind(c.Kind)
	if !ok {
		api.WriteError(w, http.StatusConflict, "operation_unsupported", "this connection kind has no safe operation test", map[string]string{"kind": c.Kind})
		return
	}
	h.capabilityRefresh(w, r, kind, id, "capability.operation")
}

func operationKind(profileKind string) (string, bool) {
	switch profileKind {
	case "s3":
		return kindOperationS3, true
	case "ai":
		return kindOperationAI, true
	}
	return "", false
}

func (h *handlers) apiMailOperation(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		api.WriteError(w, http.StatusForbidden, "forbidden", "a user principal is required to receive the test message", nil)
		return
	}
	h.capabilityRefresh(w, r, kindOperationMail, "user:"+u.ID, "capability.operation")
}

func mailOperationKey(u *auth.User) string {
	if u == nil {
		return ""
	}
	return fmt.Sprintf("user:%s", u.ID)
}
