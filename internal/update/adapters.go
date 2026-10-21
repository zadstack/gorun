package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Artifact is an immutable release identity from a trusted source.
type Artifact struct {
	Version string `json:"version"`
	ID      string `json:"id" doc:"Immutable artifact identity, e.g. an image reference or archive name"`
	Digest  string `json:"digest" doc:"sha256:… from the release source; empty when the source publishes none"`
	Source  string `json:"source"`
}

// Source resolves release identities and digests from an operator-trusted
// location. It is read-only.
type Source interface {
	Name() string
	Resolve(ctx context.Context, version string) (Artifact, error)
	Latest(ctx context.Context) (Artifact, error)
}

// Activation statuses an adapter reports for a deployment reference.
const (
	ActivationQueued  = "queued"
	ActivationRunning = "running"
	ActivationActive  = "active"
	ActivationFailed  = "failed"
	ActivationUnknown = "unknown"
)

// Activation is the provider's view of a deployment reference. Active means
// the provider says the artifact runs; the lifecycle still requires the
// serving process to report the expected identity before the phase passes.
type Activation struct {
	Status string `json:"status" enum:"queued,running,active,failed,unknown"`
	Detail string `json:"detail"`
}

// Adapter requests activation of an artifact through a local supervisor or
// a configured deployment provider. Credentials and hosts come from server
// configuration only.
type Adapter interface {
	Name() string
	// Activate asks the provider to make artifact active and returns a
	// durable reference. Acceptance is not activation.
	Activate(ctx context.Context, artifact Artifact) (string, error)
	// Status reports the state of a reference returned by Activate.
	Status(ctx context.Context, ref string) (Activation, error)
}

// ErrNotConfigured means no trusted release source is configured.
var ErrNotConfigured = errors.New("update: release source not configured")

// --- Manual adapter --------------------------------------------------------

// ManualAdapter is the fallback when no provider is configured: an
// operator replaces the binary or image themselves. Activate records the
// request; Status stays queued until the serving identity changes (which
// Reconcile observes) or the activation timeout expires.
type ManualAdapter struct{}

func (ManualAdapter) Name() string { return "manual" }

func (ManualAdapter) Activate(_ context.Context, a Artifact) (string, error) {
	return "manual:" + a.Version, nil
}

func (ManualAdapter) Status(context.Context, string) (Activation, error) {
	return Activation{Status: ActivationQueued, Detail: "waiting for the operator to activate the artifact"}, nil
}

// --- Coolify adapter -------------------------------------------------------

var coolifyResourcePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// CoolifyAdapter requests a deployment of one operator-configured resource
// and reads the deployment status. The endpoint, token and resource come
// from server configuration; request parameters cannot change them.
type CoolifyAdapter struct {
	origin, token, resource string
	client                  *http.Client
}

// NewCoolifyAdapter validates the operator configuration (HTTPS origin only,
// no credentials, query or path) and returns the adapter.
func NewCoolifyAdapter(rawURL, token, resource string, client *http.Client) (*CoolifyAdapter, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") ||
		strings.ContainsAny(token, "\r\n") || strings.TrimSpace(token) == "" || !coolifyResourcePattern.MatchString(resource) {
		return nil, errors.New("deployment integration is not configured")
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	u.Path, u.RawPath = "", ""
	return &CoolifyAdapter{origin: u.String(), token: token, resource: resource, client: client}, nil
}

func (c *CoolifyAdapter) Name() string { return "coolify" }

func (c *CoolifyAdapter) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Never forward credentials through redirects and never surface upstream
	// bodies or URL errors (they may include the host or token).
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, errors.New("deployment provider could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("deployment provider returned HTTP %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(out); err != nil {
			return resp.StatusCode, errors.New("deployment provider returned an unreadable response")
		}
	}
	return resp.StatusCode, nil
}

// Activate posts the deploy request and requires the provider to echo the
// configured resource with a deployment id; anything else is not accepted.
func (c *CoolifyAdapter) Activate(ctx context.Context, _ Artifact) (string, error) {
	var body struct {
		Deployments []struct {
			Resource string `json:"resource_uuid"`
			ID       string `json:"deployment_uuid"`
		} `json:"deployments"`
	}
	if _, err := c.do(ctx, http.MethodPost, "/api/v1/deploy", map[string]string{"uuid": c.resource}, &body); err != nil {
		return "", err
	}
	for _, d := range body.Deployments {
		if d.Resource == c.resource && d.ID != "" && coolifyResourcePattern.MatchString(d.ID) {
			return d.ID, nil
		}
	}
	return "", errors.New("deployment provider did not accept the request for the configured resource")
}

// Status reads GET /api/v1/deployments/{id}. Coolify statuses map to:
// finished → active; failed/cancelled → failed; queued → queued;
// in_progress → running; others → unknown.
func (c *CoolifyAdapter) Status(ctx context.Context, ref string) (Activation, error) {
	if !coolifyResourcePattern.MatchString(ref) {
		return Activation{Status: ActivationUnknown}, errors.New("invalid deployment reference")
	}
	var body struct {
		Status string `json:"status"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/api/v1/deployments/"+ref, nil, &body); err != nil {
		return Activation{Status: ActivationUnknown, Detail: err.Error()}, nil
	}
	switch strings.ToLower(body.Status) {
	case "finished":
		return Activation{Status: ActivationActive, Detail: "deployment finished"}, nil
	case "failed", "cancelled", "canceled":
		return Activation{Status: ActivationFailed, Detail: "deployment " + strings.ToLower(body.Status)}, nil
	case "queued":
		return Activation{Status: ActivationQueued, Detail: "deployment queued"}, nil
	case "in_progress", "running":
		return Activation{Status: ActivationRunning, Detail: "deployment in progress"}, nil
	}
	return Activation{Status: ActivationUnknown, Detail: "provider status not recognised"}, nil
}

// --- Release index source --------------------------------------------------

// ReleaseIndex reads an operator-trusted HTTPS JSON document:
//
//	{"latest":"0.15.0",
//	 "releases":[{"version":"0.15.0","artifacts":[{"id":"ghcr.io/zadstack/gorun:0.15.0","digest":"sha256:…"}]}]}
//
// Artifact selects which artifact of a release to use (exact id, or a
// substring when the release lists several); a release with one artifact
// needs no selector.
type ReleaseIndex struct {
	url, token, artifact string
	client               *http.Client
}

// NewReleaseIndex validates the HTTPS URL (no credentials, no fragment).
func NewReleaseIndex(rawURL, token, artifact string, client *http.Client) (*ReleaseIndex, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("release source is not configured")
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &ReleaseIndex{url: u.String(), token: token, artifact: strings.TrimSpace(artifact), client: client}, nil
}

func (r *ReleaseIndex) Name() string { return "release-index" }

type releaseDoc struct {
	Latest   string `json:"latest"`
	Releases []struct {
		Version   string `json:"version"`
		Artifacts []struct {
			ID     string `json:"id"`
			Digest string `json:"digest"`
		} `json:"artifacts"`
	} `json:"releases"`
}

func (r *ReleaseIndex) fetch(ctx context.Context) (releaseDoc, error) {
	var doc releaseDoc
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return doc, err
	}
	req.Header.Set("Accept", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return doc, errors.New("release source could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return doc, fmt.Errorf("release source returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return doc, errors.New("release source returned an unreadable document")
	}
	return doc, nil
}

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (r *ReleaseIndex) Resolve(ctx context.Context, ver string) (Artifact, error) {
	doc, err := r.fetch(ctx)
	if err != nil {
		return Artifact{}, err
	}
	want := normalizeVersion(ver)
	for _, rel := range doc.Releases {
		if normalizeVersion(rel.Version) != want {
			continue
		}
		var chosen *Artifact
		for _, a := range rel.Artifacts {
			if a.ID == "" {
				continue
			}
			if len(rel.Artifacts) == 1 || r.artifact == "" || a.ID == r.artifact || strings.Contains(a.ID, r.artifact) {
				if chosen != nil && r.artifact == "" {
					return Artifact{}, errors.New("release lists several artifacts; configure which one to use")
				}
				c := Artifact{Version: want, ID: a.ID, Digest: strings.ToLower(strings.TrimSpace(a.Digest)), Source: r.Name()}
				chosen = &c
				if r.artifact != "" && a.ID == r.artifact {
					break
				}
			}
		}
		if chosen == nil {
			return Artifact{}, errors.New("release has no matching artifact")
		}
		if chosen.Digest != "" && !digestPattern.MatchString(chosen.Digest) {
			return Artifact{}, errors.New("release artifact digest is not a sha256 value")
		}
		return *chosen, nil
	}
	return Artifact{}, errors.New("release version not found in the trusted source")
}

func (r *ReleaseIndex) Latest(ctx context.Context) (Artifact, error) {
	doc, err := r.fetch(ctx)
	if err != nil {
		return Artifact{}, err
	}
	if doc.Latest == "" {
		return Artifact{}, errors.New("release source does not name a latest version")
	}
	if _, ok := parseVersion(doc.Latest); !ok {
		return Artifact{}, errors.New("release source latest version is not a release tag")
	}
	return r.Resolve(ctx, doc.Latest)
}

// NewLoopbackReleaseIndex is NewReleaseIndex for a plain-HTTP index served
// on the local host (a local supervisor); any other scheme/host is refused.
func NewLoopbackReleaseIndex(rawURL, token, artifact string, client *http.Client) (*ReleaseIndex, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("release source is not configured")
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return nil, errors.New("plain-HTTP release sources must be on the loopback address")
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &ReleaseIndex{url: u.String(), token: token, artifact: strings.TrimSpace(artifact), client: client}, nil
}

// DecodeArtifact reads a saved observation payload into a.
func DecodeArtifact(raw json.RawMessage, a *Artifact) bool {
	return len(raw) > 0 && json.Unmarshal(raw, a) == nil
}
