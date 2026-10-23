package admin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zadstack/gorun/internal/db"
)

type logoRoundTrip func(*http.Request) (*http.Response, error)

func (f logoRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func seedLogoBinding(t *testing.T, h *handlers, ctx context.Context) (connectionRecord, LogoImagePrepared) {
	t.Helper()
	c := seedAIConnection(t, h, ctx, "https://images.example.test/v1")
	c.Model = "explicit-image-model"
	if e := h.d.Settings.Set(ctx, connectionPrefix+c.ID, c); e != nil {
		t.Fatal(e)
	}
	b := LogoBinding{ConnectionVersion: c.Version, Protocol: LogoProtocolPNG, Model: c.Model, Quality: "low", Background: "transparent", EstimateMicros: 50000, Currency: "USD", Source: "https://prices.example.test/image", AsOf: "2026-10-23", Basis: "Operator estimate including input assumptions, actual charge may differ"}
	if e := SaveIdentityLogoBinding(ctx, h.d, c.ID, b); e != nil {
		t.Fatal(e)
	}
	p, e := PrepareIdentityLogo(ctx, h.d, LogoImageInput{ConnectionID: c.ID, Theme: "default", Prompt: "Synthetic logo brief", Size: 1024, BudgetMicros: 100000, Currency: "USD"}, "identitylogo-static-v1")
	if e != nil {
		t.Fatal(e)
	}
	return c, p
}
func fakeLogoProvider(t *testing.T, check func(*http.Request)) (*http.Client, *atomic.Int64) {
	t.Helper()
	count := &atomic.Int64{}
	picture := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	picture.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})
	var encoded bytes.Buffer
	if e := png.Encode(&encoded, picture); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if check != nil {
			check(r)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(encoded.Bytes())}}})
	}))
	t.Cleanup(server.Close)
	local, _ := url.Parse(server.URL)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: logoRoundTrip(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = local.Scheme, local.Host
		copy.URL = &u
		return http.DefaultTransport.RoundTrip(copy)
	})}
	return client, count
}
func TestLogoReviewMutationsNeverSend(t *testing.T) {
	h, ctx, _, _ := canvasOperationFixture(t)
	c, approved := seedLogoBinding(t, h, ctx)
	client, count := fakeLogoProvider(t, nil)
	previous := identityLogoClient
	identityLogoClient = func() *http.Client { return client }
	t.Cleanup(func() { identityLogoClient = previous })
	var original LogoBinding
	if e := h.d.Settings.Get(ctx, logoBindingPrefix+c.ID, &original); e != nil {
		t.Fatal(e)
	}
	cases := map[string]func(*connectionRecord, *LogoBinding, *LogoImagePrepared){
		"credential": func(c *connectionRecord, _ *LogoBinding, _ *LogoImagePrepared) {
			c.Secret, _ = h.encryptConnectionSecrets(connectionSecrets{Token: "rotated-synthetic-key"})
		},
		"model":              func(c *connectionRecord, _ *LogoBinding, _ *LogoImagePrepared) { c.Model = "another-image-model" },
		"connection version": func(c *connectionRecord, _ *LogoBinding, _ *LogoImagePrepared) { c.Version = db.NewID() },
		"adapter version":    func(_ *connectionRecord, b *LogoBinding, _ *LogoImagePrepared) { b.Version = db.NewID() },
		"protocol":           func(_ *connectionRecord, b *LogoBinding, _ *LogoImagePrepared) { b.Protocol = "unsupported" },
		"quote":              func(_ *connectionRecord, b *LogoBinding, _ *LogoImagePrepared) { b.EstimateMicros++ },
		"quote source": func(_ *connectionRecord, b *LogoBinding, _ *LogoImagePrepared) {
			b.Source = "https://other.example.test/prices"
		},
		"payload": func(_ *connectionRecord, _ *LogoBinding, p *LogoImagePrepared) {
			p.Payload = json.RawMessage(`{"n":2}`)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			next, b, p := c, original, approved
			mutate(&next, &b, &p)
			_ = h.d.Settings.Set(ctx, connectionPrefix+c.ID, next)
			_ = h.d.Settings.Set(ctx, logoBindingPrefix+c.ID, b)
			if _, e := GenerateIdentityLogoBounded(ctx, h.d, p); !errors.Is(e, ErrLogoChanged) {
				t.Fatalf("mutation accepted: %v", e)
			}
			if count.Load() != 0 {
				t.Fatal("changed review reached transport")
			}
			_ = h.d.Settings.Set(ctx, connectionPrefix+c.ID, c)
			_ = h.d.Settings.Set(ctx, logoBindingPrefix+c.ID, original)
		})
	}
}
func TestLogoGenerationUsesTheReviewedCredentialSnapshot(t *testing.T) {
	h, ctx, _, _ := canvasOperationFixture(t)
	c, p := seedLogoBinding(t, h, ctx)
	client, count := fakeLogoProvider(t, func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hidden-provider-key" {
			t.Error("new or missing credential sent")
		}
		raw, _ := io.ReadAll(r.Body)
		if !bytes.Equal(raw, p.Payload) {
			t.Error("payload changed after approval")
		}
		if bytes.Contains(raw, []byte("response_format")) {
			t.Error("unsupported response_format sent")
		}
	})
	oldFactory, oldHook := identityLogoClient, afterLogoDestinationLoad
	identityLogoClient = func() *http.Client { return client }
	afterLogoDestinationLoad = func() {
		changed := c
		changed.Model = "changed-after-read"
		changed.Secret, _ = h.encryptConnectionSecrets(connectionSecrets{Token: "new-secret"})
		_ = h.d.Settings.Set(ctx, connectionPrefix+c.ID, changed)
	}
	t.Cleanup(func() { identityLogoClient = oldFactory; afterLogoDestinationLoad = oldHook })
	image, e := GenerateIdentityLogoBounded(ctx, h.d, p)
	if e != nil || len(image) == 0 || count.Load() != 1 {
		t.Fatalf("reviewed single request failed: %v calls=%d", e, count.Load())
	}
}
func TestLogoAdapterRejectsNativeAnthropicAndUnknownEstimate(t *testing.T) {
	h, ctx, _, _ := canvasOperationFixture(t)
	c, p := seedLogoBinding(t, h, ctx)
	var b LogoBinding
	_ = h.d.Settings.Get(ctx, logoBindingPrefix+c.ID, &b)
	c.Endpoint = "https://api.anthropic.com/v1"
	c.Provider = "anthropic"
	_ = h.d.Settings.Set(ctx, connectionPrefix+c.ID, c)
	if e := SaveIdentityLogoBinding(ctx, h.d, c.ID, b); e == nil {
		t.Fatal("native text protocol incorrectly declared image support")
	}
	c.Endpoint = "https://compatible.example.test/v1"
	_ = h.d.Settings.Set(ctx, connectionPrefix+c.ID, c)
	b.EstimateMicros = 0
	_ = h.d.Settings.Set(ctx, logoBindingPrefix+c.ID, b)
	prepared, e := PrepareIdentityLogo(ctx, h.d, p.Input, p.PromptVersion)
	if e != nil || prepared.Quote.Known {
		t.Fatalf("unknown estimate hidden or invented: %v", e)
	}
	client, count := fakeLogoProvider(t, nil)
	old := identityLogoClient
	identityLogoClient = func() *http.Client { return client }
	t.Cleanup(func() { identityLogoClient = old })
	if _, e = GenerateIdentityLogoBounded(ctx, h.d, prepared); e == nil || count.Load() != 0 {
		t.Fatal("unknown estimate caused provider I/O")
	}
}
func TestLogoTransportRejectsRemoteURLsAndSanitizesFailures(t *testing.T) {
	for _, body := range []string{`{"data":[{"url":"https://secret.example/private?token=hidden"}]}`, `{"data":[{"b64_json":"!!!"}]}`, `{"data":[{"b64_json":"eA=="},{"b64_json":"eA=="}]}`} {
		client := &http.Client{Transport: logoRoundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		_, e := sendIdentityLogo(context.Background(), client, "https://images.example.test/v1", "fake", []byte(`{}`))
		if !errors.Is(e, ErrLogoProvider) || strings.Contains(e.Error(), "token=") {
			t.Fatal("unsafe response exposed")
		}
	}
}

// A non-streaming image request returns no headers until the image exists;
// the transport must not give up on headers before the request budget.
func TestLogoHTTPClientWaitsForHeadersAsLongAsTheRequest(t *testing.T) {
	c := logoHTTPClient()
	transport, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport %T", c.Transport)
	}
	if c.Timeout != logoRequestTimeout || transport.ResponseHeaderTimeout < c.Timeout {
		t.Fatalf("client timeout %s, response header timeout %s", c.Timeout, transport.ResponseHeaderTimeout)
	}
	if c.CheckRedirect == nil {
		t.Fatal("redirects must stay refused")
	}
}
