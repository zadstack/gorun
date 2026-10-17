package webhooks

import (
	"context"
	"testing"
)

// A dispatch that fails part-way queues nothing, so the hook's retry of the
// event does not send duplicates to the webhooks that were already queued.
func TestDispatchIsAtomicSoRetriesDoNotDuplicate(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	for _, n := range []string{"a", "b", "c"} {
		if _, _, err := s.Create(ctx, Input{Name: strp(n), URL: strp("https://" + n + ".example.com/h"), Events: []string{"*"}}); err != nil {
			t.Fatal(err)
		}
	}
	hooks, _ := s.List(ctx)
	last := hooks[len(hooks)-1].ID // queued after the others
	if _, err := s.db.Exec(`CREATE TRIGGER fail_last BEFORE INSERT ON webhook_deliveries WHEN NEW.webhook_id = '` + last + `' BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.dispatch(ctx, "menus.saved", map[string]string{"id": "m"}); err == nil {
		t.Fatal("dispatch must report the failure so the event is retried")
	}
	count := func() (n int) {
		_ = s.db.QueryRow(`SELECT count(*) FROM webhook_deliveries`).Scan(&n)
		return n
	}
	if n := count(); n != 0 {
		t.Fatalf("a failed dispatch must queue nothing, %d queued", n)
	}
	_, _ = s.db.Exec(`DROP TRIGGER fail_last`)
	if err := s.dispatch(ctx, "menus.saved", map[string]string{"id": "m"}); err != nil {
		t.Fatal(err)
	}
	for _, w := range hooks {
		list, _ := s.Deliveries(ctx, w.ID, 10)
		if len(list) != 1 {
			t.Fatalf("webhook %s: %d deliveries after a retried dispatch, want 1", w.Name, len(list))
		}
	}
}
