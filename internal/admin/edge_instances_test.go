package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/observe"
)

type instanceTransport func(*http.Request) (*http.Response, error)

func (f instanceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func instanceResponse(site, manager string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"data":{"site_id":"` + site + `","manager_id":"` + manager + `","label":"Independent site","primary":true}}`)), Header: make(http.Header)}
}
func instanceClient(t *testing.T, seen *[]string) *http.Client {
	t.Helper()
	return &http.Client{Transport: instanceTransport(func(r *http.Request) (*http.Response, error) {
		*seen = append(*seen, r.URL.Host+":"+r.Header.Get("Authorization"))
		switch r.URL.Host {
		case "a.example":
			if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 43) {
				t.Error("site A received wrong credential")
			}
			return instanceResponse("site-a", "manager-a"), nil
		case "b.example":
			if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("b", 43) {
				t.Error("site B received wrong credential")
			}
			return instanceResponse("site-b", "manager-b"), nil
		}
		t.Fatalf("unexpected remote origin %s", r.URL.Host)
		return nil, errors.New("unexpected")
	})}
}
func instanceRevision(t *testing.T, h *handlers) string {
	t.Helper()
	s, e := h.edgeInstances(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return s.revision()
}
func connectInstance(t *testing.T, h *handlers, c *http.Client, host, key string) edgeInstance {
	t.Helper()
	v, _, e := h.connectEdgeInstance(context.Background(), c, edgeInstanceInput{Label: "Site " + host, Origin: "https://" + host, Key: key, Revision: instanceRevision(t, h)})
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestEdgeInstancesIndependentCredentialsAndLegacyPreserved(t *testing.T) {
	h := edgeTestHandlers(t)
	ctx := context.Background()
	legacy := storeEdgeConnection(t, h, "https://legacy.example", strings.Repeat("l", 43))
	seen := []string{}
	client := instanceClient(t, &seen)
	a := connectInstance(t, h, client, "a.example", strings.Repeat("a", 43))
	b := connectInstance(t, h, client, "b.example", strings.Repeat("b", 43))
	before, e := h.edgeInstances(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if len(before.Records) != 2 || a.ID == b.ID {
		t.Fatal("independent records missing")
	}
	for _, v := range before.Records {
		plain, e := db.Decrypt(h.d.Config.SecretKey, v.Cipher, edgeInstanceAAD(v.Instance.ID))
		if e != nil || len(plain) != 43 {
			t.Fatal("credential not stored encrypted for its instance")
		}
		other := a.ID
		if other == v.Instance.ID {
			other = b.ID
		}
		if _, e := db.Decrypt(h.d.Config.SecretKey, v.Cipher, edgeInstanceAAD(other)); e == nil {
			t.Fatal("cross-instance ciphertext accepted")
		}
	}
	public, _ := json.Marshal(before.public())
	if strings.Contains(string(public), "cipher") || strings.Contains(string(public), strings.Repeat("a", 43)) || strings.Contains(string(public), strings.Repeat("b", 43)) {
		t.Fatal("public projection leaked credentials")
	}
	checked, _, e := h.checkEdgeInstance(ctx, client, a.ID, before.revision())
	if e != nil || checked.State != "connected" {
		t.Fatalf("check: %v", e)
	}
	after, _ := h.edgeInstances(ctx)
	if after.Records[1] != before.Records[1] {
		t.Fatal("checking A changed B")
	}
	if len(seen) != 3 || seen[2] != "a.example:Bearer "+strings.Repeat("a", 43) {
		t.Fatal("check used another instance credential")
	}
	old, _ := h.currentEdge(ctx)
	if old.Cipher != legacy || old.Page.Origin != "https://legacy.example" {
		t.Fatal("registry changed legacy single binding")
	}
}

func TestEdgeInstancesFenceBeforeIOAndNoLostUpdate(t *testing.T) {
	h := edgeTestHandlers(t)
	ctx := context.Background()
	seen := []string{}
	client := instanceClient(t, &seen)
	oldRevision := instanceRevision(t, h)
	a := connectInstance(t, h, client, "a.example", strings.Repeat("a", 43))
	for _, revision := range []string{"", "forged", oldRevision} {
		_, _, e := h.connectEdgeInstance(ctx, client, edgeInstanceInput{Label: "B", Origin: "https://b.example", Key: strings.Repeat("b", 43), Revision: revision})
		if !errors.Is(e, errEdgeConflict) {
			t.Fatalf("revision %q: %v", revision, e)
		}
		if _, _, e := h.checkEdgeInstance(ctx, client, a.ID, revision); !errors.Is(e, errEdgeConflict) {
			t.Fatalf("check fence: %v", e)
		}
	}
	if len(seen) != 1 {
		t.Fatal("invalid approval fingerprint caused network I/O")
	}
	s, _ := h.edgeInstances(ctx)
	racing := &http.Client{Transport: instanceTransport(func(*http.Request) (*http.Response, error) {
		current, _ := h.edgeInstances(ctx)
		current.Records[0].Instance.Label = "Concurrent operator edit"
		if e := h.saveEdgeInstances(ctx, current, current.Records); e != nil {
			t.Fatal(e)
		}
		return instanceResponse("site-b", "manager-b"), nil
	})}
	if _, _, e := h.connectEdgeInstance(ctx, racing, edgeInstanceInput{Label: "B", Origin: "https://b.example", Key: strings.Repeat("b", 43), Revision: s.revision()}); !errors.Is(e, errEdgeConflict) {
		t.Fatalf("race: %v", e)
	}
	current, _ := h.edgeInstances(ctx)
	if len(current.Records) != 1 || current.Records[0].Instance.Label != "Concurrent operator edit" {
		t.Fatal("lost concurrent update")
	}
}

func TestEdgeInstancesRejectUntrustedOriginsAndUnverifiedConnections(t *testing.T) {
	h := edgeTestHandlers(t)
	ctx := context.Background()
	calls := 0
	c := &http.Client{Transport: instanceTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("private provider details and secret material")
	})}
	for _, origin := range []string{"http://a.example", "https://localhost", "https://127.0.0.1", "https://192.168.1.1", "https://a.internal", "https://user:pass@a.example", "https://a.example/?query=secret", "https://a.example/path"} {
		if _, _, e := h.connectEdgeInstance(ctx, c, edgeInstanceInput{Label: "Test", Origin: origin, Key: strings.Repeat("a", 43), Revision: instanceRevision(t, h)}); !errors.Is(e, errEdgeOrigin) {
			t.Fatalf("origin %s: %v", origin, e)
		}
	}
	if calls != 0 {
		t.Fatal("rejected origin reached client")
	}
	if _, _, e := h.connectEdgeInstance(ctx, c, edgeInstanceInput{Label: "Test", Origin: "https://a.example", Key: strings.Repeat("a", 43), Revision: instanceRevision(t, h)}); !errors.Is(e, errEdgeCheck) {
		t.Fatal("unverified manager accepted")
	}
	s, _ := h.edgeInstances(ctx)
	if len(s.Records) != 0 || s.present {
		t.Fatal("failed connection wrote a binding")
	}
}

func TestEdgeInstancesIdentityMismatchAndSafeFailureState(t *testing.T) {
	h := edgeTestHandlers(t)
	ctx := context.Background()
	seen := []string{}
	a := connectInstance(t, h, instanceClient(t, &seen), "a.example", strings.Repeat("a", 43))
	before, _ := h.edgeInstances(ctx)
	wrong := &http.Client{Transport: instanceTransport(func(*http.Request) (*http.Response, error) {
		return instanceResponse("different-site", "different-manager"), nil
	})}
	if _, _, e := h.connectEdgeInstance(ctx, wrong, edgeInstanceInput{ID: a.ID, Label: "Overwrite", Origin: a.Origin, Key: strings.Repeat("b", 43), Revision: before.revision()}); !errors.Is(e, errEdgeConflict) {
		t.Fatal("different identity replaced existing site")
	}
	after, _ := h.edgeInstances(ctx)
	if after.raw != before.raw {
		t.Fatal("rejected identity wrote registry")
	}
	failed := &http.Client{Transport: instanceTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("secret=" + strings.Repeat("a", 43))
	})}
	item, _, e := h.checkEdgeInstance(ctx, failed, a.ID, after.revision())
	if e != nil || item.State != "error" || item.Error != "manager_identity_check_failed" {
		t.Fatalf("unsafe failure state: %+v %v", item, e)
	}
	current, _ := h.edgeInstances(ctx)
	if current.Records[0].Cipher != before.Records[0].Cipher || current.Records[0].Instance.SiteID != a.SiteID {
		t.Fatal("failed check destroyed credential/identity")
	}
	public, _ := json.Marshal(current.public())
	if strings.Contains(string(public), strings.Repeat("a", 43)) {
		t.Fatal("remote error leaked secret")
	}
	r := httptest.NewRequest("GET", "/api/v1/site/edge/instances", nil)
	w := httptest.NewRecorder()
	h.edgeInstancesList(w, r)
	if w.Code != 403 {
		t.Fatal("registry list exposed without permission")
	}
}

func TestEdgeInstancesDuplicateAndAssociationCannotOverwrite(t *testing.T) {
	h := edgeTestHandlers(t)
	ctx := context.Background()
	seen := []string{}
	client := instanceClient(t, &seen)
	connectInstance(t, h, client, "a.example", strings.Repeat("a", 43))
	before, _ := h.edgeInstances(ctx)
	if _, _, err := h.connectEdgeInstance(ctx, client, edgeInstanceInput{Label: "Duplicate", Origin: "https://a.example", Key: strings.Repeat("a", 43), Revision: before.revision()}); !errors.Is(err, errEdgeConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	for _, input := range []edgeInstanceInput{{AccountID: "unowned-account", Worker: "another-worker"}, {AccountID: "unowned-account"}, {Worker: "another-worker"}} {
		input.Label = "Claim"
		input.Origin = "https://b.example"
		input.Key = strings.Repeat("b", 43)
		input.Revision = before.revision()
		count := len(seen)
		if _, _, err := h.connectEdgeInstance(ctx, client, input); !errors.Is(err, errEdgeSettings) {
			t.Fatalf("unverified association: %v", err)
		}
		if len(seen) != count {
			t.Fatal("unverified association caused network I/O")
		}
	}
	after, _ := h.edgeInstances(ctx)
	if after.raw != before.raw {
		t.Fatal("rejected duplicate or account association changed registry")
	}
}

func TestEdgeInstancesHTTPViewsNeverExposeCipherAndDenyMutations(t *testing.T) {
	h := edgeTestHandlers(t)
	seen := []string{}
	client := instanceClient(t, &seen)
	connectInstance(t, h, client, "a.example", strings.Repeat("a", 43))
	r := httptest.NewRequest("GET", "/api/v1/site/edge/instances", nil).WithContext(auth.WithUser(context.Background(), &auth.User{ID: "admin", Status: auth.StatusActive, Role: "admin"}))
	w := httptest.NewRecorder()
	h.edgeInstancesList(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "manager_key") || strings.Contains(w.Body.String(), strings.Repeat("a", 43)) {
		t.Fatal("API list leaked credential")
	}
	var view struct {
		OK       bool           `json:"ok"`
		Data     []edgeInstance `json:"data"`
		Revision string         `json:"revision"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || !view.OK || len(view.Data) != 1 || len(view.Revision) != 64 {
		t.Fatal("invalid public list envelope")
	}
	for _, action := range []func(http.ResponseWriter, *http.Request, *http.Client){h.edgeInstancesConnectWithClient, h.edgeInstancesCheckWithClient} {
		count := len(seen)
		w := httptest.NewRecorder()
		action(w, httptest.NewRequest("POST", "/api/v1/site/edge/instances", strings.NewReader(`{"key":"private"}`)), client)
		if w.Code != 403 || len(seen) != count {
			t.Fatal("unauthorized mutation was allowed or made I/O")
		}
	}
}

func TestEdgeInstancesRejectTrailingJSONWithoutIO(t *testing.T) {
	h := edgeTestHandlers(t)
	seen := []string{}
	client := instanceClient(t, &seen)
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "admin", Status: auth.StatusActive, Role: "admin"})
	for _, fn := range []func(http.ResponseWriter, *http.Request, *http.Client){h.edgeInstancesConnectWithClient, h.edgeInstancesCheckWithClient} {
		w := httptest.NewRecorder()
		fn(w, httptest.NewRequest("POST", "/", strings.NewReader(`{} {}`)).WithContext(ctx), client)
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil || w.Code != 400 || body.Error.Code != "invalid_request" || len(seen) != 0 {
			t.Fatalf("trailing JSON accepted or malformed error: %s", w.Body.String())
		}
	}
}

func TestEdgeInstancesInventoryDoesNotProveOriginOwnership(t *testing.T) {
	h, _ := integrationsHandlers(t)
	storeCloudflare(t, h, strings.Repeat("t", 32), "account-1")
	ctx := context.Background()
	cf, e := h.cloudflareSnapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e := h.observe.Record(ctx, kindCFInventory, "workers", cf.revision(true), observe.Result{Data: cfInventoryResult{Kind: "workers", Items: []cfResource{{Name: "worker-a"}}, Count: 1, Complete: true}}, nil, 0); e != nil {
		t.Fatal(e)
	}
	if !h.cloudflareResourceDiscoveredFor(ctx, cf, "workers", "worker-a") {
		t.Fatal("fixture has no discovered Worker")
	}
	seen := []string{}
	client := instanceClient(t, &seen)
	if _, _, e := h.connectEdgeInstance(ctx, client, edgeInstanceInput{Label: "Unrelated B", Origin: "https://b.example", Key: strings.Repeat("b", 43), AccountID: "account-1", Worker: "worker-a", Revision: instanceRevision(t, h)}); !errors.Is(e, errEdgeSettings) || len(seen) != 0 {
		t.Fatal("inventory accepted as origin ownership evidence")
	}
	s, _ := h.edgeInstances(ctx)
	if len(s.Records) != 0 {
		t.Fatal("false association persisted")
	}
	connectInstance(t, h, client, "b.example", strings.Repeat("b", 43))
	s, _ = h.edgeInstances(ctx)
	if len(s.Records) != 1 || s.Records[0].Instance.AccountID != "" || s.Records[0].Instance.Worker != "" {
		t.Fatal("identity-only connect retained unproven provider association")
	}
}
