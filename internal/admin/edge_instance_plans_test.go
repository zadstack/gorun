package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/edgeprovision"
)

const planArtifactTest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type fakeEdgeProvision struct {
	calls      []string
	fail       string
	seed       edgeprovision.Seed
	request    edgeprovision.Request
	checkpoint func(string)
}

func (f *fakeEdgeProvision) stage(s string) error {
	if f.checkpoint != nil {
		f.checkpoint(s)
	}
	f.calls = append(f.calls, s)
	if f.fail == s {
		return errors.New("uncertain outcome with secret provider response")
	}
	return nil
}
func (f *fakeEdgeProvision) Inspect(_ context.Context, _ string, r edgeprovision.Request, _ edgeprovision.Resources) (edgeprovision.Inspection, error) {
	f.request = r
	return edgeprovision.Inspection{AccountID: r.AccountID, WorkersSubdomain: "demo-account", WorkerAvailable: true, D1Available: true, BucketAvailable: true, WritePermissionsVerified: true, RequiredPermissions: []string{"Workers Scripts:Edit", "D1:Edit", "R2:Edit"}}, nil
}
func (f *fakeEdgeProvision) CreateDB(context.Context, string, edgeprovision.Request, edgeprovision.Resources) (string, error) {
	return "generated-d1-id", f.stage("create_db")
}
func (f *fakeEdgeProvision) CreateBucket(_ context.Context, _ string, _ edgeprovision.Request, r edgeprovision.Resources) (string, error) {
	return r.R2Bucket, f.stage("create_bucket")
}
func (f *fakeEdgeProvision) ApplySchema(_ context.Context, _ string, _ edgeprovision.Request, _ edgeprovision.Resources, seed edgeprovision.Seed) error {
	f.seed = seed
	return f.stage("apply_schema")
}
func (f *fakeEdgeProvision) UploadWorker(context.Context, string, edgeprovision.Request, edgeprovision.Resources, string) error {
	return f.stage("upload_worker")
}
func (f *fakeEdgeProvision) EnableWorker(_ context.Context, _ string, _ edgeprovision.Request, r edgeprovision.Resources) (string, error) {
	return r.Origin, f.stage("enable_worker")
}
func planHandlers(t *testing.T) (*handlers, *fakeEdgeProvision) {
	t.Helper()
	h := edgeTestHandlers(t)
	cipher, e := db.Encrypt(h.d.Config.SecretKey, []byte(strings.Repeat("t", 32)), cfTokenAAD)
	if e != nil {
		t.Fatal(e)
	}
	if e := h.d.Settings.Set(context.Background(), settingCFToken, cipher); e != nil {
		t.Fatal(e)
	}
	if e := h.d.Settings.Set(context.Background(), settingCFAccount, "account-1"); e != nil {
		t.Fatal(e)
	}
	a := &fakeEdgeProvision{}
	h.registerEdgeInstanceProvisionJobs(a, planArtifactTest)
	return h, a
}
func makeEdgePlan(t *testing.T, h *handlers, a *fakeEdgeProvision, site string) edgeInstancePlan {
	t.Helper()
	p, e := h.createEdgeInstancePlan(context.Background(), a, planArtifactTest, edgeprovision.Request{Label: "Independent demo", Worker: "demo", SiteID: site, Title: "Empty Edge site", Locale: "ar", AccountID: "account-1"}, instanceRevision(t, h))
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func approvedEdgePlan(t *testing.T, h *handlers, a *fakeEdgeProvision) edgeInstancePlan {
	t.Helper()
	p := makeEdgePlan(t, h, a, "site-demo")
	q, e := h.approveEdgeInstancePlan(context.Background(), p.ID, p.Fingerprint, true, planArtifactTest)
	if e != nil {
		t.Fatal(e)
	}
	return q
}
func fakePlanIdentity(t *testing.T, a *fakeEdgeProvision) *http.Client {
	t.Helper()
	return &http.Client{Transport: instanceTransport(func(r *http.Request) (*http.Response, error) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		sum := sha256.Sum256([]byte("gorun-edge-manager:v1:" + a.request.SiteID + ":" + key))
		if len(key) != 43 || hex.EncodeToString(sum[:]) != a.seed.ManagerKeyHash || a.seed.ManagerKeyHint != key[len(key)-4:] {
			t.Error("verification did not use generated manager credential seeded into own D1")
		}
		if r.URL.Path != "/api/v1/manager" || !strings.HasSuffix(r.URL.Host, ".demo-account.workers.dev") {
			t.Error("verification reached unexpected endpoint")
		}
		return instanceResponse(a.request.SiteID, a.seed.ManagerID), nil
	})}
}

func TestEdgeInstancePlansPreviewAndApprovalFences(t *testing.T) {
	h, a := planHandlers(t)
	ctx := context.Background()
	p := makeEdgePlan(t, h, a, "site-demo")
	other := makeEdgePlan(t, h, a, "site-other")
	if p.Resources.Worker == other.Resources.Worker || p.Resources.D1Name == other.Resources.D1Name || p.Resources.R2Bucket == other.Resources.R2Bucket {
		t.Fatal("plans share resource names")
	}
	if len(a.calls) != 0 || p.Inspection.WritePermissionsVerified {
		t.Fatal("read-only plan pretended to verify mutation permissions")
	}
	raw, _ := json.Marshal(p)
	private, _ := h.edgeInstancePlan(ctx, p.ID)
	key, e := db.Decrypt(h.d.Config.SecretKey, private.ManagerCipher, edgePlanAAD(p.ID))
	if e != nil || len(key) != 43 {
		t.Fatal("plan credential not encrypted")
	}
	if strings.Contains(string(raw), private.ManagerCipher) || strings.Contains(string(raw), string(key)) {
		t.Fatal("public plan leaked secret")
	}
	for _, in := range []struct {
		fingerprint string
		approved    bool
	}{{"", true}, {"forged", true}, {p.Fingerprint, false}} {
		if _, e := h.approveEdgeInstancePlan(ctx, p.ID, in.fingerprint, in.approved, planArtifactTest); !errors.Is(e, errEdgePlan) {
			t.Fatalf("approval accepted: %v", e)
		}
	}
	if _, e := h.approveEdgeInstancePlan(ctx, p.ID, p.Fingerprint, true, strings.Repeat("f", 64)); !errors.Is(e, errEdgePlan) {
		t.Fatal("changed artifact approved")
	}
	h.d.Settings.Set(ctx, settingCFAccount, "account-2")
	if _, e := h.approveEdgeInstancePlan(ctx, p.ID, p.Fingerprint, true, planArtifactTest); !errors.Is(e, errEdgeConflict) {
		t.Fatalf("stale account: %v", e)
	}
	var count int
	h.d.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind=?", edgeProvisionJob).Scan(&count)
	if count != 0 {
		t.Fatal("invalid approval queued a job")
	}
}
func TestEdgeInstancePlanAtomicQueueAndVerifiedRegistration(t *testing.T) {
	h, a := planHandlers(t)
	ctx := context.Background()
	p := approvedEdgePlan(t, h, a)
	var payload string
	var attempts int
	if e := h.d.DB.QueryRow("SELECT payload_json,max_attempts FROM jobs WHERE id=?", p.JobID).Scan(&payload, &attempts); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(payload, "manager") || strings.Contains(payload, "token") || attempts != 1 {
		t.Fatal("unsafe job payload or retry policy")
	}
	if _, e := h.approveEdgeInstancePlan(ctx, p.ID, p.Fingerprint, true, planArtifactTest); !errors.Is(e, errEdgePlan) {
		t.Fatal("duplicate approval accepted")
	}
	a.checkpoint = func(stage string) {
		current, e := h.edgeInstancePlan(ctx, p.ID)
		if e != nil || current.Plan.Step != stage+"_running" {
			t.Fatal("mutation started before durable checkpoint")
		}
	}
	if e := h.runEdgeInstancePlan(ctx, a, planArtifactTest, fakePlanIdentity(t, a), p.ID); e != nil {
		t.Fatal(e)
	}
	done, _ := h.edgeInstancePlan(ctx, p.ID)
	registry, _ := h.edgeInstances(ctx)
	if done.Plan.State != "verified" || done.Plan.InstanceID != p.ID || done.ManagerCipher != "" || len(registry.Records) != 1 || registry.Records[0].Instance.State != "connected" {
		t.Fatalf("unverified or unregistered result: %+v", done.Plan)
	}
	if len(a.calls) != 5 || registry.Records[0].Instance.SiteID != p.Request.SiteID {
		t.Fatal("wrong site or stage count")
	}
	secret, e := db.Decrypt(h.d.Config.SecretKey, registry.Records[0].Cipher, edgeInstanceAAD(p.ID))
	sum := sha256.Sum256([]byte("gorun-edge-manager:v1:" + p.Request.SiteID + ":" + string(secret)))
	if e != nil || hex.EncodeToString(sum[:]) != a.seed.ManagerKeyHash {
		t.Fatal("registered credential differs from D1 seed")
	}
	if e := h.runEdgeInstancePlan(ctx, a, planArtifactTest, fakePlanIdentity(t, a), p.ID); e != nil || len(a.calls) != 5 {
		t.Fatal("completed plan repeated mutations")
	}
}
func TestEdgeInstancePlanUncertainStageNeverRetries(t *testing.T) {
	for _, stage := range []string{"create_db", "create_bucket", "apply_schema", "upload_worker", "enable_worker"} {
		t.Run(stage, func(t *testing.T) {
			h, a := planHandlers(t)
			ctx := context.Background()
			p := approvedEdgePlan(t, h, a)
			a.fail = stage
			if e := h.runEdgeInstancePlan(ctx, a, planArtifactTest, fakePlanIdentity(t, a), p.ID); e != nil {
				t.Fatal(e)
			}
			stopped, _ := h.edgeInstancePlan(ctx, p.ID)
			registry, _ := h.edgeInstances(ctx)
			if stopped.Plan.State != "needs_reconciliation" || len(registry.Records) != 0 || strings.Contains(stopped.Plan.Error, "secret") {
				t.Fatal("uncertain provider result presented as created or leaked error")
			}
			if stopped.Plan.Resources.D1ID != "generated-d1-id" {
				t.Fatal("known D1 ID lost at failure checkpoint")
			}
			calls := len(a.calls)
			a.fail = ""
			h.runEdgeInstancePlan(ctx, a, planArtifactTest, fakePlanIdentity(t, a), p.ID)
			if len(a.calls) != calls {
				t.Fatal("uncertain stage automatically retried")
			}
		})
	}
}
func TestEdgeInstancePlanInterruptedAndExpiredDoNotMutate(t *testing.T) {
	h, a := planHandlers(t)
	ctx := context.Background()
	p := approvedEdgePlan(t, h, a)
	stored, _ := h.edgeInstancePlan(ctx, p.ID)
	stored.Plan.State = "running"
	stored.Plan.Step = "create_db_running"
	if e := h.saveEdgePlan(ctx, stored, true); e != nil {
		t.Fatal(e)
	}
	h.runEdgeInstancePlan(ctx, a, planArtifactTest, fakePlanIdentity(t, a), p.ID)
	done, _ := h.edgeInstancePlan(ctx, p.ID)
	if done.Plan.State != "needs_reconciliation" || len(a.calls) != 0 {
		t.Fatal("recovered running mutation repeated")
	}
	h2, a2 := planHandlers(t)
	p2 := makeEdgePlan(t, h2, a2, "expired-site")
	s2, _ := h2.edgeInstancePlan(ctx, p2.ID)
	s2.Plan.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	s2.Plan.Fingerprint = edgePlanFingerprint(s2.Plan)
	h2.saveEdgePlan(ctx, s2, true)
	if _, e := h2.approveEdgeInstancePlan(ctx, p2.ID, s2.Plan.Fingerprint, true, planArtifactTest); !errors.Is(e, errEdgePlan) {
		t.Fatal("expired plan approved")
	}
}

func TestEdgeInstancePlanApprovalRollbackAndUnverifiedResult(t *testing.T) {
	h, a := planHandlers(t)
	ctx := context.Background()
	p := makeEdgePlan(t, h, a, "site-demo")
	if _, e := h.d.DB.Exec("CREATE TRIGGER reject_edge_job BEFORE INSERT ON jobs BEGIN SELECT RAISE(ABORT, 'synthetic queue failure'); END"); e != nil {
		t.Fatal(e)
	}
	if _, e := h.approveEdgeInstancePlan(ctx, p.ID, p.Fingerprint, true, planArtifactTest); e == nil {
		t.Fatal("queue failure accepted")
	}
	unchanged, e := h.edgeInstancePlan(ctx, p.ID)
	if e != nil || unchanged.Plan.State != "planned" || unchanged.Plan.JobID != "" {
		t.Fatal("queue failure left partial approval")
	}
	if _, e := h.d.DB.Exec("DROP TRIGGER reject_edge_job"); e != nil {
		t.Fatal(e)
	}
	if _, e := h.approveEdgeInstancePlan(ctx, p.ID, p.Fingerprint, true, planArtifactTest); e != nil {
		t.Fatal(e)
	}
	bad := &http.Client{Transport: instanceTransport(func(r *http.Request) (*http.Response, error) {
		return instanceResponse("wrong-site", "wrong-manager"), nil
	})}
	if e := h.runEdgeInstancePlan(ctx, a, planArtifactTest, bad, p.ID); e != nil {
		t.Fatal(e)
	}
	stopped, _ := h.edgeInstancePlan(ctx, p.ID)
	registry, _ := h.edgeInstances(ctx)
	if stopped.Plan.State != "needs_reconciliation" || len(registry.Records) != 0 || len(a.calls) != 5 {
		t.Fatal("resource creation mistaken for verified identity")
	}
}

func TestEdgeInstancePlanHTTPPublicAndStrictJSON(t *testing.T) {
	h, a := planHandlers(t)
	p := makeEdgePlan(t, h, a, "site-demo")
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "admin", Role: "admin", Status: auth.StatusActive})
	w := httptest.NewRecorder()
	h.edgeInstancePlansList(w, httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	stored, _ := h.edgeInstancePlan(ctx, p.ID)
	key, e := db.Decrypt(h.d.Config.SecretKey, stored.ManagerCipher, edgePlanAAD(p.ID))
	if e != nil {
		t.Fatal(e)
	}
	if w.Code != 200 || strings.Contains(w.Body.String(), stored.ManagerCipher) || strings.Contains(w.Body.String(), string(key)) || strings.Contains(w.Body.String(), "token_raw") {
		t.Fatal("public plan leaked private binding")
	}
	for _, fn := range []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, r *http.Request) {
		h.edgeInstancePlanCreateWithAdapter(w, r, a, planArtifactTest)
	}, func(w http.ResponseWriter, r *http.Request) {
		h.edgeInstancePlanApproveWithArtifact(w, r, planArtifactTest)
	}} {
		w := httptest.NewRecorder()
		fn(w, httptest.NewRequest("POST", "/", strings.NewReader(`{} {}`)).WithContext(ctx))
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil || w.Code != 400 || body.Error.Code != "invalid_request" || len(a.calls) != 0 {
			t.Fatal("plan trailing JSON accepted")
		}
	}
}

func TestEdgeInstancePlanChangedConnectionStopsBeforeNextMutation(t *testing.T) {
	h, a := planHandlers(t)
	ctx := context.Background()
	p := approvedEdgePlan(t, h, a)
	a.checkpoint = func(stage string) {
		if stage == "create_db" {
			if e := h.d.Settings.Set(ctx, settingCFAccount, "changed-account"); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := h.runEdgeInstancePlan(ctx, a, planArtifactTest, fakePlanIdentity(t, a), p.ID); e != nil {
		t.Fatal(e)
	}
	stopped, _ := h.edgeInstancePlan(ctx, p.ID)
	s, _ := h.edgeInstances(ctx)
	if len(a.calls) != 1 || a.calls[0] != "create_db" || stopped.Plan.State != "needs_reconciliation" || stopped.Plan.Resources.D1ID != "generated-d1-id" || len(s.Records) != 0 {
		t.Fatal("changed connection crossed durable mutation fence")
	}
}

func TestEdgeInstancePlanPreservesIndependentRegistryChanges(t *testing.T) {
	h, a := planHandlers(t)
	ctx := context.Background()
	seen := []string{}
	client := instanceClient(t, &seen)
	existing := connectInstance(t, h, client, "a.example", strings.Repeat("a", 43))
	p := approvedEdgePlan(t, h, a)
	var afterCheck storedEdgeInstance
	a.checkpoint = func(stage string) {
		if stage == "create_db" {
			if _, _, e := h.checkEdgeInstance(ctx, client, existing.ID, instanceRevision(t, h)); e != nil {
				t.Fatal(e)
			}
			s, _ := h.edgeInstances(ctx)
			afterCheck = s.Records[0]
		}
	}
	identity := fakePlanIdentity(t, a)
	verify := &http.Client{Transport: instanceTransport(func(r *http.Request) (*http.Response, error) {
		// A second independent connection lands after verify_running but before final insert.
		connectInstance(t, h, client, "b.example", strings.Repeat("b", 43))
		return identity.Transport.RoundTrip(r)
	})}
	if e := h.runEdgeInstancePlan(ctx, a, planArtifactTest, verify, p.ID); e != nil {
		t.Fatal(e)
	}
	done, _ := h.edgeInstancePlan(ctx, p.ID)
	s, _ := h.edgeInstances(ctx)
	if done.Plan.State != "verified" || len(a.calls) != 5 || len(s.Records) != 3 {
		t.Fatalf("independent change aborted creation: %+v", done.Plan)
	}
	if s.Records[0] != afterCheck || s.Records[1].Instance.SiteID != "site-b" || s.Records[2].Instance.SiteID != "site-demo" {
		t.Fatal("final insert overwrote independent checked/connected records")
	}
}

func TestEdgeInstancePlanTargetCollisionStopsFurtherMutation(t *testing.T) {
	h, a := planHandlers(t)
	ctx := context.Background()
	p := approvedEdgePlan(t, h, a)
	a.checkpoint = func(stage string) {
		if stage == "create_db" {
			client := &http.Client{Transport: instanceTransport(func(r *http.Request) (*http.Response, error) {
				return instanceResponse("site-demo", "other-manager"), nil
			})}
			connectInstance(t, h, client, "collision.example", strings.Repeat("c", 43))
		}
	}
	if e := h.runEdgeInstancePlan(ctx, a, planArtifactTest, fakePlanIdentity(t, a), p.ID); e != nil {
		t.Fatal(e)
	}
	done, _ := h.edgeInstancePlan(ctx, p.ID)
	s, _ := h.edgeInstances(ctx)
	if done.Plan.State != "needs_reconciliation" || len(a.calls) != 1 || len(s.Records) != 1 || s.Records[0].Instance.ManagerID != "other-manager" {
		t.Fatal("target collision crossed mutation fence or was overwritten")
	}
}
