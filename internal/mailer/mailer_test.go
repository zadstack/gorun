package mailer

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/settings"
)

func newStore(t *testing.T) *settings.Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d, "settings", settings.Migrations()); err != nil {
		t.Fatal(err)
	}
	return settings.New(d)
}

func bufLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func TestMailerLogsWhenSMTPUnset(t *testing.T) {
	log, buf := bufLogger()
	m := New(newStore(t), log).WithEnv("dev")
	if err := m.Send(context.Background(), "a@example.com", "Hi", "<p>Hi</p>", "visit https://x.test/auth/magic/abc"); err != nil {
		t.Fatalf("send without smtp must not fail: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"level=INFO", "mail not configured; link/body logged", "a@example.com", "https://x.test/auth/magic/abc"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}

// Outside dev the body (which may hold a sign-in link) is never logged.
func TestMailerProdDoesNotLogBody(t *testing.T) {
	for _, env := range []string{"prod", ""} {
		log, buf := bufLogger()
		m := New(newStore(t), log).WithEnv(env)
		if m.Configured(context.Background()) {
			t.Fatal("no mail.host: must not be configured")
		}
		if err := m.Send(context.Background(), "a@example.com", "Sign in", "<p>https://x.test/auth/magic/abc</p>", "visit https://x.test/auth/magic/abc"); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if strings.Contains(out, "auth/magic/abc") {
			t.Fatalf("env %q logged the body:\n%s", env, out)
		}
		for _, want := range []string{"level=WARN", "mail not configured", "a@example.com", "Sign in"} {
			if !strings.Contains(out, want) {
				t.Fatalf("env %q log missing %q:\n%s", env, want, out)
			}
		}
	}
}

func TestEncryptDecryptSecret(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	enc, err := EncryptSecret(key, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, "s3cret") || !strings.HasPrefix(enc, encPrefix) {
		t.Fatalf("not encrypted: %q", enc)
	}
	enc2, _ := EncryptSecret(key, "s3cret")
	if enc == enc2 {
		t.Fatal("nonce must be random")
	}
	got, err := DecryptSecret(key, enc)
	if err != nil || got != "s3cret" {
		t.Fatalf("decrypt: %q %v", got, err)
	}
	if _, err := DecryptSecret(bytes.Repeat([]byte{8}, 32), enc); err == nil {
		t.Fatal("wrong key must fail")
	}
	if _, err := EncryptSecret([]byte("short"), "x"); err == nil {
		t.Fatal("bad key size must fail")
	}
}

func TestRenderTemplatesBothLocales(t *testing.T) {
	m := New(nil, slog.Default())
	data := map[string]any{"Name": "Ahmed", "Link": "https://x.test/auth/magic/tok", "SiteTitle": "ZAD", "TTLMinutes": 15}
	for _, loc := range []string{"en", "ar"} {
		subj, html, text, err := m.Render(loc, "magic_link", data)
		if err != nil {
			t.Fatalf("%s: %v", loc, err)
		}
		if subj == "" || !strings.Contains(html, "https://x.test/auth/magic/tok") || !strings.Contains(text, "https://x.test/auth/magic/tok") {
			t.Fatalf("%s: subject %q html %q text %q", loc, subj, html, text)
		}
		if loc == "ar" && !strings.Contains(html, `dir="rtl"`) {
			t.Fatalf("arabic html must be rtl: %s", html)
		}
		if _, _, _, err := m.Render(loc, "test", data); err != nil {
			t.Fatalf("%s test template: %v", loc, err)
		}
	}
	if subj, _, _, err := m.Render("fr", "magic_link", data); err != nil || subj == "" {
		t.Fatalf("unknown locale must fall back to en: %q %v", subj, err)
	}
	if _, _, _, err := m.Render("en", "nope", data); err == nil {
		t.Fatal("unknown template must fail")
	}
	data["Name"] = "<script>"
	_, html, _, _ := m.Render("en", "magic_link", data)
	if strings.Contains(html, "<script>") {
		t.Fatal("html template must escape data")
	}
}

// fakeSMTP is a minimal plaintext SMTP server that records one message.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	from string
	to   string
	data string
	done chan struct{}
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, done: make(chan struct{})}
	t.Cleanup(func() { ln.Close() })
	go f.serve()
	return f
}

func (f *fakeSMTP) serve() {
	defer close(f.done)
	c, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer c.Close()
	r := bufio.NewReader(c)
	reply := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	reply("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			reply("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			f.mu.Lock()
			f.from = strings.TrimSpace(line[len("MAIL FROM:"):])
			f.mu.Unlock()
			reply("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			f.mu.Lock()
			f.to = strings.TrimSpace(line[len("RCPT TO:"):])
			f.mu.Unlock()
			reply("250 ok")
		case cmd == "DATA":
			reply("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			reply("250 queued")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func TestSendDeliversOverSMTP(t *testing.T) {
	srv := startFakeSMTP(t)
	host, port, _ := net.SplitHostPort(srv.ln.Addr().String())
	st := newStore(t)
	ctx := context.Background()
	p, _ := strconv.Atoi(port)
	for k, v := range map[string]any{"mail.host": host, "mail.port": p, "mail.from": "noreply@example.com", "mail.from_name": "زاد", "mail.tls": "none"} {
		if err := st.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	log, _ := bufLogger()
	m := New(st, log).WithKey(make([]byte, 32))
	if err := m.Send(ctx, "user@example.com", "Hello\r\nBcc: evil@x.test", "<p>Hi ✓</p>", "Hi plain"); err != nil {
		t.Fatal(err)
	}
	<-srv.done
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.from != "<noreply@example.com>" || srv.to != "<user@example.com>" {
		t.Fatalf("envelope: %q → %q", srv.from, srv.to)
	}
	for _, want := range []string{"Subject: Hello Bcc: evil@x.test", "multipart/alternative", "Hi plain", "To: <user@example.com>", "MIME-Version: 1.0"} {
		if !strings.Contains(srv.data, want) {
			t.Fatalf("message missing %q:\n%s", want, srv.data)
		}
	}
	if strings.Contains(srv.data, "\r\nBcc:") {
		t.Fatalf("header injection:\n%s", srv.data)
	}
}

func TestSendRejectsBadRecipient(t *testing.T) {
	st := newStore(t)
	_ = st.Set(context.Background(), "mail.host", "127.0.0.1")
	m := New(st, slog.Default())
	if err := m.Send(context.Background(), "not-an-email", "s", "h", "t"); err == nil {
		t.Fatal("expected error for invalid recipient")
	}
}

func TestConfigDecryptsPassword(t *testing.T) {
	st := newStore(t)
	key := bytes.Repeat([]byte{3}, 32)
	enc, _ := EncryptSecret(key, "pw")
	ctx := context.Background()
	_ = st.Set(ctx, "mail.host", "smtp.example.com")
	_ = st.Set(ctx, "mail.port", "2525")
	_ = st.Set(ctx, "mail.password", enc)
	c, err := New(st, slog.Default()).WithKey(key).Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.Password != "pw" || c.Port != 2525 || c.TLS != "starttls" {
		t.Fatalf("config: %+v", c)
	}
}

func TestSendMessageSetsReplyTo(t *testing.T) {
	srv := startFakeSMTP(t)
	host, port, _ := net.SplitHostPort(srv.ln.Addr().String())
	st := newStore(t)
	ctx := context.Background()
	p, _ := strconv.Atoi(port)
	for k, v := range map[string]any{"mail.host": host, "mail.port": p, "mail.from": "noreply@example.com", "mail.tls": "none"} {
		if err := st.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	log, _ := bufLogger()
	m := New(st, log).WithKey(make([]byte, 32))
	if err := m.SendMessage(ctx, Message{To: "owner@example.com", ReplyTo: "Visitor <visitor@example.org>", Subject: "Contact", HTML: "<p>x</p>", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	<-srv.done
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !strings.Contains(srv.data, "Reply-To: \"Visitor\" <visitor@example.org>") {
		t.Fatalf("Reply-To missing:\n%s", srv.data)
	}
	if err := m.SendMessage(ctx, Message{To: "owner@example.com", ReplyTo: "not an address", Subject: "s", Text: "t"}); err == nil {
		t.Fatal("invalid reply-to must fail")
	}
}
