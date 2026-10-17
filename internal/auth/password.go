package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP baseline: 19 MiB, 2 passes, 1 lane).
const (
	argonMemory             = 19 * 1024
	argonTime               = 2
	argonThreads            = 1
	argonKeyLen             = 32
	argonSaltLen            = 16
	maxPasswordBytes        = 4096
	maxEncodedPasswordBytes = 256
	maxArgonMemory          = 64 * 1024
	maxArgonTime            = 10
	maxArgonThreads         = 8
)

// HashPassword returns a PHC-encoded Argon2id hash:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash> (raw std base64).
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword accepts native Argon2id and migrated WordPress password hashes.
// Malformed hashes and parameters outside bounded verification costs never match.
func VerifyPassword(encoded, password string) bool {
	if len(encoded) > maxEncodedPasswordBytes {
		return false
	}
	if strings.HasPrefix(encoded, "$wp$") {
		return len(password) <= maxPasswordBytes && verifyWordPressBcrypt(encoded, password)
	}
	if strings.HasPrefix(encoded, "$P$") || strings.HasPrefix(encoded, "$H$") {
		return len(password) <= maxPasswordBytes && verifyPHPass(encoded, password)
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil ||
		m < 8*uint32(p) || m > maxArgonMemory || t == 0 || t > maxArgonTime || p == 0 || p > maxArgonThreads ||
		parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", m, t, p) {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified for unknown users so lookups take as long as a
// real password check (no user enumeration by timing).
var dummyHash, _ = HashPassword("gorun-timing-equaliser")
