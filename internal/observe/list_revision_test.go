package observe

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestListWithRevisionUsesSnapshotAndPreservesObservationStates(t *testing.T) {
	h := newHarness(t)
	enabledCalls, revisionCalls := 0, 0
	h.register(Spec{Freshness: time.Minute, Enabled: func(context.Context) bool { enabledCalls++; return true }, Revision: func(context.Context, string) (string, error) { revisionCalls++; return h.rev.Load().(string), nil }})
	ctx := context.Background()
	for _, key := range []string{"one", "two"} {
		if _, err := h.s.Refresh(ctx, "probe", key); err != nil {
			t.Fatal(err)
		}
	}
	h.drain()
	for _, revision := range []string{"r1", "r2"} {
		h.rev.Store(revision)
		for _, age := range []time.Duration{0, 2 * time.Minute} {
			h.now = h.now.Add(age)
			want, err := h.s.List(ctx, "probe")
			if err != nil {
				t.Fatal(err)
			}
			enabledCalls, revisionCalls = 0, 0
			got, err := h.s.ListWithRevision(ctx, "probe", func(context.Context, string) (string, error) { return revision, nil })
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("snapshot list differs from ordinary list: got %+v want %+v", got, want)
			}
			if enabledCalls != 1 || revisionCalls != 0 {
				t.Fatalf("expected one enabled read and no provider revision reads, got %d/%d", enabledCalls, revisionCalls)
			}
		}
	}
}
