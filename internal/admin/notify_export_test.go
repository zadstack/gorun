package admin

import (
	"context"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// FakeMail records notification emails instead of sending them.
type FakeMail struct {
	mu           sync.Mutex
	Unconfigured bool
	Fail         error
	sent         []FakeMessage
}

// FakeMessage is one recorded email.
type FakeMessage struct{ To, Subject, HTML, Text string }

func (f *FakeMail) Configured(context.Context) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.Unconfigured
}

func (f *FakeMail) SendRequired(_ context.Context, to, subject, html, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return f.Fail
	}
	f.sent = append(f.sent, FakeMessage{To: to, Subject: subject, HTML: html, Text: text})
	return nil
}

// Messages returns the recorded emails.
func (f *FakeMail) Messages() []FakeMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeMessage(nil), f.sent...)
}

// NotifyHarness drives the delivery service in tests. Boot without job
// workers so the cron does not run concurrently with these setters.
type NotifyHarness struct{ s *notifyDelivery }

// Notify returns the delivery service of app.
func Notify(app *kernel.App) *NotifyHarness {
	s, ok := kernel.Get[*notifyDelivery](app.Deps, notifyServiceName)
	if !ok {
		panic("notify delivery service missing")
	}
	return &NotifyHarness{s: s}
}

func (n *NotifyHarness) UseMail(f *FakeMail) { n.s.mail = f }

// UseTelegram points the Bot API client at a local test server and makes
// retries immediate; it records each backoff wait.
func (n *NotifyHarness) UseTelegram(base string, waits *[]time.Duration) {
	n.s.tg.base = base
	n.s.tg.http = httpx.NewSafeClientOpts(httpx.SafeClientOptions{AllowPrivate: true, NoRedirects: true, Timeout: 5 * time.Second})
	n.s.tg.sleep = func(_ context.Context, d time.Duration) error {
		if waits != nil {
			*waits = append(*waits, d)
		}
		return nil
	}
}

func (n *NotifyHarness) SetNow(t time.Time) { n.s.now = func() time.Time { return t } }

func (n *NotifyHarness) Run(ctx context.Context) (users, sent, failed int, err error) {
	st, err := n.s.Run(ctx)
	return st.Users, st.Sent, st.Failed, err
}

func (n *NotifyHarness) UnsubscribeToken(userID string) string { return n.s.unsubscribeToken(userID) }

// Webhook returns the webhook path segment and secret header value.
func (n *NotifyHarness) Webhook(ctx context.Context) (path, header string) {
	sec := n.s.webhookSecret(ctx)
	if sec == nil {
		return "", ""
	}
	return webhookPath(sec), webhookHeader(sec)
}

func (n *NotifyHarness) NewLinkCode(ctx context.Context, userID string) (string, error) {
	code, _, err := n.s.newLinkCode(ctx, userID)
	return code, err
}
