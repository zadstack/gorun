package commerce

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCallOpClaimBlocksConcurrentRemote(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var remoteCalls atomic.Int32
	upstream := e.billing.Config.Handler
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/suspend") {
			remoteCalls.Add(1)
			select {
			case <-entered:
			default:
				close(entered)
			}
			<-release
		}
		upstream.ServeHTTP(w, r)
	}))
	defer func() { unblock(); proxy.Close() }()
	srv, err := e.svc.saveServer(ctx, Server{Name: "ClaimRace", BaseURL: proxy.URL, ClientID: testClientID, Secret: testServerSecret})
	if err != nil {
		t.Fatal(err)
	}
	id := e.checkout(e.product("claim-race", srv), "claim@example.com")
	e.pay(id)
	if errs := e.drain(JobProvision); len(errs) > 0 {
		t.Fatal(errs)
	}
	entID := e.str(`SELECT id FROM entitlements WHERE order_id = ?`, id)
	ext := e.str(`SELECT external_id FROM entitlements WHERE id = ?`, entID)
	key := "suspend:" + entID + ":race"

	type result struct{ err error }
	out := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			_, err := e.svc.callOp(ctx, entID, "suspend", key, "POST", accountPath(ext, "suspend"),
				map[string]string{"reason": "admin"}, false, true)
			out <- result{err: err}
		}()
	}
	close(start)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("suspend did not start")
	}
	var second result
	select {
	case second = <-out:
	case <-time.After(2 * time.Second):
		t.Fatal("second caller did not return while first held the claim")
	}
	if second.err == nil || !strings.Contains(second.err.Error(), "operation in flight") {
		t.Fatalf("second caller: %v (want in flight)", second.err)
	}
	unblock()
	first := <-out
	if first.err != nil {
		t.Fatal(first.err)
	}
	if remoteCalls.Load() != 1 {
		t.Fatalf("remote calls = %d, want 1", remoteCalls.Load())
	}
	if st := e.str(`SELECT status FROM provisioning_ops WHERE idempotency_key = ?`, key); st != "done" {
		t.Fatalf("op status = %s", st)
	}
}
