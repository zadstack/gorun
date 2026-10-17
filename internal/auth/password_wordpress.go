package auth

import (
	"crypto/hmac"
	"crypto/md5" // WordPress phpass compatibility only; new passwords use Argon2id.
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	phpassAlphabet         = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	maxPHPassLogIterations = 20
	maxWordPressBcryptCost = 14
)

// WordPress 6.8 domain-separates bcrypt inputs with base64 HMAC-SHA384.
// See wordpress-develop/6.8/src/wp-includes/pluggable.php:wp_check_password.
func verifyWordPressBcrypt(encoded, password string) bool {
	hash := encoded[3:]
	if len(hash) != 60 || !(strings.HasPrefix(hash, "$2y$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2a$")) {
		return false
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil || cost > maxWordPressBcryptCost {
		return false
	}
	prehash := hmac.New(sha512.New384, []byte("wp-sha384"))
	prehash.Write([]byte(password))
	input := base64.StdEncoding.EncodeToString(prehash.Sum(nil))
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(input)) == nil
}

// Portable phpass hashes use their own little-endian base64 alphabet.
// Limit iterations before hashing, so malformed imported records cannot exhaust CPU.
func verifyPHPass(encoded, password string) bool {
	if len(encoded) != 34 {
		return false
	}
	for _, c := range encoded[3:] {
		if !strings.ContainsRune(phpassAlphabet, c) {
			return false
		}
	}
	logIterations := strings.IndexByte(phpassAlphabet, encoded[3])
	if logIterations < 7 || logIterations > maxPHPassLogIterations {
		return false
	}
	input := []byte(password)
	digest := md5.Sum(append([]byte(encoded[4:12]), input...))
	buffer := make([]byte, md5.Size+len(input))
	copy(buffer[md5.Size:], input)
	for i := 0; i < 1<<logIterations; i++ {
		copy(buffer, digest[:])
		digest = md5.Sum(buffer)
	}
	got := encoded[:12] + encodePHPass(digest[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(encoded)) == 1
}

func encodePHPass(input []byte) string {
	var output strings.Builder
	for i := 0; i < len(input); {
		value := uint32(input[i])
		i++
		output.WriteByte(phpassAlphabet[value&63])
		if i < len(input) {
			value |= uint32(input[i]) << 8
		}
		output.WriteByte(phpassAlphabet[(value>>6)&63])
		if i >= len(input) {
			break
		}
		i++
		if i < len(input) {
			value |= uint32(input[i]) << 16
		}
		output.WriteByte(phpassAlphabet[(value>>12)&63])
		if i >= len(input) {
			break
		}
		i++
		output.WriteByte(phpassAlphabet[(value>>18)&63])
	}
	return output.String()
}

func wordpressPasswordHash(encoded string) bool {
	return strings.HasPrefix(encoded, "$wp$") || strings.HasPrefix(encoded, "$P$") || strings.HasPrefix(encoded, "$H$")
}
