package mailer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

const resendEndpoint = "https://api.resend.com/emails"
const maxResendRequest = 32 << 20

type resendAttachment struct {
	Content     string `json:"content"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
}
type resendMessage struct {
	From        string             `json:"from"`
	To          []string           `json:"to"`
	ReplyTo     string             `json:"reply_to,omitempty"`
	Subject     string             `json:"subject"`
	HTML        string             `json:"html,omitempty"`
	Text        string             `json:"text,omitempty"`
	Attachments []resendAttachment `json:"attachments,omitempty"`
}

func (m *Mailer) sendResend(ctx context.Context, c Config, msg Message) error {
	if c.ResendAPIKey == "" || c.From == "" {
		return errors.New("mailer: Resend requires an API key and sender address")
	}
	from, err := mail.ParseAddress(c.From)
	if err != nil {
		return errors.New("mailer: invalid sender address")
	}
	from.Name = oneLine(c.FromName)
	to, err := mail.ParseAddress(oneLine(msg.To))
	if err != nil {
		return errors.New("mailer: invalid recipient address")
	}
	p := resendMessage{From: from.String(), To: []string{to.Address}, Subject: oneLine(msg.Subject), HTML: msg.HTML, Text: msg.Text}
	if strings.TrimSpace(msg.ReplyTo) != "" {
		reply, err := mail.ParseAddress(oneLine(msg.ReplyTo))
		if err != nil {
			return errors.New("mailer: invalid reply-to address")
		}
		p.ReplyTo = reply.Address
	}
	size := len(msg.HTML) + len(msg.Text)
	for _, a := range msg.Attachments {
		size += base64.StdEncoding.EncodedLen(len(a.Data))
		if size > maxResendRequest {
			return errors.New("mailer: Resend message exceeds 32 MiB limit")
		}
		if a.Name == "" || len(a.Name) > 255 || strings.ContainsAny(a.Name, "\r\n/\\") {
			return errors.New("mailer: invalid attachment filename")
		}
		p.Attachments = append(p.Attachments, resendAttachment{Content: base64.StdEncoding.EncodeToString(a.Data), Filename: a.Name, ContentType: a.ContentType})
	}
	if size > maxResendRequest {
		return errors.New("mailer: Resend message exceeds 32 MiB limit")
	}
	body, err := json.Marshal(p)
	if err != nil {
		return errors.New("mailer: cannot encode Resend message")
	}
	if len(body) > maxResendRequest {
		return errors.New("mailer: Resend message exceeds 32 MiB limit")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendEndpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("mailer: cannot create Resend request")
	}
	req.Header.Set("Authorization", "Bearer "+c.ResendAPIKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second, Transport: m.resendTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// A timeout is an uncertain outcome. Never retry this POST automatically.
	res, err := client.Do(req)
	if err != nil {
		return errors.New("mailer: Resend request failed; delivery outcome unknown")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("mailer: Resend rejected request (HTTP %d)", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		return errors.New("mailer: invalid Resend response; delivery outcome unknown")
	}
	var out struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &out) != nil || strings.TrimSpace(out.ID) == "" || len(out.ID) > 256 {
		return errors.New("mailer: invalid Resend response; delivery outcome unknown")
	}
	return nil
}
