package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type metricoolTransport func(*http.Request) (*http.Response, error)

func (f metricoolTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMetricoolCreatesOnlyDraftAtFixedProvider(t *testing.T) {
	c := metricoolClient{transport: metricoolTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://app.metricool.com/api/v2/scheduler/posts?blogId=23&userId=12" || r.Header.Get("X-Mc-Auth") != "private" {
			t.Fatal("provider/auth mismatch")
		}
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p["draft"] != true || p["autoPublish"] != false || p["text"] != "Reviewed text" {
			t.Fatal("not draft-only")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{"id":91,"draft":true,"autoPublish":false}}`)), Header: http.Header{}}, nil
	})}
	id, err := c.create(context.Background(), metricoolConfig{UserID: "12", BlogID: "23"}, "private", metricoolInput{Text: "Reviewed text", Network: "linkedin", DateTime: "2027-01-01T12:00:00", Timezone: "UTC"})
	if err != nil || id != "91" {
		t.Fatalf("%s %v", id, err)
	}
}
func TestMetricoolDoesNotFollowRedirectOrTrustNonDraft(t *testing.T) {
	for _, reply := range []struct {
		status int
		body   string
	}{{302, `{}`}, {200, `{"data":{"id":1,"draft":false}}`}, {200, `{"data":{"id":1,"draft":true,"autoPublish":true}}`}, {200, `{"data":{"draft":true}}`}} {
		calls := 0
		c := metricoolClient{transport: metricoolTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: reply.status, Header: http.Header{"Location": []string{"https://evil.example"}}, Body: io.NopCloser(strings.NewReader(reply.body))}, nil
		})}
		if _, err := c.create(context.Background(), metricoolConfig{UserID: "1", BlogID: "2"}, "secret", metricoolInput{}); err == nil || calls != 1 {
			t.Fatal("unsafe response accepted or retried")
		}
	}
}
func TestMetricoolReadOnlyProbeRequiresConfiguredBrand(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{{`[{"id":23}]`, true}, {`[{"id":24}]`, false}, {`[{"id":23,"deleted":true}]`, false}, {`{"data":[]}`, false}} {
		c := metricoolClient{transport: metricoolTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method != "GET" || r.URL.Path != "/api/admin/simpleProfiles" {
				t.Fatal("probe mutated provider")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}
		if err := c.test(context.Background(), metricoolConfig{UserID: "12", BlogID: "23"}, "secret"); (err == nil) != tc.ok {
			t.Fatal("brand verification mismatch")
		}
	}
}
