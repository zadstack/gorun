package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type resendRoundTrip func(*http.Request) (*http.Response, error)

func (f resendRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func resendFixture(t *testing.T) *Mailer {
	t.Helper()
	s := newStore(t)
	key := bytes.Repeat([]byte{3}, 32)
	enc, err := EncryptSecret(key, "re_private_test_key")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"mail.provider": "resend", "mail.resend_api_key": enc, "mail.from": "info@example.invalid", "mail.from_name": "GoRun"} {
		if err := s.Set(context.Background(), k, v); err != nil {
			t.Fatal(err)
		}
	}
	l, _ := bufLogger()
	return New(s, l).WithKey(key)
}
func TestResendSelectedPayload(t *testing.T) {
	m := resendFixture(t)
	calls := 0
	m.resendTransport = resendRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.resend.com/emails" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer re_private_test_key" {
			t.Fatal("wrong destination/auth")
		}
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p["reply_to"] != "reply@example.invalid" || p["text"] != "hello" || p["subject"] != "Subject" {
			t.Fatalf("bad payload %v", p)
		}
		at := p["attachments"].([]any)[0].(map[string]any)
		if at["content"] != "aGk=" || at["filename"] != "receipt.txt" {
			t.Fatal("bad attachment")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"message-id"}`)), Header: http.Header{}}, nil
	})
	if !m.Configured(context.Background()) {
		t.Fatal("configured")
	}
	if err := m.SendMessage(context.Background(), Message{To: "to@example.invalid", ReplyTo: "reply@example.invalid", Subject: "Subject", Text: "hello", HTML: "<p>hello</p>", Attachments: []Attachment{{Name: "receipt.txt", Data: []byte("hi")}}}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestResendFailureRedactionNoRetry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		network bool
	}{{"rejected", 422, `{"message":"re_private_test_key private message"}`, false}, {"redirect", 307, ``, false}, {"malformed", 200, `not json`, false}, {"missing id", 200, `{}`, false}, {"network", 0, "", true}} {
		t.Run(tc.name, func(t *testing.T) {
			m := resendFixture(t)
			calls := 0
			m.resendTransport = resendRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if tc.network {
					return nil, errors.New("re_private_test_key private message")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{"Location": []string{"https://attacker.invalid"}}}, nil
			})
			err := m.Send(context.Background(), "to@example.invalid", "hi", "", "body")
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe error %v", err)
			}
			if calls != 1 {
				t.Fatal("retried", calls)
			}
		})
	}
}
func TestResendUnconfiguredFailsClosed(t *testing.T) {
	m := resendFixture(t)
	if err := m.settings.Set(context.Background(), "mail.resend_api_key", ""); err != nil {
		t.Fatal(err)
	}
	if m.Configured(context.Background()) {
		t.Fatal("configured without key")
	}
	if err := m.Send(context.Background(), "to@example.invalid", "hi", "", "body"); err == nil {
		t.Fatal("must fail instead of SMTP/log fallback")
	}
}
func TestResendDoesNotDecryptInactiveSMTPPassword(t *testing.T) {
	m := resendFixture(t)
	if err := m.settings.Set(context.Background(), "mail.password", "broken"); err != nil {
		t.Fatal(err)
	}
	c, err := m.Config(context.Background())
	if err != nil || c.ResendAPIKey != "re_private_test_key" {
		t.Fatal("inactive provider blocks active", err)
	}
}

func TestResendBoundsAndNoTransportOnInvalidMessage(t *testing.T) {
	for _, msg := range []Message{{To: "bad address", Text: "hello"}, {To: "to@example.invalid", Text: strings.Repeat("x", maxResendRequest+1)}, {To: "to@example.invalid", Attachments: []Attachment{{Name: "../secret.txt", Data: []byte("x")}}}} {
		m := resendFixture(t)
		m.resendTransport = resendRoundTrip(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid message reached transport")
			return nil, nil
		})
		if err := m.SendMessage(context.Background(), msg); err == nil {
			t.Fatal("invalid accepted")
		}
	}
	m := resendFixture(t)
	m.resendTransport = resendRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", (64<<10)+1)))}, nil
	})
	if err := m.Send(context.Background(), "to@example.invalid", "hi", "", "body"); err == nil {
		t.Fatal("oversized response accepted")
	}
}
func TestResendCanceledContextIsBounded(t *testing.T) {
	m := resendFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.resendTransport = resendRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Context().Err() != context.Canceled {
			t.Fatal("context lost")
		}
		return nil, r.Context().Err()
	})
	if err := m.Send(ctx, "to@example.invalid", "hi", "", "body"); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestInvalidProviderSettingCannotFallBackToSMTP(t *testing.T) {
	m := resendFixture(t)
	if err := m.settings.Set(context.Background(), "mail.provider", 123); err != nil {
		t.Fatal(err)
	}
	if err := m.Send(context.Background(), "to@example.invalid", "hi", "", "body"); err == nil {
		t.Fatal("invalid provider silently treated as unconfigured SMTP")
	}
}
