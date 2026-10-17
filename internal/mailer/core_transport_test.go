package mailer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func smtpFixtureConfig(t *testing.T, addr string, mode string) Config {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return Config{Host: host, Port: number, TLS: mode, From: "sender@example.invalid", Provider: "smtp"}
}

func TestSMTPHonorsContextAfterConnect(t *testing.T) {
	for _, mode := range []string{"deadline", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			accepted := make(chan net.Conn, 1)
			go func() {
				c, err := listener.Accept()
				if err != nil {
					return
				}
				accepted <- c
				io.Copy(io.Discard, c)
			}()
			var ctx context.Context
			var cancel context.CancelFunc
			if mode == "deadline" {
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- smtpSend(ctx, smtpFixtureConfig(t, listener.Addr().String(), TLSNone), "sender@example.invalid", "recipient@example.invalid", []byte("fixture message"))
			}()
			var conn net.Conn
			select {
			case conn = <-accepted:
			case <-time.After(time.Second):
				t.Fatal("local SMTP connection was not accepted")
			}
			defer conn.Close()
			if mode == "cancellation" {
				cancel()
			}
			select {
			case err := <-result:
				expected := context.DeadlineExceeded
				if mode == "cancellation" {
					expected = context.Canceled
				}
				if !errors.Is(err, expected) {
					t.Fatalf("SMTP cancellation cause not retained: %T", err)
				}
			case <-time.After(350 * time.Millisecond):
				conn.Close()
				<-result
				t.Fatal("SMTP ignored context after connecting")
			}
		})
	}
}

func TestSMTPRefusesMissingSTARTTLSAndUntrustedTLS(t *testing.T) {
	t.Run("STARTTLS required", func(t *testing.T) {
		srv := startFakeSMTP(t)
		err := smtpSend(context.Background(), smtpFixtureConfig(t, srv.ln.Addr().String(), TLSStartTLS), "sender@example.invalid", "recipient@example.invalid", []byte("fixture"))
		if err == nil {
			t.Fatal("missing STARTTLS accepted")
		}
		<-srv.done
		srv.mu.Lock()
		defer srv.mu.Unlock()
		if srv.from != "" || srv.data != "" {
			t.Fatal("message transmitted before required TLS")
		}
	})
	t.Run("implicit TLS untrusted", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("SMTP certificate failure reached application handler")
		}))
		defer srv.Close()
		u, _ := url.Parse(srv.URL)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := smtpSend(ctx, smtpFixtureConfig(t, u.Host, TLSImplicit), "sender@example.invalid", "recipient@example.invalid", []byte("fixture")); err == nil {
			t.Fatal("untrusted TLS accepted")
		}
	})
}

func TestSMTPProviderErrorCannotEchoCredentials(t *testing.T) {
	const password = "synthetic-smtp-password-echo"
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		io.WriteString(conn, "220 local fixture\r\n")
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				io.WriteString(conn, "250-fixture\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(line, "AUTH"):
				io.WriteString(conn, "535 rejected "+password+"\r\n")
			case strings.HasPrefix(line, "*"):
				io.WriteString(conn, "501 cancelled\r\n")
			case strings.HasPrefix(line, "QUIT"):
				io.WriteString(conn, "221 bye\r\n")
				return
			default:
				io.WriteString(conn, "500 fixture command rejected\r\n")
			}
		}
	}()
	cfg := smtpFixtureConfig(t, listener.Addr().String(), TLSNone)
	cfg.Username = "fixture-user"
	cfg.Password = password
	store := newStore(t)
	log, _ := bufLogger()
	key := bytes.Repeat([]byte{9}, 32)
	enc, err := EncryptSecret(key, password)
	if err != nil {
		t.Fatal(err)
	}
	m := New(store, log).WithKey(key)
	for k, v := range map[string]any{"mail.host": cfg.Host, "mail.port": cfg.Port, "mail.tls": cfg.TLS, "mail.from": cfg.From, "mail.username": cfg.Username, "mail.password": enc} {
		if err := store.Set(context.Background(), k, v); err != nil {
			t.Fatal(err)
		}
	}
	// Inject the already configured synthetic credentials; settings secrets are
	// covered separately and the only transport is this local SMTP sink.
	m.transport = func(ctx context.Context, _ Config, from, to string, msg []byte) error {
		return smtpSend(ctx, cfg, from, to, msg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = m.Send(ctx, "recipient@example.invalid", "Fixture", "<p>Fixture</p>", "Fixture")
	if err == nil {
		t.Fatal("authentication rejection accepted")
	}
	if strings.Contains(err.Error(), password) {
		t.Fatal("SMTP provider error exposed the synthetic password")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("local SMTP fixture did not close")
	}
}

func TestMailSettingsRemainIsolatedBetweenSites(t *testing.T) {
	key := bytes.Repeat([]byte{8}, 32)
	left, right := newStore(t), newStore(t)
	log, _ := bufLogger()
	for _, tc := range []struct {
		store interface {
			Set(context.Context, string, any) error
		}
		host, pass string
	}{{left, "left.example.invalid", "left-fixture-secret"}, {right, "right.example.invalid", "right-fixture-secret"}} {
		enc, err := EncryptSecret(key, tc.pass)
		if err != nil {
			t.Fatal(err)
		}
		if err := tc.store.Set(context.Background(), "mail.host", tc.host); err != nil {
			t.Fatal(err)
		}
		if err := tc.store.Set(context.Background(), "mail.password", enc); err != nil {
			t.Fatal(err)
		}
	}
	a, err := New(left, log).WithKey(key).Config(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(right, log).WithKey(key).Config(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a.Host == b.Host || a.Password == b.Password || a.Password == "" || b.Password == "" {
		t.Fatal("mail configuration leaked across fixture sites")
	}
}
