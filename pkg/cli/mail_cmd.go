package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/mail"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

const (
	mailSendUsage         = "usage: gorun mail send --message-stdin [--data dir]"
	maxMailMessageBytes   = 32 << 10
	maxMailSubjectBytes   = 256
	maxMailRecipientBytes = 254
	mailSendTimeout       = 45 * time.Second
)

type cliMailMessage struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
}

// mailCmd uses the site's encrypted settings even when the HTTP process is down.
// It starts no workers: an operational alert must not replay unrelated jobs.
func mailCmd(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] != "send" {
		return errors.New(mailSendUsage)
	}
	fs := flag.NewFlagSet("mail send", flag.ContinueOnError)
	messageStdin := fs.Bool("message-stdin", false, "read one to/subject/text JSON object from stdin")
	dataDir := fs.String("data", "", "data directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !*messageStdin || len(fs.Args()) != 0 {
		return errors.New(mailSendUsage)
	}
	message, err := readCLIMailMessage(in)
	if err != nil {
		return err
	}
	return sendCLIMail(message, *dataDir, out)
}

func readCLIMailMessage(in io.Reader) (cliMailMessage, error) {
	var message cliMailMessage
	encoded, err := io.ReadAll(io.LimitReader(in, maxMailMessageBytes+1))
	if err != nil {
		return message, errors.New("mail: cannot read message from stdin")
	}
	if len(encoded) > maxMailMessageBytes {
		return message, errors.New("mail: message JSON exceeds 32 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		return message, errors.New("mail: expected one JSON object containing only to, subject and text")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return message, errors.New("mail: trailing message data is not allowed")
	}
	return message, validateCLIMailMessage(message)
}

func validateCLIMailMessage(message cliMailMessage) error {
	address, err := mail.ParseAddress(message.To)
	if err != nil || address.Address != message.To || len(message.To) > maxMailRecipientBytes || strings.ContainsAny(message.To, "\r\n") {
		return errors.New("mail: to must be one plain email address")
	}
	if strings.TrimSpace(message.Subject) == "" || len(message.Subject) > maxMailSubjectBytes || strings.ContainsAny(message.Subject, "\r\n") {
		return errors.New("mail: subject must be non-empty, one line and at most 256 bytes")
	}
	if strings.TrimSpace(message.Text) == "" {
		return errors.New("mail: text must not be empty")
	}
	return nil
}

func sendCLIMail(message cliMailMessage, dataDir string, out io.Writer) error {
	var cfgArgs []string
	if dataDir != "" {
		cfgArgs = []string{"--data", dataDir}
	}
	cfg, err := config.Load(cfgArgs)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), mailSendTimeout)
	defer cancel()
	app, err := kernel.BootWithOptions(ctx, cfg, []kernel.Module{settingsmod.New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		return err
	}
	defer app.Close()
	return deliverCLIMail(ctx, app.Deps, message, out)
}

func deliverCLIMail(ctx context.Context, deps *kernel.Deps, message cliMailMessage, out io.Writer) error {
	sender := mailer.New(deps.Settings, deps.Log).WithKey(deps.Config.SecretKey).WithEnv(deps.Config.Env)
	body := "<p>" + strings.ReplaceAll(html.EscapeString(message.Text), "\n", "<br>\n") + "</p>"
	if err := sender.SendRequired(ctx, message.To, message.Subject, body, message.Text); err != nil {
		deps.Audit.Record(ctx, "mail.cli.failed", "mail", "", nil)
		return fmt.Errorf("mail: send failed: %w", err)
	}
	deps.Audit.Record(ctx, "mail.cli.accepted", "mail", "", nil)
	return json.NewEncoder(out).Encode(struct {
		Accepted bool   `json:"accepted"`
		Delivery string `json:"delivery"`
	}{Accepted: true, Delivery: "not_verified"})
}
