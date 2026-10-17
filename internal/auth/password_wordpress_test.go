package auth

import (
	"errors"
	"strings"
	"testing"
)

// Fixtures generated independently with PHP password_hash (HMAC-SHA384 input)
// and WordPress 6.8's unmodified PasswordHash class. Password is test data only.
const testWPBcrypt = "$wp$2y$04$FPdGuc3y6GGVHml7z7oVXO4BkIpwB2S0Q73WBMd/vDTxmH.pufRi."
const testPHPass = "$P$BdSev0eLjWVY5LHrJCKN2lj6CRfnNX/"

func TestImportedWordPressPasswords(t *testing.T) {
	for _, hash := range []string{testWPBcrypt, testPHPass, "$H$" + testPHPass[3:]} {
		t.Run(hash[:3], func(t *testing.T) {
			if !VerifyPassword(hash, "correct horse battery") {
				t.Fatal("independent WordPress fixture rejected")
			}
			for _, wrong := range []string{"wrong", " correct horse battery", strings.Repeat("x", 4097)} {
				if VerifyPassword(hash, wrong) {
					t.Fatal("incorrect password accepted")
				}
			}
		})
	}
}

func TestPasswordResourceBounds(t *testing.T) {
	for _, hash := range []string{
		"$P$z" + testPHPass[4:], "$P$!" + testPHPass[4:], testPHPass[:33],
		"$wp$2y$31$" + testWPBcrypt[10:], "$wp$2y$04$broken", "$wp$2x$04$" + testWPBcrypt[10:],
		"$argon2id$v=19$m=4294967295,t=1,p=1$AAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=8,t=4294967295,p=1$AAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=8,t=1,p=255$AAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=8,t=1,p=1extra$AAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=8,t=1,p=1$$AAAAAAAAAAAAAAAAAAAAAA",
		"5f4dcc3b5aa765d61d8327deb882cf99", // Bare legacy MD5 is not enabled.
	} {
		if VerifyPassword(hash, "password") {
			t.Fatal("malformed or unsupported hash accepted")
		}
	}
}

func TestWordPressPrehashPreservesLongUnicodePassword(t *testing.T) {
	const hash = "$wp$2y$04$SAG6tjzWpnLNczn/dYdEjeRC/x.sOFcx8UGriERxiFA32ygZT6fJ."
	password := strings.Repeat("long-password-", 30) + "آخر"
	if !VerifyPassword(hash, password) || VerifyPassword(hash, password[:72]) || VerifyPassword(hash, password+"x") {
		t.Fatal("WordPress HMAC prehash lost password entropy")
	}
}

func TestWordPressLoginUpgradesHashWithoutChangingRole(t *testing.T) {
	for _, hash := range []string{testWPBcrypt, testPHPass} {
		t.Run(hash[:3], func(t *testing.T) {
			s := testService(newDB(t))
			u := mustUser(t, s, "migrated@example.com", RoleCustomer)
			if _, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, u.ID); err != nil {
				t.Fatal(err)
			}
			ctx := withIP("198.51.100.91")
			if _, err := s.CheckPassword(ctx, u.Email, "wrong"); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("wrong password: %v", err)
			}
			var stored string
			if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, u.ID).Scan(&stored); err != nil || stored != hash {
				t.Fatal("failed login changed imported hash")
			}
			got, err := s.CheckPassword(ctx, u.Email, "correct horse battery")
			if err != nil || got.Role != RoleCustomer || got.ID != u.ID {
				t.Fatalf("imported login/role: %+v %v", got, err)
			}
			if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, u.ID).Scan(&stored); err != nil || !strings.HasPrefix(stored, "$argon2id$") || !VerifyPassword(stored, "correct horse battery") {
				t.Fatal("successful login did not upgrade to a usable Argon2 hash")
			}
			if _, err := s.CheckPassword(ctx, u.Email, "correct horse battery"); err != nil {
				t.Fatalf("repeat login after upgrade: %v", err)
			}
		})
	}
}

func TestDisabledImportedAccountDoesNotUpgrade(t *testing.T) {
	s := testService(newDB(t))
	u := mustUser(t, s, "disabled-import@example.com", RoleCustomer)
	if _, err := s.db.Exec(`UPDATE users SET password_hash = ?, status = 'disabled' WHERE id = ?`, testWPBcrypt, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckPassword(withIP("198.51.100.92"), u.Email, "correct horse battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled account: %v", err)
	}
	var hash string
	if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, u.ID).Scan(&hash); err != nil || hash != testWPBcrypt {
		t.Fatal("disabled account hash changed")
	}
}
