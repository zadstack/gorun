package commerce

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/httpx"
)

// The mcp-billing/v1 protocol, signed exactly like class-mcbb-client.php
// 0.3.1 (spec §4.6, docs/COMMERCE.md):
//
//	body          compact JSON, slashes and unicode unescaped; GET → ""
//	target        "/mcp-billing/v1" + path
//	string2sign   time "\n" nonce "\n" METHOD "\n" target "\n" hex(sha256(body))
//	X-MCBB-Signature = hex(hmac_sha256(secret, string2sign))
const (
	mcbbPrefix      = "/mcp-billing/v1"
	mcbbTimeout     = 15 * time.Second
	mcbbNonceLen    = 32
	mcbbMaxResponse = 1 << 20
	nonceAlphabet   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// MCBBError is a failed billing-server call. Status is the HTTP status (0
// for transport failures). Retryable reports transient failures (transport,
// 408, 429, 5xx), mirroring the PHP client's retry rule.
type MCBBError struct {
	Status  int
	Message string
}

func (e *MCBBError) Error() string {
	if e.Status == 0 {
		return "mcbb: " + e.Message
	}
	return fmt.Sprintf("mcbb: HTTP %d: %s", e.Status, e.Message)
}

// Retryable reports whether the call may succeed when retried.
func (e *MCBBError) Retryable() bool {
	return e.Status == 0 || e.Status == http.StatusRequestTimeout || e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// retryable reports whether err is a transient billing-server failure.
func retryable(err error) bool {
	var me *MCBBError
	return errors.As(err, &me) && me.Retryable()
}

// mcbbClient calls one billing server.
type mcbbClient struct {
	base     string // scheme://host[:port][/path], no trailing slash
	clientID string
	secret   string
	http     *http.Client
	now      func() time.Time
	nonce    func() string
}

// validateBaseURL enforces HTTPS on the default port without credentials;
// allowPrivate (GORUN_ALLOW_PRIVATE_NET, the home lab) also allows http
// and other ports.
func validateBaseURL(raw string, allowPrivate bool) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return "", fmt.Errorf("commerce: invalid billing server URL %q", raw)
	}
	switch {
	case u.User != nil || u.Fragment != "" || u.RawQuery != "":
		return "", errors.New("commerce: billing server URL must not contain credentials, a query or a fragment")
	case u.Scheme == "https":
		if !allowPrivate && u.Port() != "" && u.Port() != "443" {
			return "", errors.New("commerce: billing server URL must use the default HTTPS port")
		}
	case u.Scheme == "http" && allowPrivate:
	default:
		return "", errors.New("commerce: billing server URL must use HTTPS")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.EscapedPath(), "/"), nil
}

// newMCBBClient returns a client for baseURL (see validateBaseURL). The HTTP
// client is SSRF-safe (private networks blocked unless allowPrivate), times
// out after 15s and never follows redirects.
func newMCBBClient(baseURL, clientID, secret string, allowPrivate bool) (*mcbbClient, error) {
	base, err := validateBaseURL(baseURL, allowPrivate)
	if err != nil {
		return nil, err
	}
	hc := httpx.NewSafeClientOpts(httpx.SafeClientOptions{AllowPrivate: allowPrivate, Timeout: mcbbTimeout})
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &mcbbClient{base: base, clientID: clientID, secret: secret, http: hc, now: time.Now, nonce: randomNonce}, nil
}

// randomNonce returns 32 random alphanumeric characters (wp_generate_password(32, false, false)).
func randomNonce() string { return randomString(mcbbNonceLen) }

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("commerce: crypto/rand: %v", err))
	}
	for i := range b {
		b[i] = nonceAlphabet[int(b[i])%len(nonceAlphabet)]
	}
	return string(b)
}

// encodeMCBBBody is PHP's wp_json_encode($body, JSON_UNESCAPED_SLASHES |
// JSON_UNESCAPED_UNICODE): compact, no HTML escaping, no trailing newline.
// A nil body encodes to nothing.
func encodeMCBBBody(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("commerce: encode billing request: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// signMCBB returns hex(hmac_sha256(secret, time\nnonce\nMETHOD\ntarget\nhex(sha256(body)))).
func signMCBB(secret, ts, nonce, method, target string, body []byte) string {
	sum := sha256.Sum256(body)
	msg := strings.Join([]string{ts, nonce, method, target, hex.EncodeToString(sum[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

type mcbbEnvelope struct {
	OK    *bool           `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error any             `json:"error"`
}

// Do sends a signed request to /mcp-billing/v1+path and returns the
// envelope's data object. Mutations (non-GET) always carry Idempotency-Key:
// idemKey, or a random one when empty.
func (c *mcbbClient) Do(ctx context.Context, method, path string, body any, idemKey string) (json.RawMessage, error) {
	method = strings.ToUpper(method)
	var raw []byte
	if method != http.MethodGet {
		var err error
		if raw, err = encodeMCBBBody(body); err != nil {
			return nil, err
		}
	}
	target := mcbbPrefix + path
	ts := strconv.FormatInt(c.now().Unix(), 10)
	nonce := c.nonce()
	req, err := http.NewRequestWithContext(ctx, method, c.base+target, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("commerce: build billing request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MCBB-Client", c.clientID)
	req.Header.Set("X-MCBB-Timestamp", ts)
	req.Header.Set("X-MCBB-Nonce", nonce)
	req.Header.Set("X-MCBB-Signature", signMCBB(c.secret, ts, nonce, method, target, raw))
	if method != http.MethodGet {
		if idemKey == "" {
			idemKey = "gp_" + randomString(24)
		}
		req.Header.Set("Idempotency-Key", idemKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &MCBBError{Message: err.Error()}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, mcbbMaxResponse))
	if err != nil {
		return nil, &MCBBError{Message: "read response: " + err.Error()}
	}
	var env mcbbEnvelope
	decodeErr := json.Unmarshal(b, &env)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &MCBBError{Status: resp.StatusCode, Message: "unexpected server response: " + bodySnippet(b)}
	}
	// PHP accepts any array for data (is_array): a JSON object or list.
	if decodeErr != nil || env.OK == nil || !*env.OK || len(env.Data) == 0 || (env.Data[0] != '{' && env.Data[0] != '[') {
		return nil, &MCBBError{Status: resp.StatusCode, Message: "response is not an ok envelope with a data object: " + bodySnippet(b)}
	}
	return env.Data, nil
}

// bodySnippet keeps up to maxErrorLen bytes of a response body for errors.
func bodySnippet(b []byte) string {
	return truncate(strings.TrimSpace(string(bytes.ToValidUTF8(b, nil))), maxErrorLen)
}
