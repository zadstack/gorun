package ext

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestInFlightResponseCannotChangeReactivatedExtension(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, ctx, _ := bootInternal(t)
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					close(started)
					<-release
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"value":{"title":"Response"}}`))
			}))
			t.Cleanup(server.Close)
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			raw := `{"slug":"slow","name":{"en":"Slow"},"version":"1.0.0","permissions":[],` +
				`"hooks":[{"name":"content.before_save","kind":"filter","timeout_ms":10000}],` +
				`"endpoint":{"url":"` + server.URL + `","auth":"hmac"}}`
			if _, err := s.InstallDir(ctx, writePkg(t, raw)); err != nil {
				t.Fatal(err)
			}
			if err := s.Activate(ctx, "slow"); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				s.applyFilter(ctx, "slow", "content.before_save", map[string]string{"title": "Original"})
				close(done)
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("delivery did not reach the endpoint")
			}
			if err := s.Deactivate(ctx, "slow"); err != nil {
				t.Fatal(err)
			}
			if err := s.Activate(ctx, "slow"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, "UPDATE extensions SET failures = 19 WHERE slug = 'slow'"); err != nil {
				t.Fatal(err)
			}
			close(release)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("delivery did not finish")
			}
			x, err := s.Status(ctx, "slow")
			if err != nil || x.Failures != 19 || x.Status != StatusActive {
				t.Fatalf("old response changed the new activation: %+v, %v", x, err)
			}
			logs, err := s.Deliveries(ctx, "slow", 50)
			if err != nil || len(logs) != 0 {
				t.Fatalf("old response entered the new activation's log: %+v, %v", logs, err)
			}
			s.applyFilter(ctx, "slow", "content.before_save", map[string]string{"title": "Current"})
			x, err = s.Status(ctx, "slow")
			wantFailures, wantStatus := 0, StatusActive
			if status != http.StatusOK {
				wantFailures, wantStatus = 20, StatusError
			}
			if err != nil || x.Failures != wantFailures || x.Status != wantStatus {
				t.Fatalf("current response was not accounted for: %+v, %v", x, err)
			}
		})
	}
}
