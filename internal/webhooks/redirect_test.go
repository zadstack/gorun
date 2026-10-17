package webhooks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A webhook endpoint that redirects is a failed delivery: the signed body
// is never resent to the redirect target, and it is not retried.
func TestDeliveryRefusesRedirects(t *testing.T) {
	var landed atomic.Int32
	dst := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { landed.Add(1) }))
	defer dst.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dst.URL+"/landed", http.StatusTemporaryRedirect)
	}))
	defer src.Close()
	s, _ := testService(t)
	s.allowPrivate = true
	s.send = httpSender(deliveryClient(true))
	ctx := context.Background()
	hook, _, err := s.Create(ctx, Input{Name: strp("r"), URL: strp(src.URL + "/in"), Events: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.dispatch(ctx, "menus.saved", map[string]string{"id": "m"}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Deliveries(ctx, hook.ID, 1)
	d := list[0]
	if err := s.attempt(ctx, &d, false, false); err != nil {
		t.Fatalf("a refused redirect is final, not retried: %v", err)
	}
	got, _ := s.delivery(ctx, d.ID)
	if got.Status != "failed" || !strings.Contains(got.Error, "redirect") {
		t.Fatalf("delivery: %+v", got)
	}
	if landed.Load() != 0 {
		t.Fatal("the signed body must not reach the redirect target")
	}
}

// Network errors are recorded without the request URL, whose query string
// may carry a token.
func TestDeliveryErrorOmitsURLQuery(t *testing.T) {
	s, _ := testService(t)
	s.send = httpSender(deliveryClient(false))
	ctx := context.Background()
	hook, _, err := s.Create(ctx, Input{Name: strp("t"), URL: strp("https://127.0.0.1/h?token=s3cret"), Events: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.dispatch(ctx, "menus.saved", map[string]string{"id": "m"}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Deliveries(ctx, hook.ID, 1)
	d := list[0]
	_ = s.attempt(ctx, &d, false, false)
	got, _ := s.delivery(ctx, d.ID)
	if strings.Contains(got.Error, "s3cret") || !strings.Contains(got.Error, "blocked") {
		t.Fatalf("delivery error: %q", got.Error)
	}
}
