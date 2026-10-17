package fakebilling

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signed(t *testing.T, s *Server, method, path, body, key, secret string) *http.Response {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "abcdefghijklmnopqrstuvwxyz012345"
	sum := sha256.Sum256([]byte(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + nonce + "\n" + method + "\n" + Prefix + path + "\n" + hex.EncodeToString(sum[:])))
	req, _ := http.NewRequest(method, s.URL+Prefix+path, strings.NewReader(body))
	req.Header.Set("X-MCBB-Client", s.ClientID)
	req.Header.Set("X-MCBB-Timestamp", ts)
	req.Header.Set("X-MCBB-Nonce", nonce)
	req.Header.Set("X-MCBB-Signature", hex.EncodeToString(mac.Sum(nil)))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestFakeVerifiesReplaysAndInjects(t *testing.T) {
	s := New("c1", "sec")
	defer s.Close()
	if r := signed(t, s, "GET", "/catalog", "", "", "wrong"); r.StatusCode != 401 {
		t.Fatalf("bad signature: %d", r.StatusCode)
	}
	if r := signed(t, s, "GET", "/catalog", "", "", "sec"); r.StatusCode != 200 {
		t.Fatalf("catalog: %d", r.StatusCode)
	}
	body := `{"external_customer_id":"x1","order_ref":"gp_1","plan_code":"pro","period_end":"2030-01-01T00:00:00Z"}`
	if r := signed(t, s, "POST", "/accounts", body, "", "sec"); r.StatusCode != 400 {
		t.Fatalf("missing key: %d", r.StatusCode)
	}
	if r := signed(t, s, "POST", "/accounts", body, "k1", "sec"); r.StatusCode != 200 {
		t.Fatalf("create: %d", r.StatusCode)
	}
	if r := signed(t, s, "POST", "/accounts", body, "k1", "sec"); r.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("second create with the same key was not replayed")
	}
	if n := s.CallCount("POST", "/accounts"); n != 1 {
		t.Fatalf("create executed %d times", n)
	}
	s.FailNext("POST", "/accounts/*", 503, 1)
	if r := signed(t, s, "POST", "/accounts/x1/suspend", `{}`, "k2", "sec"); r.StatusCode != 503 {
		t.Fatalf("injected: %d", r.StatusCode)
	}
	if r := signed(t, s, "POST", "/accounts/x1/suspend", `{}`, "k2", "sec"); r.StatusCode != 200 {
		t.Fatalf("after injection: %d", r.StatusCode)
	}
	if a, _ := s.Account("x1"); a.Status != "suspended" {
		t.Fatalf("account = %+v", a)
	}
}
