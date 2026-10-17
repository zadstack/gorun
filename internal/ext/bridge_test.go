package ext_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/ext"
)

func TestActivateSubscribesAndDeliversEventSigned(t *testing.T) {
	e := boot(t)
	h, rec, url := helloServer(t)
	res := e.install(manifest("hello", url, hook("content.published", "event", 0), hook("content.after_save", "action", 0)))
	h.SetSecret(res.Secret)
	if err := e.svc.Activate(e.ctx, "hello"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if x := e.status("hello"); x.Status != ext.StatusActive {
		t.Fatalf("status = %s", x.Status)
	}
	it, err := e.content.Create(e.ctx, content.Input{Type: "post", Title: "Hello world", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	// hello only records deliveries whose signature verified.
	eventually(t, "signed event and action delivered", func() bool {
		var ev, act bool
		for _, d := range h.Events() {
			ev = ev || (d.Hook == "content.published" && d.Kind == "event")
			act = act || (d.Hook == "content.after_save" && d.Kind == "action")
		}
		return ev && act
	})
	for _, d := range h.Events() {
		var p struct {
			ID string `json:"id"`
		}
		json.Unmarshal(d.Payload, &p)
		if p.ID != it.ID {
			t.Fatalf("payload id = %q want %q (%s)", p.ID, it.ID, d.Payload)
		}
		if d.DeliveryID == "" || d.TS == 0 {
			t.Fatalf("delivery = %+v", d)
		}
		var site struct {
			BaseURL string `json:"base_url"`
		}
		json.Unmarshal(d.Site, &site)
		if site.BaseURL != "http://gorun.test" {
			t.Fatalf("site = %s", d.Site)
		}
	}
	rec.mu.Lock()
	hdr, body := rec.headers[0], rec.bodies[0]
	rec.mu.Unlock()
	if hdr.Get(ext.HeaderExtension) != "hello" {
		t.Fatalf("extension header = %q", hdr.Get(ext.HeaderExtension))
	}
	ts := hdr.Get(ext.HeaderTimestamp)
	if err := ext.Verify([]byte(res.Secret), ts, body, hdr.Get(ext.HeaderSignature), time.Now(), time.Minute); err != nil {
		t.Fatalf("signature: %v", err)
	}
	var envl struct {
		TS int64 `json:"ts"`
	}
	json.Unmarshal(body, &envl)
	if strconv.FormatInt(envl.TS, 10) != ts {
		t.Fatalf("body ts %d != header %s", envl.TS, ts)
	}
	eventually(t, "delivery log", func() bool {
		ds, _ := e.svc.Deliveries(context.Background(), "hello", 50)
		return len(ds) >= 2 && ds[0].Status == "ok"
	})
}

func TestFilterRoundtripReplacesValue(t *testing.T) {
	e := boot(t)
	h, _, url := helloServer(t)
	res := e.install(manifest("hello", url, hook("content.before_save", "filter", 2000)))
	h.SetSecret(res.Secret)
	if err := e.svc.Activate(e.ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	it, err := e.content.Create(e.ctx, content.Input{Type: "post", Title: "Hi", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if it.Title != "Hi — via hello" {
		t.Fatalf("title = %q", it.Title)
	}
	ds, _ := e.svc.Deliveries(context.Background(), "hello", 50)
	if len(ds) != 1 || ds[0].Status != "ok" || ds[0].Kind != "filter" {
		t.Fatalf("deliveries = %+v", ds)
	}
}

func TestFilterTimeoutKeepsOriginal(t *testing.T) {
	e := boot(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(1500 * time.Millisecond):
		}
		w.Write([]byte(`{"value":{"title":"too late"}}`))
	}))
	t.Cleanup(slow.Close)
	e.install(manifest("slow", slow.URL, hook("content.before_save", "filter", 100)))
	if err := e.svc.Activate(e.ctx, "slow"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	it, err := e.content.Create(e.ctx, content.Input{Type: "post", Title: "Original", Status: "draft"})
	if err != nil {
		t.Fatalf("a failing filter must not fail the save: %v", err)
	}
	if it.Title != "Original" {
		t.Fatalf("title = %q", it.Title)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("timeout not honored: %s", d)
	}
	x := e.status("slow")
	if x.Failures != 1 || x.LastError == "" {
		t.Fatalf("failure not counted: %+v", x)
	}
	ds, _ := e.svc.Deliveries(context.Background(), "slow", 50)
	if len(ds) != 1 || ds[0].Status != "timeout" {
		t.Fatalf("deliveries = %+v", ds)
	}
}

func TestConsecutiveFailuresMarkError(t *testing.T) {
	e := boot(t)
	var calls atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(bad.Close)
	e.install(manifest("flaky", bad.URL, hook("content.before_save", "filter", 1000)))
	if err := e.svc.Activate(e.ctx, "flaky"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 22; i++ {
		if _, err := e.content.Create(e.ctx, content.Input{Type: "post", Title: "t" + strconv.Itoa(i), Status: "draft"}); err != nil {
			t.Fatal(err)
		}
	}
	x := e.status("flaky")
	if x.Status != ext.StatusError || calls.Load() != 20 {
		t.Fatalf("after 20 failures status=%s calls=%d", x.Status, calls.Load())
	}
	// Re-activation resets the counter.
	if err := e.svc.Activate(e.ctx, "flaky"); err != nil {
		t.Fatal(err)
	}
	if x := e.status("flaky"); x.Status != ext.StatusActive || x.Failures != 0 {
		t.Fatalf("reactivate: %+v", x)
	}
}

func TestDeactivateStopsDelivery(t *testing.T) {
	e := boot(t)
	h, rec, url := helloServer(t)
	res := e.install(manifest("hello", url, hook("content.before_save", "filter", 2000), hook("content.after_save", "action", 0)))
	h.SetSecret(res.Secret)
	if err := e.svc.Activate(e.ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Deactivate(e.ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if x := e.status("hello"); x.Status != ext.StatusDisabled {
		t.Fatalf("status = %s", x.Status)
	}
	it, err := e.content.Create(e.ctx, content.Input{Type: "post", Title: "Quiet", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if it.Title != "Quiet" {
		t.Fatalf("deactivated filter still applied: %q", it.Title)
	}
	time.Sleep(2500 * time.Millisecond) // two worker polls: nothing may arrive
	if n := rec.count(); n != 0 {
		t.Fatalf("deactivated extension received %d deliveries", n)
	}
	// Re-activating resumes delivery exactly once per hook (no duplicate
	// subscriptions from the earlier activation).
	if err := e.svc.Activate(e.ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	it, err = e.content.Create(e.ctx, content.Input{Type: "post", Title: "Loud", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if it.Title != "Loud — via hello" {
		t.Fatalf("title = %q (want exactly one suffix)", it.Title)
	}
	eventually(t, "action after reactivation", func() bool { return len(h.Events()) == 1 })
	time.Sleep(1500 * time.Millisecond)
	if n := len(h.Events()); n != 1 || strings.Count(it.Title, "via hello") != 1 {
		t.Fatalf("events after reactivation = %d", n)
	}
}

func TestRestartResubscribesActiveExtensions(t *testing.T) {
	dir := t.TempDir()
	h, _, url := helloServer(t)
	e := bootAt(t, dir, true)
	res := e.install(manifest("hello", url, hook("content.before_save", "filter", 2000)))
	h.SetSecret(res.Secret)
	if err := e.svc.Activate(e.ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	e.app.Close()

	e2 := bootAt(t, dir, false)
	it, err := e2.content.Create(e2.ctx, content.Input{Type: "post", Title: "Again", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if it.Title != "Again — via hello" {
		t.Fatalf("active extension not resubscribed at boot: %q", it.Title)
	}
}
