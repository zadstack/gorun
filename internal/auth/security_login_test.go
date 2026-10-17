package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFailedLoginAuditSanitizesAttemptedIdentity(t *testing.T) {
	d := newDB(t)
	s := testService(d)
	s.verify = func(string, string) bool { return false }
	attempted := strings.Repeat("x", 500) + "\n\x1b<script>@example.com"
	password := "PRIVATE_PASSWORD_SENTINEL"
	if _, e := s.CheckPassword(withIP("203.0.113.9"), attempted, password); e != ErrInvalidCredentials {
		t.Fatal(e)
	}
	var raw, ip string
	if e := d.QueryRow(`SELECT meta_json,ip FROM audit_log WHERE action='user.login_failed' ORDER BY id DESC LIMIT 1`).Scan(&raw, &ip); e != nil {
		t.Fatal(e)
	}
	var meta map[string]string
	if e := json.Unmarshal([]byte(raw), &meta); e != nil {
		t.Fatal(e)
	}
	if len(meta["email"]) > 254 || strings.ContainsAny(meta["email"], "\n\r\x1b") || strings.Contains(raw, password) {
		t.Fatal("unsafe attempted identity metadata")
	}
	if ip != "203.0.113.9" || meta["reason"] != "invalid" {
		t.Fatal("missing observed failure metadata", ip)
	}
}
