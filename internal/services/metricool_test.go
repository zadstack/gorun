package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/mailer"
)

const testMetricoolToken = "metricool-token-value-0001"

type metricoolFake func(*http.Request) (*http.Response, error)

func (f metricoolFake) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// brandList answers Metricool's brand list with brands, checking the token.
func brandList(t *testing.T, status int, brands string) metricoolFake {
	return func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "app.metricool.com" || r.URL.Path != "/api/admin/simpleProfiles" || r.Header.Get("X-Mc-Auth") != testMetricoolToken {
			t.Errorf("request %s %v", r.URL, r.Header.Get("X-Mc-Auth") != "")
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(brands)), Header: http.Header{}}, nil
	}
}

func metricoolToken() *string {
	s := testMetricoolToken
	return &s
}

func storedMetricool(t *testing.T, r *Registry) admin.MetricoolConfig {
	t.Helper()
	var c admin.MetricoolConfig
	if err := r.d.Settings.Get(context.Background(), admin.MetricoolConfigKey, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMetricoolSetStoresTokenEncryptedAndNeverShowsIt(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Metricool(d))
	st, err := r.Status(ctx, "metricool")
	if err != nil || st.State != StateOff {
		t.Fatalf("fresh: %+v %v", st, err)
	}
	st, fields, err := r.Set(ctx, "metricool", map[string]string{"user_id": "12", "blog_id": "34"}, nil, "cli")
	if err != nil || len(fields) > 0 || st.State != StateIncomplete || strings.Join(st.Missing, ",") != "token" {
		t.Fatalf("without token: %+v %v %v", st, fields, err)
	}
	st, fields, err = r.Set(ctx, "metricool", map[string]string{"tracker_hash": "abc123def456"}, metricoolToken(), "cli")
	if err != nil || len(fields) > 0 || st.State != StateConfigured || !st.SecretSet {
		t.Fatalf("with token: %+v %v %v", st, fields, err)
	}
	if st.Fields["user_id"] != "12" || st.Fields["blog_id"] != "34" || st.Fields["tracker_hash"] != "abc123def456" || st.Fields["drafts_ready"] != false {
		t.Fatalf("fields: %v", st.Fields)
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), testMetricoolToken) || strings.Contains(string(raw), "enc:") {
		t.Fatalf("status shows the token: %s", raw)
	}
	c := storedMetricool(t, r)
	if plain, err := mailer.DecryptSecret(d.Config.SecretKey, c.Secret); err != nil || plain != testMetricoolToken || c.Version == "" {
		t.Fatalf("stored: %+v %v", c, err)
	}

	// Fixing the brand keeps the token, as the admin form would not have to.
	st, fields, err = r.Set(ctx, "metricool", map[string]string{"blog_id": "56"}, nil, "cli")
	if err != nil || len(fields) > 0 || st.Fields["blog_id"] != "56" || !st.SecretSet {
		t.Fatalf("brand fix: %+v %v %v", st, fields, err)
	}
	if plain, _ := mailer.DecryptSecret(d.Config.SecretKey, storedMetricool(t, r).Secret); plain != testMetricoolToken {
		t.Fatal("token lost on a brand change")
	}
}

func TestMetricoolSetRefusesBadValues(t *testing.T) {
	d := testDeps(t)
	r := NewRegistry(d, Metricool(d))
	_, fields, err := r.Set(context.Background(), "metricool", map[string]string{"user_id": "012", "blog_id": "x", "tracker_hash": "<script>", "secret": "v"}, nil, "cli")
	if err != nil || fields["user_id"] == "" || fields["blog_id"] == "" || fields["tracker_hash"] == "" || fields["secret"] != errUnknownField {
		t.Fatalf("fields: %v %v", fields, err)
	}
	// A token named as a value is refused before anything is written.
	if f := RemoteFields(Metricool(d), map[string]string{"token": "x"}); f["token"] != ErrKeySecretValue {
		t.Fatalf("remote: %v", f)
	}
}

func TestMetricoolTestChecksTokenAndBrand(t *testing.T) {
	for _, tc := range []struct {
		name, brands string
		status       int
		ok           bool
		step         string
	}{
		{"pass", `[{"id":34,"deleted":false}]`, 200, true, ""},
		{"brand missing", `[{"id":99,"deleted":false}]`, 200, false, "brand"},
		{"brand deleted", `[{"id":34,"deleted":true}]`, 200, false, "brand"},
		{"token refused", `{}`, 401, false, "credential"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDeps(t)
			ctx := context.Background()
			r := NewRegistry(d, metricoolService{d: d, transport: brandList(t, tc.status, tc.brands)})
			if _, fields, err := r.Set(ctx, "metricool", map[string]string{"user_id": "12", "blog_id": "34"}, metricoolToken(), "cli"); err != nil || len(fields) > 0 {
				t.Fatalf("set: %v %v", fields, err)
			}
			before := storedMetricool(t, r).Version
			res, err := r.Test(ctx, "metricool", "")
			if err != nil || res.OK != tc.ok || res.Step != tc.step {
				t.Fatalf("test: %+v %v", res, err)
			}
			c := storedMetricool(t, r)
			wantStatus := map[bool]string{true: "pass", false: "fail"}[tc.ok]
			if c.TestStatus != wantStatus || c.TestedAt == "" || c.Version == before {
				t.Fatalf("stored test: %+v", c)
			}
			st, _ := r.Status(ctx, "metricool")
			if st.LastTest == nil || st.LastTest.OK != tc.ok || st.Fields["drafts_ready"] != tc.ok {
				t.Fatalf("status: %+v", st)
			}
			// A new brand needs a new test before drafts.
			if _, _, err := r.Set(ctx, "metricool", map[string]string{"blog_id": "35"}, nil, "cli"); err != nil {
				t.Fatal(err)
			}
			if st, _ := r.Status(ctx, "metricool"); st.LastTest != nil || st.Fields["drafts_ready"] != false {
				t.Fatalf("after brand change: %+v", st)
			}
		})
	}
}

func TestMetricoolTestNeedsUserBrandAndToken(t *testing.T) {
	d := testDeps(t)
	r := NewRegistry(d, Metricool(d))
	if _, _, err := r.Set(context.Background(), "metricool", map[string]string{"user_id": "12", "blog_id": "34"}, nil, "cli"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Test(context.Background(), "metricool", ""); err != ErrMetricoolNotConfigured {
		t.Fatalf("err: %v", err)
	}
}

func TestMetricoolOffRemovesConfig(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Metricool(d))
	if _, _, err := r.Set(ctx, "metricool", map[string]string{"user_id": "12", "blog_id": "34", "tracker_hash": "abc123def456"}, metricoolToken(), "cli"); err != nil {
		t.Fatal(err)
	}
	if err := r.Off(ctx, "metricool", "cli"); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.Status(ctx, "metricool"); st.State != StateOff || st.SecretSet || st.Fields["tracker_hash"] != "" {
		t.Fatalf("after off: %+v", st)
	}
}
