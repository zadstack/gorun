package commerce

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type signatureVector struct {
	Secret, Time, Nonce, Method, Target, Body string
	ExpectedBodySHA256                        string `json:"expected_body_sha256"`
	ExpectedSignature                         string `json:"expected_signature"`
}

func loadVector(t *testing.T) signatureVector {
	t.Helper()
	b, err := os.ReadFile("testdata/mcbb_signature_vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var v signatureVector
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestMCBBSignatureMatchesPHPVector recomputes the spec §4.6 signature step
// by step with the standard library (independently of the client code),
// checks it against the stored golden vector, then checks that the client
// produces exactly the same headers on the wire.
func TestMCBBSignatureMatchesPHPVector(t *testing.T) {
	v := loadVector(t)
	if v.Time != "1700000000" || v.Nonce != "abcdefghijklmnopqrstuvwxyz012345" || v.Method != "POST" ||
		v.Target != "/mcp-billing/v1/accounts" || v.Body != `{"a":1}` || v.Secret != "secret" {
		t.Fatalf("vector inputs changed: %+v", v)
	}

	// Step 1: hex(sha256(body)).
	bodySum := sha256.Sum256([]byte(v.Body))
	bodyHex := hex.EncodeToString(bodySum[:])
	if bodyHex != v.ExpectedBodySHA256 {
		t.Fatalf("body sha256 = %s, vector says %s", bodyHex, v.ExpectedBodySHA256)
	}
	// Step 2: time \n nonce \n METHOD \n target \n bodyHex (PHP implode("\n", …)).
	stringToSign := v.Time + "\n" + v.Nonce + "\n" + v.Method + "\n" + v.Target + "\n" + bodyHex
	// Step 3: hex(hmac_sha256(secret, stringToSign)).
	mac := hmac.New(sha256.New, []byte(v.Secret))
	mac.Write([]byte(stringToSign))
	expected := hex.EncodeToString(mac.Sum(nil))
	if expected != v.ExpectedSignature {
		t.Fatalf("independent signature = %s, vector says %s", expected, v.ExpectedSignature)
	}

	// The client's signing function.
	if got := signMCBB(v.Secret, v.Time, v.Nonce, v.Method, v.Target, []byte(v.Body)); got != expected {
		t.Fatalf("signMCBB = %s, want %s", got, expected)
	}

	// The client on the wire: fixed clock and nonce, body {"a":1}.
	var got http.Header
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		if r.URL.Path != "/base/mcp-billing/v1/accounts" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"ok":true,"data":{"status":"active"}}`))
	}))
	defer srv.Close()
	c, err := newMCBBClient(srv.URL+"/base/", "client-1", v.Secret, true)
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return time.Unix(1700000000, 0) }
	c.nonce = func() string { return v.Nonce }
	if _, err := c.Do(context.Background(), "POST", "/accounts", map[string]int{"a": 1}, "create:1"); err != nil {
		t.Fatal(err)
	}
	if gotBody != v.Body {
		t.Fatalf("body on the wire = %q, want %q", gotBody, v.Body)
	}
	want := map[string]string{
		"X-Mcbb-Client": "client-1", "X-Mcbb-Timestamp": v.Time, "X-Mcbb-Nonce": v.Nonce,
		"X-Mcbb-Signature": expected, "Accept": "application/json", "Content-Type": "application/json",
	}
	for k, w := range want {
		if got.Get(k) != w {
			t.Errorf("header %s = %q, want %q", k, got.Get(k), w)
		}
	}
}

func TestMCBBBodyEncodingIsCompactAndUnescaped(t *testing.T) {
	b, err := encodeMCBBBody(map[string]any{"url": "https://x.test/a<b>&c", "name": "زاد"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"name":"زاد","url":"https://x.test/a<b>&c"}`; string(b) != want {
		t.Fatalf("body = %s, want %s", b, want)
	}
	if b, _ := encodeMCBBBody(nil); len(b) != 0 {
		t.Fatalf("nil body = %q", b)
	}
}

func TestMCBBIdempotencyKeyOnMutations(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{} // method path → Idempotency-Key
	var getBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Method+" "+r.URL.Path] = r.Header.Get("Idempotency-Key")
		if r.Method == "GET" {
			b, _ := io.ReadAll(r.Body)
			getBody = string(b)
			// GET signs the empty body.
			empty := sha256.Sum256(nil)
			mac := hmac.New(sha256.New, []byte("s3"))
			mac.Write([]byte(r.Header.Get("X-MCBB-Timestamp") + "\n" + r.Header.Get("X-MCBB-Nonce") + "\nGET\n/mcp-billing/v1/catalog\n" + hex.EncodeToString(empty[:])))
			if hex.EncodeToString(mac.Sum(nil)) != r.Header.Get("X-MCBB-Signature") {
				t.Errorf("GET signature does not cover the empty body")
			}
		}
		mu.Unlock()
		w.Write([]byte(`{"ok":true,"data":{}}`))
	}))
	defer srv.Close()
	c, err := newMCBBClient(srv.URL, "c", "s3", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.Do(ctx, "GET", "/catalog", nil, "ignored"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(ctx, "POST", "/accounts", map[string]string{"external_id": "x"}, "create:o1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(ctx, "POST", "/accounts/x/suspend", map[string]string{"reason": "r"}, ""); err != nil {
		t.Fatal(err)
	}
	if k := seen["GET /mcp-billing/v1/catalog"]; k != "" {
		t.Errorf("GET carried Idempotency-Key %q", k)
	}
	if getBody != "" {
		t.Errorf("GET sent a body: %q", getBody)
	}
	if k := seen["POST /mcp-billing/v1/accounts"]; k != "create:o1" {
		t.Errorf("POST /accounts Idempotency-Key = %q", k)
	}
	if k := seen["POST /mcp-billing/v1/accounts/x/suspend"]; len(k) < 16 {
		t.Errorf("mutation without a caller key must still get a generated key, got %q", k)
	}
}

func TestMCBBRejectsHTTP(t *testing.T) {
	for _, u := range []string{"http://billing.example.com", "ftp://billing.example.com", "https://user:pw@billing.example.com", "https://billing.example.com:8443", "billing.example.com", "https://"} {
		if _, err := newMCBBClient(u, "c", "s", false); err == nil {
			t.Errorf("%s accepted without AllowPrivateNet", u)
		}
	}
	if _, err := newMCBBClient("https://billing.example.com/", "c", "s", false); err != nil {
		t.Errorf("https rejected: %v", err)
	}
	// The home lab (AllowPrivateNet) may use plain http.
	if _, err := newMCBBClient("http://10.0.0.5:8080", "c", "s", true); err != nil {
		t.Errorf("http with AllowPrivateNet rejected: %v", err)
	}
}

func TestMCBBNoRedirectsAndErrorClassification(t *testing.T) {
	status := http.StatusFound
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch status {
		case http.StatusFound:
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		case http.StatusOK:
			w.Write([]byte(`{"ok":false,"error":"nope"}`))
		default:
			w.WriteHeader(status)
			w.Write([]byte(`{"ok":false}`))
		}
	}))
	defer srv.Close()
	c, _ := newMCBBClient(srv.URL, "c", "s", true)
	ctx := context.Background()
	cases := []struct {
		status    int
		retryable bool
	}{{http.StatusFound, false}, {http.StatusOK, false}, {500, true}, {503, true}, {429, true}, {408, true}, {400, false}, {404, false}, {422, false}}
	for _, tc := range cases {
		status = tc.status
		_, err := c.Do(ctx, "POST", "/accounts", map[string]string{}, "k")
		var me *MCBBError
		if !errors.As(err, &me) {
			t.Fatalf("%d: err = %v", tc.status, err)
		}
		if me.Retryable() != tc.retryable {
			t.Errorf("%d: retryable = %v", tc.status, me.Retryable())
		}
	}
	// Transport failures are retryable.
	srv.Close()
	_, err := c.Do(ctx, "GET", "/catalog", nil, "")
	var me *MCBBError
	if !errors.As(err, &me) || !me.Retryable() || !strings.Contains(err.Error(), "mcbb") {
		t.Fatalf("transport error = %v", err)
	}
}
