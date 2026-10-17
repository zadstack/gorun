package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

func appendFilter(s string) FilterFunc {
	return func(_ context.Context, v any) (any, error) { return v.(string) + s, nil }
}

func TestFilterChainOrderByPrio(t *testing.T) {
	r := New()
	r.Filter("title", 20, appendFilter("c"))
	r.Filter("title", 10, appendFilter("a"))
	r.Filter("title", 10, appendFilter("b")) // same prio: registration order
	v, err := r.Apply(context.Background(), "title", "")
	if err != nil || v.(string) != "abc" {
		t.Fatalf("got %v %v", v, err)
	}
}

func TestApplyWithoutFiltersReturnsInput(t *testing.T) {
	v, err := New().Apply(context.Background(), "none", 42)
	if err != nil || v.(int) != 42 {
		t.Fatalf("got %v %v", v, err)
	}
}

func TestApplyStopsOnError(t *testing.T) {
	r := New()
	boom := errors.New("boom")
	r.Filter("f", 1, appendFilter("a"))
	r.Filter("f", 2, func(context.Context, any) (any, error) { return nil, boom })
	r.Filter("f", 3, appendFilter("never"))
	v, err := r.Apply(context.Background(), "f", "")
	if !errors.Is(err, boom) || v.(string) != "a" {
		t.Fatalf("got %v %v", v, err)
	}
}

func TestActionErrorsJoined(t *testing.T) {
	r := New()
	e1, e2 := errors.New("e1"), errors.New("e2")
	ran := 0
	r.Action("saved", 1, func(context.Context, any) error { ran++; return e1 })
	r.Action("saved", 2, func(context.Context, any) error { ran++; return nil })
	r.Action("saved", 3, func(context.Context, any) error { ran++; return e2 })
	err := r.Do(context.Background(), "saved", nil)
	if ran != 3 || !errors.Is(err, e1) || !errors.Is(err, e2) {
		t.Fatalf("ran=%d err=%v", ran, err)
	}
	if err := r.Do(context.Background(), "unknown", nil); err != nil {
		t.Fatal(err)
	}
}

func TestEmitWithoutQueueRunsSync(t *testing.T) {
	r := New()
	var got json.RawMessage
	r.Action("thing.happened", 10, func(_ context.Context, p any) error {
		got = p.(json.RawMessage)
		return nil
	})
	r.Emit(context.Background(), "thing.happened", map[string]int{"a": 1})
	if string(got) != `{"a":1}` {
		t.Fatalf("got %s", got)
	}
}

func TestEmitWithQueueDeliversAsync(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := db.Migrate(ctx, d, "jobs", jobs.Migrations()); err != nil {
		t.Fatal(err)
	}
	q := jobs.New(d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := New()
	r.AttachQueue(q)
	got := make(chan string, 1)
	r.Action("thing.happened", 10, func(_ context.Context, p any) error {
		got <- string(p.(json.RawMessage))
		return nil
	})
	r.Emit(ctx, "thing.happened", map[string]int{"a": 1})
	jl, err := q.List(ctx, "queued", 10)
	if err != nil || len(jl) != 1 || jl[0].Kind != EventJobKind {
		t.Fatalf("want one queued %s job, got %+v %v", EventJobKind, jl, err)
	}
	q.Start(ctx, 1)
	select {
	case s := <-got:
		if s != `{"a":1}` {
			t.Fatalf("got %s", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event not delivered")
	}
	cancel()
	q.Wait()
}

func TestNamesSortedUnique(t *testing.T) {
	r := New()
	r.Action("b", 1, func(context.Context, any) error { return nil })
	r.Filter("a", 1, appendFilter(""))
	r.Filter("b", 1, appendFilter(""))
	if got := strings.Join(r.Names(), ","); got != "a,b" {
		t.Fatal(got)
	}
}

func TestRemoveUnregistersByToken(t *testing.T) {
	r := New()
	ctx := context.Background()
	var calls []string
	a1 := r.Action("x", 10, func(context.Context, any) error { calls = append(calls, "a1"); return nil })
	a2 := r.Action("x", 10, func(context.Context, any) error { calls = append(calls, "a2"); return nil })
	f1 := r.Filter("f", 10, appendFilter("1"))
	f2 := r.Filter("f", 20, appendFilter("2"))
	if a1 == a2 || a1.IsZero() || f1 == f2 {
		t.Fatalf("tokens must be unique and non-zero: %v %v %v %v", a1, a2, f1, f2)
	}
	if !r.Remove(a1) {
		t.Fatal("Remove of a live action token should report true")
	}
	if r.Remove(a1) {
		t.Fatal("second Remove of the same token should report false")
	}
	_ = r.Do(ctx, "x", nil)
	if strings.Join(calls, ",") != "a2" {
		t.Fatalf("after Remove(a1): %v", calls)
	}
	if !r.Remove(f1) {
		t.Fatal("Remove of a live filter token should report true")
	}
	if v, _ := r.Apply(ctx, "f", ""); v != "2" {
		t.Fatalf("after Remove(f1): %q", v)
	}
	r.Remove(a2)
	r.Remove(f2)
	if len(r.Names()) != 0 {
		t.Fatalf("names should drop when the last registration goes: %v", r.Names())
	}
	if r.Remove(Token{}) {
		t.Fatal("the zero token removes nothing")
	}
}

func TestRemoveDuringDoIsSafe(t *testing.T) {
	r := New()
	ctx := context.Background()
	var tok Token
	n := 0
	tok = r.Action("x", 0, func(context.Context, any) error { r.Remove(tok); n++; return nil })
	r.Action("x", 1, func(context.Context, any) error { n++; return nil })
	_ = r.Do(ctx, "x", nil) // removing itself mid-run must not skip or panic
	_ = r.Do(ctx, "x", nil)
	if n != 3 {
		t.Fatalf("calls %d, want 3", n)
	}
}
