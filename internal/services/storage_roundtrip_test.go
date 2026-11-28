package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// bindStorage binds the R2 bucket site-media for purpose.
func bindStorage(t *testing.T, r *Registry, purpose string) {
	t.Helper()
	_, fields, err := r.Set(context.Background(), "storage", map[string]string{"provider": "r2", "account": testR2Account, "bucket": "site-media", "purpose": purpose}, storageKeys(), "cli")
	if err != nil || len(fields) > 0 {
		t.Fatalf("bind %s: %v %v", purpose, fields, err)
	}
}

func TestStorageTestRoundTripsEachBoundPurpose(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	s3 := newFakeS3()
	r := NewRegistry(d, storageService{d: d, client: s3.client(t)})
	bindStorage(t, r, "both")

	res, err := r.Test(ctx, "storage", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Step != "" || res.Message != "media: ok; backups: ok" || res.At.IsZero() {
		t.Fatalf("result: %+v", res)
	}
	if left := s3.keys(); len(left) != 0 {
		t.Fatalf("test objects left behind: %v", left)
	}
	got := strings.Join(s3.methods, ",")
	if got != "HEAD,PUT,GET,DELETE,HEAD,PUT,GET,DELETE" {
		t.Fatalf("requests: %s", got)
	}
	// The result is recorded as last_test, and a change of the binding clears it.
	st, err := r.Status(ctx, "storage")
	if err != nil || st.LastTest == nil || !st.LastTest.OK || st.LastTest.Message != res.Message {
		t.Fatalf("last_test: %+v %v", st.LastTest, err)
	}
	bindStorage(t, r, "media")
	if st, _ := r.Status(ctx, "storage"); st.LastTest != nil {
		t.Fatalf("last_test kept after a change: %+v", st.LastTest)
	}
}

func TestStorageTestNamesTheFailingStep(t *testing.T) {
	for _, tc := range []struct {
		method string
		status int
		step   string
	}{
		{http.MethodHead, http.StatusForbidden, "auth"},
		{http.MethodHead, http.StatusNotFound, "bucket_missing"},
		{http.MethodPut, http.StatusForbidden, "write"},
		{http.MethodGet, http.StatusForbidden, "read"},
		{http.MethodDelete, http.StatusForbidden, "delete"},
	} {
		t.Run(tc.step, func(t *testing.T) {
			d := testDeps(t)
			s3 := newFakeS3()
			s3.fail[tc.method] = tc.status
			r := NewRegistry(d, storageService{d: d, client: s3.client(t)})
			bindStorage(t, r, "media")
			res, err := r.Test(context.Background(), "storage", "")
			if err != nil {
				t.Fatal(err)
			}
			if res.OK || res.Step != tc.step || !strings.HasPrefix(res.Message, "media: ") || !strings.Contains(res.Message, "HTTP") {
				t.Fatalf("result: %+v", res)
			}
			st, _ := r.Status(context.Background(), "storage")
			if st.LastTest == nil || st.LastTest.OK || st.LastTest.Step != tc.step {
				t.Fatalf("last_test: %+v", st.LastTest)
			}
		})
	}
}

func TestStorageTestWithNothingBoundIsNotConfigured(t *testing.T) {
	d := testDeps(t)
	r := NewRegistry(d, Storage(d))
	_, err := r.Test(context.Background(), "storage", "")
	var te *TestError
	if !errors.Is(err, ErrStorageNotConfigured) || !errors.As(ClassifyTestError(err), &te) || te.Key != errNotConfigured {
		t.Fatalf("err: %v", err)
	}
	if st, _ := r.Status(context.Background(), "storage"); st.LastTest != nil {
		t.Fatalf("a test that could not run is recorded: %+v", st.LastTest)
	}
}

// The JSON shape of a storage status for a bucket bound to both purposes is
// pinned here (docs/SERVICES.md documents it): destinations maps each bound
// purpose (media, backups) to the destination id, a plain string.
func TestStorageStatusJSONShape(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Storage(d))
	bindStorage(t, r, "both")
	st, err := r.Status(ctx, "storage")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(st)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]any) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	if want := []string{"fields", "last_test", "missing", "name", "provider", "secret_set", "source", "state"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("status keys %v, want %v", keys(got), want)
	}
	fields := got["fields"].(map[string]any)
	if want := []string{"account", "backups_source", "bucket", "destinations", "endpoint", "media_source", "provider", "purpose", "region"}; !reflect.DeepEqual(keys(fields), want) {
		t.Fatalf("field keys %v, want %v", keys(fields), want)
	}
	dests, ok := fields["destinations"].(map[string]any)
	if !ok || !reflect.DeepEqual(keys(dests), []string{"backups", "media"}) {
		t.Fatalf("destinations: %#v", fields["destinations"])
	}
	for purpose, setting := range map[string]string{"media": "storage.active.media", "backups": "storage.active.backup"} {
		if id, ok := dests[purpose].(string); !ok || id == "" || id != d.Settings.GetString(ctx, setting, "") {
			t.Fatalf("destinations.%s = %#v, want the id in %s", purpose, dests[purpose], setting)
		}
	}
	if fields["purpose"] != "both" || fields["media_source"] != "settings" || fields["backups_source"] != "settings" {
		t.Fatalf("fields: %v", fields)
	}
}

// The admin form says backup where the service says backups: both spellings
// bind the backup destination.
func TestStoragePurposeBackupIsBackups(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Storage(d))
	bindStorage(t, r, "backup")
	st, err := r.Status(ctx, "storage")
	if err != nil || st.Fields["purpose"] != "backups" || d.Settings.GetString(ctx, "storage.active.backup", "") == "" {
		t.Fatalf("status: %+v %v", st, err)
	}
	dests := st.Fields["destinations"].(map[string]string)
	if _, ok := dests["backups"]; !ok || len(dests) != 1 {
		t.Fatalf("destinations: %v", dests)
	}
}
