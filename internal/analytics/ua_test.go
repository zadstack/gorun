package analytics

import (
	"strings"
	"testing"
)

func TestParseUAFamilies(t *testing.T) {
	cases := []struct {
		name, ua, browser, os string
	}{
		{"chrome windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36", "Chrome", "Windows"},
		{"chrome mac", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36", "Chrome", "macOS"},
		{"chrome android", "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36", "Chrome", "Android"},
		{"chrome ios", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0.0.0 Mobile/15E148 Safari/604.1", "Chrome", "iOS"},
		{"chrome linux", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36", "Chrome", "Linux"},
		{"safari mac", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", "Safari", "macOS"},
		{"safari iphone", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "Safari", "iOS"},
		{"safari ipad", "Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "Safari", "iOS"},
		{"firefox windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0", "Firefox", "Windows"},
		{"firefox linux", "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0", "Firefox", "Linux"},
		{"firefox ios", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/132.0 Mobile/15E148 Safari/605.1.15", "Firefox", "iOS"},
		{"edge windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0", "Edge", "Windows"},
		{"edge android", "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36 EdgA/130.0.0.0", "Edge", "Android"},
		{"samsung", "Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0.0.0 Mobile Safari/537.36", "Samsung", "Android"},
		{"opera", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 OPR/115.0.0.0", "Opera", "Windows"},
		{"unknown", "SomethingElse/1.0", "other", "other"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, o, bot := parseUA(c.ua)
			if b != c.browser || o != c.os || bot {
				t.Fatalf("parseUA = %q, %q, bot=%v; want %q, %q, false", b, o, bot, c.browser, c.os)
			}
		})
	}
}

func TestParseUABots(t *testing.T) {
	for _, ua := range []string{
		"",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)",
		"Mozilla/5.0 (compatible; YandexSpider/3.0)",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/130.0.0.0 Safari/537.36",
		"Mozilla/5.0 (compatible; UptimeRobot/2.0; http://www.uptimerobot.com/)",
		"Pingdom.com_bot_version_1.4_(http://www.pingdom.com/)",
		"facebookexternalhit/1.1 (+http://www.facebook.com/externalhit_uatext.php)",
		"curl/8.7.1",
		"Go-http-client/1.1",
		"python-requests/2.32.3",
		"SomeCrawler/1.0",
	} {
		if _, _, bot := parseUA(ua); !bot {
			t.Errorf("parseUA(%q): not a bot", ua)
		}
	}
}

func TestDeviceBuckets(t *testing.T) {
	for w, want := range map[int]string{1: "mobile", 320: "mobile", 767: "mobile", 768: "tablet", 1023: "tablet", 1024: "desktop", 2560: "desktop"} {
		if got := deviceFor(w); got != want {
			t.Errorf("deviceFor(%d) = %q, want %q", w, got, want)
		}
	}
	if got := deviceFor(0); got != "unknown" {
		t.Errorf("deviceFor(0) = %q, want unknown", got)
	}
}

func TestRefHostSelfIsDirect(t *testing.T) {
	cases := map[string]string{
		"":                                "direct",
		"http://site.test/a":              "direct",
		"https://SITE.test:8443/b?x=1":    "direct",
		"https://www.google.com/search?q": "www.google.com",
		"https://News.Example.org:443/x":  "news.example.org",
		"android-app://com.slack/":        "com.slack",
		"not a url":                       "direct",
		"/relative/path":                  "direct",
	}
	for ref, want := range cases {
		if got := refHost(ref, "site.test"); got != want {
			t.Errorf("refHost(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestCleanPath(t *testing.T) {
	p, utm := cleanPath("/a?token=x&utm_source=nl&utm_medium=email&utm_campaign=fall#frag")
	if p != "/a" || utm != [3]string{"nl", "email", "fall"} {
		t.Fatalf("cleanPath = %q %v", p, utm)
	}
	if p, _ := cleanPath("https://evil.test/a"); p != "" {
		t.Errorf("absolute URL accepted: %q", p)
	}
	if p, _ := cleanPath(""); p != "" {
		t.Errorf("empty accepted: %q", p)
	}
	long := "/" + strings.Repeat("x", 599)
	if p, _ := cleanPath(long); len(p) != maxPath {
		t.Errorf("long path len = %d, want %d", len(p), maxPath)
	}
	// Paths are stored canonical (percent-encoded), and a cut never splits
	// an escape or the escapes of one UTF-8 character.
	multi := "/" + strings.Repeat("é", 300)
	if p, _ := cleanPath(multi); len(p) > maxPath || !strings.HasSuffix(p, "%C3%A9") {
		t.Errorf("multibyte cut: len %d, ends %q", len(p), p[max(0, len(p)-6):])
	}
}
