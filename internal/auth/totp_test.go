package auth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/audit"
)

// RFC 6238 Appendix B, SHA-1 rows: key "12345678901234567890", 8 digits.
func TestTOTPRFC6238Vectors(t *testing.T) {
	key := []byte("12345678901234567890")
	for _, c := range []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	} {
		if got := hotp(key, uint64(c.unix/totpPeriod), 8); got != c.want {
			t.Errorf("T=%d: %s, want %s", c.unix, got, c.want)
		}
	}
	// The 6-digit codes the app shows are the last six digits.
	if got := totpCode(key, time.Unix(59, 0)); got != "287082" {
		t.Errorf("6-digit code %s", got)
	}
}

func TestMatchTOTPWindowAndReplay(t *testing.T) {
	key := newTOTPSecret()
	now := time.Date(2026, 11, 11, 12, 0, 15, 0, time.UTC)
	cur := totpStep(now)
	for name, c := range map[string]struct {
		at       time.Time
		lastStep int64
		ok       bool
	}{
		"current step":             {now, 0, true},
		"previous step (drift)":    {now.Add(-30 * time.Second), 0, true},
		"next step (drift)":        {now.Add(30 * time.Second), 0, true},
		"two steps old":            {now.Add(-60 * time.Second), 0, false},
		"two steps ahead":          {now.Add(60 * time.Second), 0, false},
		"replay of the used step":  {now, cur, false},
		"older than the used step": {now.Add(-30 * time.Second), cur - 1, false},
		"later than the used step": {now.Add(30 * time.Second), cur, true},
	} {
		step, ok := matchTOTP(key, totpCode(key, c.at), now, c.lastStep)
		if ok != c.ok {
			t.Errorf("%s: ok=%v, want %v", name, ok, c.ok)
		}
		if ok && step != totpStep(c.at) {
			t.Errorf("%s: step %d, want %d", name, step, totpStep(c.at))
		}
	}
	if _, ok := matchTOTP(key, "12345", now, 0); ok {
		t.Error("short code accepted")
	}
	// Spaces and Arabic-Indic digits are normalized.
	code := totpCode(key, now)
	arabic := strings.Map(func(r rune) rune { return '٠' + (r - '0') }, code)
	if _, ok := matchTOTP(key, " "+code[:3]+" "+code[3:], now, 0); !ok {
		t.Error("spaced code rejected")
	}
	if _, ok := matchTOTP(key, arabic, now, 0); !ok {
		t.Error("Arabic-Indic digits rejected")
	}
}

func TestTOTPURIAndSecretFormat(t *testing.T) {
	secret := []byte("12345678901234567890")
	uri := totpURI(secret, "My Site", "a@example.com")
	for _, want := range []string{"otpauth://totp/My%20Site:a@example.com?", "secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "issuer=My+Site", "digits=6", "period=30", "algorithm=SHA1"} {
		if !strings.Contains(uri, want) {
			t.Errorf("uri %s lacks %s", uri, want)
		}
	}
	if got := formatTOTPSecret(secret); got != "GEZD GNBV GY3T QOJQ GEZD GNBV GY3T QOJQ" {
		t.Errorf("grouped secret %q", got)
	}
}

// newTFStore returns a two-factor store on a fresh database with a clock.
func newTFStore(t *testing.T) (*twoFactorStore, *service, *clock) {
	t.Helper()
	d := newDB(t)
	deps := testDeps(d)
	deps.Config.SecretKey = make([]byte, 32)
	c := newClock()
	s := newTwoFactorStore(deps)
	s.now = c.now
	return s, testService(d), c
}

func TestTwoFactorEnrolVerifyAndReplay(t *testing.T) {
	s, svc, c := newTFStore(t)
	ctx := context.Background()
	u := mustUser(t, svc, "tf@example.com", "editor")
	if st, _ := s.status(ctx, u.ID); st.Enabled || st.Pending {
		t.Fatalf("fresh status %+v", st)
	}
	secret, err := s.begin(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The secret is encrypted at rest, bound to the user.
	var enc string
	s.db.QueryRow(`SELECT secret_enc FROM user_totp WHERE user_id = ?`, u.ID).Scan(&enc)
	if strings.Contains(enc, totpEncoding.EncodeToString(secret)) || len(enc) < 40 {
		t.Fatalf("secret stored in clear: %q", enc)
	}
	if _, err := s.open("someone-else", enc); err == nil {
		t.Fatal("secret decrypts for another user")
	}
	if _, err := s.verify(ctx, u.ID, totpCode(secret, c.now())); !errors.Is(err, errTwoFactorNotEnabled) {
		t.Fatalf("pending factor verified: %v", err)
	}
	if _, err := s.confirm(ctx, u.ID, "000000"); !errors.Is(err, errTwoFactorCode) {
		t.Fatalf("wrong confirmation code: %v", err)
	}
	codes, err := s.confirm(ctx, u.ID, totpCode(secret, c.now()))
	if err != nil || len(codes) != recoveryCodeCount {
		t.Fatalf("confirm: %v %d", err, len(codes))
	}
	if _, err := s.begin(ctx, u.ID); !errors.Is(err, errTwoFactorEnabled) {
		t.Fatalf("enabled secret replaced: %v", err)
	}
	// The confirmation code cannot be reused for a login (replay).
	if _, err := s.verify(ctx, u.ID, totpCode(secret, c.now())); !errors.Is(err, errTwoFactorCode) {
		t.Fatalf("confirmation code replayed: %v", err)
	}
	c.add(30 * time.Second)
	m, err := s.verify(ctx, u.ID, totpCode(secret, c.now()))
	if err != nil || m != "totp" {
		t.Fatalf("next step: %s %v", m, err)
	}
	if _, err := s.verify(ctx, u.ID, totpCode(secret, c.now())); !errors.Is(err, errTwoFactorCode) {
		t.Fatal("same step accepted twice")
	}
	st, _ := s.status(ctx, u.ID)
	if !st.Enabled || st.RecoveryLeft != recoveryCodeCount {
		t.Fatalf("status %+v", st)
	}
}

func TestRecoveryCodesSingleUseAndRegenerate(t *testing.T) {
	s, svc, c := newTFStore(t)
	ctx := WithUser(context.Background(), &User{ID: "actor"})
	u := mustUser(t, svc, "rc@example.com", "editor")
	secret, _ := s.begin(ctx, u.ID)
	codes, err := s.confirm(ctx, u.ID, totpCode(secret, c.now()))
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range codes {
		if len(code) != recoveryCodeLen+1 || code[5] != '-' {
			t.Fatalf("code format %q", code)
		}
	}
	if len(slices.Compact(slices.Sorted(slices.Values(codes)))) != len(codes) {
		t.Fatal("duplicate recovery codes")
	}
	// Stored hashed, never in clear.
	var n int
	s.db.QueryRow(`SELECT count(*) FROM user_recovery_codes WHERE code_hash = ? OR code_hash = ?`, codes[0], normalizeRecoveryCode(codes[0])).Scan(&n)
	if n != 0 {
		t.Fatal("recovery code stored in clear")
	}
	// Upper case, no dash and look-alike letters still match.
	typed := strings.ToUpper(strings.ReplaceAll(codes[0], "-", " "))
	if m, err := s.verify(ctx, u.ID, typed); err != nil || m != "recovery" {
		t.Fatalf("recovery code: %s %v", m, err)
	}
	if _, err := s.verify(ctx, u.ID, codes[0]); !errors.Is(err, errTwoFactorCode) {
		t.Fatal("recovery code used twice")
	}
	if st, _ := s.status(ctx, u.ID); st.RecoveryLeft != recoveryCodeCount-1 {
		t.Fatalf("left %d", st.RecoveryLeft)
	}
	// Another user's code never matches.
	other := mustUser(t, svc, "other@example.com", "editor")
	osecret, _ := s.begin(ctx, other.ID)
	if _, err := s.confirm(ctx, other.ID, totpCode(osecret, c.now())); err != nil {
		t.Fatal(err)
	}
	if _, err := s.verify(ctx, other.ID, codes[1]); !errors.Is(err, errTwoFactorCode) {
		t.Fatal("recovery code accepted for another user")
	}
	fresh, err := s.regenerate(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.verify(ctx, u.ID, codes[1]); !errors.Is(err, errTwoFactorCode) {
		t.Fatal("old recovery code survived regeneration")
	}
	if _, err := s.verify(ctx, u.ID, fresh[0]); err != nil {
		t.Fatalf("new recovery code: %v", err)
	}
	actions := auditActions(t, s.db)
	for _, want := range []string{"user.2fa_enabled", "user.2fa_recovery_used", "user.2fa_recovery_regenerated"} {
		if !slices.Contains(actions, want) {
			t.Errorf("audit lacks %s: %v", want, actions)
		}
	}
	if _, err := s.remove(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if on, _ := TwoFactorEnabled(ctx, s.db, u.ID); on {
		t.Fatal("factor survived removal")
	}
	s.db.QueryRow(`SELECT count(*) FROM user_recovery_codes WHERE user_id = ?`, u.ID).Scan(&n)
	if n != 0 {
		t.Fatal("recovery codes survived removal")
	}
}

func TestResetTwoFactorIsAudited(t *testing.T) {
	s, svc, c := newTFStore(t)
	u := mustUser(t, svc, "reset@example.com", "editor")
	secret, _ := s.begin(context.Background(), u.ID)
	if _, err := s.confirm(context.Background(), u.ID, totpCode(secret, c.now())); err != nil {
		t.Fatal(err)
	}
	deps := testDeps(s.db)
	deps.Audit = audit.New(s.db, discardLog())
	admin := &User{ID: "admin-id", Name: "Admin"}
	if err := ResetTwoFactor(WithUser(context.Background(), admin), deps, u.ID); err != nil {
		t.Fatal(err)
	}
	if on, _ := TwoFactorEnabled(context.Background(), s.db, u.ID); on {
		t.Fatal("reset left the factor")
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'user.2fa_reset' AND target_id = ?`, u.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("reset audited %d times", n)
	}
	// Resetting a user without a factor records nothing.
	if err := ResetTwoFactor(WithUser(context.Background(), admin), deps, u.ID); err != nil {
		t.Fatal(err)
	}
	s.db.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'user.2fa_reset'`).Scan(&n)
	if n != 1 {
		t.Fatalf("no-op reset audited: %d", n)
	}
}
