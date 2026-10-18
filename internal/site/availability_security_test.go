package site

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
)

func TestAvailabilityPlatformPrecedence(t *testing.T) {
	for _, pm := range []string{"public", "banner", "blocked", "password"} {
		for _, lm := range []string{"public", "construction", "password"} {
			got := effectiveAvailability(Availability{Mode: pm}, Availability{Mode: lm})
			want := lm
			if pm == "blocked" || pm == "password" {
				want = pm
			}
			if got.Mode != want {
				t.Fatalf("%s/%s: %s", pm, lm, got.Mode)
			}
		}
	}
	hash, e := auth.HashPassword("fixture long password")
	if e != nil {
		t.Fatal(e)
	}
	p := Availability{Mode: "password", PasswordHash: hash}
	if !validAvailability(p, true) {
		t.Fatal("valid hash rejected")
	}
	p.PasswordHash = strings.Replace(hash, "m=19456", "m=999999999", 1)
	if validAvailability(p, true) {
		t.Fatal("unbounded argon parameters")
	}
}
func TestAvailabilityCookieScopeExpiryTamper(t *testing.T) {
	key := []byte("test key")
	p := Availability{SiteID: "one", Mode: "password", Revision: 2}
	l := Availability{Mode: "public"}
	scope := availabilityScope(p, l, "site.example")
	token := signUnlock(key, scope, time.Now().Add(time.Minute).Unix())
	req := httptest.NewRequest("GET", "https://site.example/", nil)
	req.AddCookie(&http.Cookie{Name: unlockCookie, Value: token})
	if !validUnlock(req, key, scope) {
		t.Fatal("valid")
	}
	for _, badScope := range []string{availabilityScope(p, l, "other.example"), availabilityScope(Availability{SiteID: "two", Mode: "password", Revision: 2}, l, "site.example"), availabilityScope(Availability{SiteID: "one", Mode: "password", Revision: 3}, l, "site.example")} {
		if validUnlock(req, key, badScope) {
			t.Fatal("scope bypass")
		}
	}
	for _, exp := range []int64{time.Now().Add(-time.Minute).Unix(), time.Now().Add(2 * time.Hour).Unix()} {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: unlockCookie, Value: signUnlock(key, scope, exp)})
		if validUnlock(r, key, scope) {
			t.Fatal("expiry bypass")
		}
	}
	if validUnlock(req, []byte("wrong key"), scope) {
		t.Fatal("signature bypass")
	}
}
func TestAvailabilityRateBoundAndConcurrency(t *testing.T) {
	l := unlockRate{}
	for i := 0; i < 5; i++ {
		if !l.allow("ip") {
			t.Fatal(i)
		}
	}
	if l.allow("ip") {
		t.Fatal("per ip")
	}
	first, second, third := l.begin(), l.begin(), l.begin()
	if !first || !second || third {
		t.Fatal("concurrency")
	}
	l.end()
	if !l.begin() {
		t.Fatal("release")
	}
	for i := 0; i < 5000; i++ {
		l.allow(string(rune(i + 100)))
	}
	if len(l.hits) > 32 {
		t.Fatal("unbounded map", len(l.hits))
	}
}
func TestAvailabilityBannerEscapesAndPrivateCache(t *testing.T) {
	rr := httptest.NewRecorder()
	w := &bannerWriter{ResponseWriter: &privateWriter{ResponseWriter: rr}, message: "<script>alert(1)</script>"}
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "public")
	w.WriteHeader(200)
	w.Write([]byte("<html><body>hello</body></html>"))
	if strings.Contains(rr.Body.String(), "<script>") || !strings.Contains(rr.Body.String(), "&lt;script&gt;") || rr.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(rr.Body.String())
	}
}

func TestAvailabilityPolicyNodeTrust(t *testing.T) {
	for _, c := range []struct {
		uid  uint32
		mode os.FileMode
		dir  bool
		want bool
	}{{0, 0640, false, true}, {0, 0660, false, false}, {0, 0644, false, false}, {123, 0640, false, false}, {0, os.ModeSymlink | 0640, false, false}, {0, os.ModeDir | 0711, true, true}, {0, os.ModeDir | 0771, true, false}} {
		st := policyTestInfo{mode: c.mode, uid: c.uid}
		if got := trustedPolicyNode(st, c.dir); got != c.want {
			t.Fatalf("%+v got %v", c, got)
		}
	}
}

type policyTestInfo struct {
	mode os.FileMode
	uid  uint32
}

func (policyTestInfo) Name() string        { return "policy.json" }
func (policyTestInfo) Size() int64         { return 100 }
func (p policyTestInfo) Mode() os.FileMode { return p.mode }
func (policyTestInfo) ModTime() time.Time  { return time.Time{} }
func (p policyTestInfo) IsDir() bool       { return p.mode.IsDir() }
func (p policyTestInfo) Sys() any          { return &syscall.Stat_t{Uid: p.uid} }

func TestAvailabilityBannerComposesWithLocalGate(t *testing.T) {
	for _, password := range []bool{true, false} {
		rr := httptest.NewRecorder()
		w := availabilityWriter(rr, Availability{Mode: "banner", Message: "Platform <notice>"})
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(401)
		e := availabilityPage.Execute(w, struct {
			AR, Password, Construction bool
			Message, CSRF              string
		}{false, password, !password, "Local notice", "csrf"})
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(rr.Body.String(), "Platform &lt;notice&gt;") || !strings.Contains(rr.Body.String(), "Local notice") || rr.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("platform banner disappeared on local gate")
		}
	}
}
