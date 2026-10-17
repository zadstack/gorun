package site

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const unlockCookie = "gorun_site_unlock"

func availabilityScope(platform, local Availability, host string) string {
	sum := sha256.Sum256([]byte(host + "\x00" + platform.SiteID + "\x00" + strconv.FormatInt(platform.Revision, 10) + "\x00" + platform.Mode + "\x00" + platform.PasswordHash + "\x00" + strconv.FormatInt(local.Revision, 10) + "\x00" + local.Mode + "\x00" + local.PasswordHash))
	return hex.EncodeToString(sum[:])
}
func signUnlock(key []byte, scope string, expires int64) string {
	body := scope + "." + strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func validUnlock(r *http.Request, key []byte, scope string) bool {
	c, e := r.Cookie(unlockCookie)
	if e != nil {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 || parts[0] != scope {
		return false
	}
	exp, e := strconv.ParseInt(parts[1], 10, 64)
	now := time.Now().Unix()
	return e == nil && exp > now && exp <= now+3600 && hmac.Equal([]byte(c.Value), []byte(signUnlock(key, scope, exp)))
}

type unlockRate struct {
	active int
	mu     sync.Mutex
	hits   map[string]window
}

func (l *unlockRate) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.hits == nil {
		l.hits = map[string]window{}
	}
	for k, v := range l.hits {
		if now.Sub(v.start) >= time.Minute {
			delete(l.hits, k)
		}
	}
	// Bound both per-address work and the map under rotating address attacks.
	if len(l.hits) >= 4096 {
		return false
	}
	global := l.hits["*"]
	if global.count >= 30 {
		return false
	}
	if global.start.IsZero() {
		global.start = now
	}
	global.count++
	l.hits["*"] = global
	v := l.hits[ip]
	if v.count >= 5 {
		return false
	}
	if v.start.IsZero() {
		v.start = now
	}
	v.count++
	l.hits[ip] = v
	return true
}

func (l *unlockRate) begin() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active >= 2 {
		return false
	}
	l.active++
	return true
}
func (l *unlockRate) end() { l.mu.Lock(); l.active--; l.mu.Unlock() }
