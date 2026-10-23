package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/edgeprovision"
	"github.com/zadstack/gorun/internal/jobs"
)

const edgePlanPrefix = "edge.instance_plans."
const edgeProvisionJob = "edge.instance.create"
const edgePlanTTL = 15 * time.Minute

var errEdgePlan = errors.New("edge: invalid or unavailable creation plan")
var edgeWorkerPrefixPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,28}[a-z0-9])?$`)
var edgeSubdomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type edgeInstancePlan struct {
	ID               string                   `json:"id"`
	Request          edgeprovision.Request    `json:"request"`
	CFRevision       string                   `json:"cf_revision"`
	RegistryRevision string                   `json:"registry_revision"`
	ArtifactSHA      string                   `json:"artifact_sha"`
	ExpiresAt        string                   `json:"expires_at"`
	State            string                   `json:"state"`
	Step             string                   `json:"step,omitempty"`
	Resources        edgeprovision.Resources  `json:"resources"`
	Inspection       edgeprovision.Inspection `json:"inspection"`
	Fingerprint      string                   `json:"fingerprint"`
	JobID            string                   `json:"job_id,omitempty"`
	InstanceID       string                   `json:"instance_id,omitempty"`
	Error            string                   `json:"error,omitempty"`
}
type storedEdgePlan struct {
	Plan          edgeInstancePlan `json:"plan"`
	ManagerCipher string           `json:"manager_key_cipher"`
	ManagerID     string           `json:"manager_id"`
	CFTokenRaw    string           `json:"cf_token_raw"`
	CFAccountRaw  string           `json:"cf_account_raw"`
	raw           string
}

func edgePlanAAD(id string) string { return edgePlanPrefix + id + ".manager_key" }
func edgePlanCFRevision(s cfSnapshot) string {
	b, _ := json.Marshal(s.raw)
	v := sha256.Sum256(b)
	return hex.EncodeToString(v[:])
}
func edgePlanFingerprint(p edgeInstancePlan) string {
	p.Fingerprint = ""
	p.JobID = ""
	p.InstanceID = ""
	p.State = ""
	p.Step = ""
	p.Error = ""
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (h *handlers) edgeInstancePlan(ctx context.Context, id string) (storedEdgePlan, error) {
	if !edgeIDPattern.MatchString(id) {
		return storedEdgePlan{}, errEdgePlan
	}
	var raw string
	if e := h.d.DB.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", edgePlanPrefix+id).Scan(&raw); e != nil {
		return storedEdgePlan{}, errEdgePlan
	}
	var p storedEdgePlan
	if e := json.Unmarshal([]byte(raw), &p); e != nil || p.Plan.ID != id {
		return storedEdgePlan{}, errEdgePlan
	}
	p.raw = raw
	return p, nil
}
func edgePlanCFGuard(ctx context.Context, tx *sql.Tx, p storedEdgePlan) error {
	for key, expected := range map[string]string{settingCFToken: p.CFTokenRaw, settingCFAccount: p.CFAccountRaw} {
		var current string
		if e := tx.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", key).Scan(&current); e != nil || current != expected {
			return errEdgeConflict
		}
	}
	return nil
}
func edgePlanRegistryGuard(ctx context.Context, tx *sql.Tx, revision string) error {
	var raw string
	e := tx.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", settingEdgeInstances).Scan(&raw)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if (edgeInstancesSnapshot{raw: raw}).revision() != revision {
		return errEdgeConflict
	}
	return nil
}
func (h *handlers) edgePlanTx(ctx context.Context, tx *sql.Tx, p storedEdgePlan) error {
	var current string
	e := tx.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", edgePlanPrefix+p.Plan.ID).Scan(&current)
	if p.raw == "" {
		if !errors.Is(e, sql.ErrNoRows) {
			return errEdgeConflict
		}
	} else if e != nil || current != p.raw {
		return errEdgeConflict
	}
	return h.d.Settings.SetTx(ctx, tx, edgePlanPrefix+p.Plan.ID, p)
}
func (h *handlers) saveEdgePlan(ctx context.Context, p storedEdgePlan, guard bool) error {
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		if guard {
			if e := edgePlanCFGuard(ctx, tx, p); e != nil {
				return e
			}
			if p.Plan.State == "running" {
				if _, e := edgePlanTargetRegistry(ctx, tx, p); e != nil {
					return e
				}
			} else if e := edgePlanRegistryGuard(ctx, tx, p.Plan.RegistryRevision); e != nil {
				return e
			}
		}
		return h.edgePlanTx(ctx, tx, p)
	})
}

func (h *handlers) createEdgeInstancePlan(ctx context.Context, a edgeprovision.Adapter, artifact string, request edgeprovision.Request, registryRevision string) (edgeInstancePlan, error) {
	if a == nil || len(artifact) != 64 {
		return edgeInstancePlan{}, errEdgePlan
	}
	if _, e := hex.DecodeString(artifact); e != nil {
		return edgeInstancePlan{}, errEdgePlan
	}
	registry, e := h.edgeInstances(ctx)
	if e != nil {
		return edgeInstancePlan{}, e
	}
	if registryRevision == "" || registry.revision() != registryRevision {
		return edgeInstancePlan{}, errEdgeConflict
	}
	cf, e := h.cloudflareSnapshot(ctx)
	if e != nil {
		return edgeInstancePlan{}, e
	}
	if !cf.configured() || cf.AccountID == "" || cf.AccountID != request.AccountID {
		return edgeInstancePlan{}, errEdgePlan
	}
	request.Label = strings.TrimSpace(request.Label)
	request.Title = strings.TrimSpace(request.Title)
	if !edgeInstanceLabel(request.Label) || request.Title == "" || utf8.RuneCountInString(request.Title) > 200 || strings.ContainsAny(request.Title, "\r\n\x00<>") || !slugRe.MatchString(request.SiteID) || (request.Locale != "ar" && request.Locale != "en") || !edgeWorkerPrefixPattern.MatchString(request.Worker) || len(request.Worker) > 30 {
		return edgeInstancePlan{}, errEdgePlan
	}
	if len(registry.Records) >= maxEdgeInstances {
		return edgeInstancePlan{}, errEdgePlan
	}
	for _, r := range registry.Records {
		if r.Instance.SiteID == request.SiteID {
			return edgeInstancePlan{}, errEdgeConflict
		}
	}
	id := db.NewID()
	suffix := strings.ToLower(id[len(id)-10:])
	request.Worker = request.Worker + "-" + suffix
	resources := edgeprovision.Resources{Worker: request.Worker, D1Name: request.Worker + "-db", R2Bucket: request.Worker + "-media"}
	token, e := h.cloudflareToken(cf)
	if e != nil {
		return edgeInstancePlan{}, errEdgePlan
	}
	inspection, e := a.Inspect(ctx, token, request, resources)
	if e != nil || inspection.AccountID != request.AccountID || !inspection.WorkerAvailable || !inspection.D1Available || !inspection.BucketAvailable || !edgeSubdomainPattern.MatchString(inspection.WorkersSubdomain) {
		return edgeInstancePlan{}, errEdgePlan
	}
	// Inspection is read-only and cannot establish mutation permissions.
	inspection.WritePermissionsVerified = false
	origin, e := validateEdgeOrigin("https://" + resources.Worker + "." + inspection.WorkersSubdomain + ".workers.dev")
	if e != nil {
		return edgeInstancePlan{}, errEdgePlan
	}
	resources.Origin = origin
	keyBytes := make([]byte, 32)
	if _, e := rand.Read(keyBytes); e != nil {
		return edgeInstancePlan{}, e
	}
	key := base64.RawURLEncoding.EncodeToString(keyBytes)
	cipher, e := db.Encrypt(h.d.Config.SecretKey, []byte(key), edgePlanAAD(id))
	if e != nil {
		return edgeInstancePlan{}, e
	}
	p := storedEdgePlan{Plan: edgeInstancePlan{ID: id, Request: request, CFRevision: edgePlanCFRevision(cf), RegistryRevision: registryRevision, ArtifactSHA: artifact, ExpiresAt: time.Now().UTC().Add(edgePlanTTL).Format(time.RFC3339), State: "planned", Resources: resources, Inspection: inspection}, ManagerCipher: cipher, ManagerID: db.NewID(), CFTokenRaw: cf.raw[settingCFToken], CFAccountRaw: cf.raw[settingCFAccount]}
	p.Plan.Fingerprint = edgePlanFingerprint(p.Plan)
	if e := h.saveEdgePlan(ctx, p, true); e != nil {
		return edgeInstancePlan{}, e
	}
	h.d.Audit.Record(ctx, "edge.instance.plan", "edge_plan", id, map[string]any{"account_id": request.AccountID, "worker": request.Worker})
	return p.Plan, nil
}
func (h *handlers) approveEdgeInstancePlan(ctx context.Context, id, fingerprint string, approved bool, artifact string) (edgeInstancePlan, error) {
	p, e := h.edgeInstancePlan(ctx, id)
	if e != nil {
		return edgeInstancePlan{}, e
	}
	expiry, e := time.Parse(time.RFC3339, p.Plan.ExpiresAt)
	if !approved || fingerprint == "" || subtle.ConstantTimeCompare([]byte(p.Plan.Fingerprint), []byte(fingerprint)) != 1 || p.Plan.Fingerprint != edgePlanFingerprint(p.Plan) || p.Plan.State != "planned" || e != nil || !time.Now().Before(expiry) || p.Plan.ArtifactSHA != artifact {
		return edgeInstancePlan{}, errEdgePlan
	}
	p.Plan.State = "queued"
	e = db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		if e := edgePlanCFGuard(ctx, tx, p); e != nil {
			return e
		}
		if e := edgePlanRegistryGuard(ctx, tx, p.Plan.RegistryRevision); e != nil {
			return e
		}
		job, e := h.d.Jobs.EnqueueTx(ctx, tx, edgeProvisionJob, map[string]string{"plan_id": id}, jobs.Idempotency("edge-plan:"+id))
		if e != nil {
			return e
		}
		p.Plan.JobID = job
		return h.edgePlanTx(ctx, tx, p)
	})
	if e != nil {
		return edgeInstancePlan{}, e
	}
	h.d.Audit.Record(ctx, "edge.instance.approve", "edge_plan", id, map[string]any{"job_id": p.Plan.JobID, "worker": p.Plan.Resources.Worker})
	return p.Plan, nil
}

// registerEdgeInstanceProvisionJobs is called before queue startup by the admin
// module. Retries, including shutdown recovery, never repeat a running mutation.
func (h *handlers) registerEdgeInstanceProvisionJobs(a edgeprovision.Adapter, artifact string) {
	h.d.Jobs.Register(edgeProvisionJob, func(ctx context.Context, payload json.RawMessage) error {
		var v struct {
			PlanID string `json:"plan_id"`
		}
		if json.Unmarshal(payload, &v) != nil {
			return nil
		}
		return h.runEdgeInstancePlan(ctx, a, artifact, edgeHTTPClient, v.PlanID)
	}, 1)
}

func (h *handlers) edgePlanStop(p storedEdgePlan, state, code string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	current, e := h.edgeInstancePlan(ctx, p.Plan.ID)
	if e != nil {
		return e
	}
	if current.Plan.State == "verified" {
		return nil
	}
	if current.Plan.Resources.D1ID == "" && p.Plan.Resources.D1ID != "" {
		current.Plan.Resources.D1ID = p.Plan.Resources.D1ID
	}
	current.Plan.State = state
	current.Plan.Error = code
	return h.saveEdgePlan(ctx, current, false)
}
func (h *handlers) runEdgeInstancePlan(ctx context.Context, a edgeprovision.Adapter, artifact string, client *http.Client, id string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	p, e := h.edgeInstancePlan(ctx, id)
	if e != nil {
		return nil
	}
	if p.Plan.State == "running" {
		return h.edgePlanStop(p, "needs_reconciliation", "interrupted_stage")
	}
	if p.Plan.State != "queued" {
		return nil
	}
	expiry, e := time.Parse(time.RFC3339, p.Plan.ExpiresAt)
	if a == nil || p.Plan.ArtifactSHA != artifact || e != nil || !time.Now().Before(expiry) {
		return h.edgePlanStop(p, "stale", "approval_expired_or_artifact_changed")
	}
	p.Plan.State = "running"
	if e := h.saveEdgePlan(ctx, p, true); e != nil {
		if errors.Is(e, errEdgeConflict) {
			current, readErr := h.edgeInstancePlan(ctx, id)
			if readErr == nil && current.Plan.State != "queued" {
				return nil
			}
			return h.edgePlanStop(p, "stale", "connection_changed")
		}
		return e
	}
	p, _ = h.edgeInstancePlan(ctx, id)
	cf, e := h.cloudflareSnapshot(ctx)
	if e != nil {
		return h.edgePlanStop(p, "failed", "connection_unavailable")
	}
	token, e := h.cloudflareToken(cf)
	if e != nil {
		return h.edgePlanStop(p, "failed", "connection_unavailable")
	}
	key, e := db.Decrypt(h.d.Config.SecretKey, p.ManagerCipher, edgePlanAAD(id))
	if e != nil {
		return h.edgePlanStop(p, "failed", "manager_key_unavailable")
	}
	seed := edgeprovision.Seed{ManagerID: p.ManagerID, ManagerKeyHash: edgeprovision.HashManagerKey(p.Plan.Request.SiteID, string(key)), ManagerKeyHint: string(key[len(key)-4:])}
	checkpoint := func(step string) error {
		current, e := h.edgeInstancePlan(ctx, id)
		if e != nil {
			return e
		}
		if current.Plan.State != "running" {
			return errEdgeConflict
		}
		p.raw = current.raw
		p.Plan.Step = step
		return h.saveEdgePlan(ctx, p, true)
	}
	stages := []struct {
		name string
		run  func() error
	}{
		{"create_db", func() error {
			id, e := a.CreateDB(ctx, token, p.Plan.Request, p.Plan.Resources)
			if e == nil && id == "" {
				return errEdgePlan
			}
			p.Plan.Resources.D1ID = id
			return e
		}},
		{"create_bucket", func() error {
			name, e := a.CreateBucket(ctx, token, p.Plan.Request, p.Plan.Resources)
			if e == nil && name != p.Plan.Resources.R2Bucket {
				return errEdgePlan
			}
			return e
		}},
		{"apply_schema", func() error { return a.ApplySchema(ctx, token, p.Plan.Request, p.Plan.Resources, seed) }},
		{"upload_worker", func() error { return a.UploadWorker(ctx, token, p.Plan.Request, p.Plan.Resources, p.Plan.ArtifactSHA) }},
		{"enable_worker", func() error {
			origin, e := a.EnableWorker(ctx, token, p.Plan.Request, p.Plan.Resources)
			if e == nil && origin != p.Plan.Resources.Origin {
				return errEdgePlan
			}
			return e
		}},
	}
	for _, stage := range stages {
		if e := checkpoint(stage.name + "_running"); e != nil {
			return h.edgePlanStop(p, "needs_reconciliation", "checkpoint_failed")
		}
		if e := stage.run(); e != nil {
			return h.edgePlanStop(p, "needs_reconciliation", "provider_stage_unconfirmed")
		}
		if e := checkpoint(stage.name + "_done"); e != nil {
			return h.edgePlanStop(p, "needs_reconciliation", "checkpoint_failed")
		}
	}
	if e := checkpoint("verify_running"); e != nil {
		return h.edgePlanStop(p, "needs_reconciliation", "checkpoint_failed")
	}
	info, e := edgeWhoami(ctx, client, p.Plan.Resources.Origin, string(key))
	if e != nil || info.SiteID != p.Plan.Request.SiteID || info.ManagerID != p.ManagerID {
		return h.edgePlanStop(p, "needs_reconciliation", "manager_identity_unconfirmed")
	}
	if e := h.finishEdgeInstancePlan(ctx, p, key); e != nil {
		return h.edgePlanStop(p, "needs_reconciliation", "registration_unconfirmed")
	}
	h.d.Audit.Record(ctx, "edge.instance.created", "edge_plan", id, map[string]any{"origin": p.Plan.Resources.Origin, "site_id": info.SiteID})
	return nil
}
func (h *handlers) finishEdgeInstancePlan(ctx context.Context, p storedEdgePlan, key []byte) error {
	cipher, e := db.Encrypt(h.d.Config.SecretKey, key, edgeInstanceAAD(p.Plan.ID))
	if e != nil {
		return e
	}
	record := storedEdgeInstance{Instance: edgeInstance{ID: p.Plan.ID, Label: p.Plan.Request.Label, Origin: p.Plan.Resources.Origin, AccountID: p.Plan.Request.AccountID, Worker: p.Plan.Resources.Worker, SiteID: p.Plan.Request.SiteID, ManagerID: p.ManagerID, State: "connected", CheckedAt: time.Now().UTC().Format(time.RFC3339), KeySet: true}, Cipher: cipher}
	current, e := h.edgeInstancePlan(ctx, p.Plan.ID)
	if e != nil {
		return e
	}
	if current.Plan.State != "running" || current.Plan.Step != "verify_running" {
		return errEdgeConflict
	}
	p.raw = current.raw
	p.Plan.State = "verified"
	p.Plan.Step = "complete"
	p.Plan.InstanceID = p.Plan.ID
	p.ManagerCipher = ""
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		if e := edgePlanCFGuard(ctx, tx, p); e != nil {
			return e
		}
		s, e := edgePlanTargetRegistry(ctx, tx, p)
		if e != nil {
			return e
		}
		s.Records = append(s.Records, record)
		if e := h.d.Settings.SetTx(ctx, tx, settingEdgeInstances, s.Records); e != nil {
			return e
		}
		return h.edgePlanTx(ctx, tx, p)
	})
}

// Only the new site's identity/resources are fenced after approval. Unrelated
// sites may be checked or managed while paid provisioning is in flight.
func edgePlanTargetRegistry(ctx context.Context, tx *sql.Tx, p storedEdgePlan) (edgeInstancesSnapshot, error) {
	var raw string
	e := tx.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", settingEdgeInstances).Scan(&raw)
	s := edgeInstancesSnapshot{Records: []storedEdgeInstance{}}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return s, e
	}
	if e == nil {
		s, e = parseEdgeInstances(raw)
		if e != nil {
			return s, e
		}
	}
	if len(s.Records) >= maxEdgeInstances {
		return s, errEdgeConflict
	}
	for _, v := range s.Records {
		i := v.Instance
		if i.ID == p.Plan.ID || i.SiteID == p.Plan.Request.SiteID || i.Origin == p.Plan.Resources.Origin || (i.AccountID == p.Plan.Request.AccountID && i.Worker == p.Plan.Resources.Worker) {
			return s, errEdgeConflict
		}
	}
	return s, nil
}
