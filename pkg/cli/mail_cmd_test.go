package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestMailCLIRejectsInvalidStdinBeforeOpeningSite(t *testing.T) {
	// Empty/null, missing fields, unknown keys, multiple objects, header injection,
	// a subject over its bound and a JSON object over the complete input bound.
	valid := `{"to":"owner@example.test","subject":"test","text":"hello"}`
	cases := map[string]string{
		"empty": "", "null": "null", "unknown": `{"to":"owner@example.test","subject":"test","text":"hi","html":"<script>bad</script>"}`,
		"trailing": valid + `{}`, "missing text": `{"to":"owner@example.test","subject":"test"}`,
		"recipient header": `{"to":"owner@example.test\r\nBcc: victim@example.test","subject":"test","text":"hi"}`,
		"recipient list":   `{"to":"a@example.test,b@example.test","subject":"test","text":"hi"}`,
		"subject header":   `{"to":"owner@example.test","subject":"x\nBcc: victim@example.test","text":"hi"}`,
		"large subject":    `{"to":"owner@example.test","subject":"` + strings.Repeat("s", 257) + `","text":"hi"}`,
		"large message":    `{"to":"owner@example.test","subject":"x","text":"` + strings.Repeat("x", maxMailMessageBytes) + `"}`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			dir := t.TempDir()
			if err := mailCmd([]string{"send", "--message-stdin", "--data", dir}, strings.NewReader(input), &output); err == nil {
				t.Fatal("invalid message was accepted")
			}
			if output.Len() != 0 {
				t.Fatal("invalid input printed message data")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid input opened or changed site data")
			}
		})
	}
}

func TestMailCLIRequiresConfiguredTransport(t *testing.T) {
	t.Setenv("GORUN_ENV", "dev")
	var output bytes.Buffer
	err := Run([]string{"mail", "send", "--message-stdin", "--data", t.TempDir()}, strings.NewReader(`{"to":"owner@example.test","subject":"test","text":"private body"}`), &output)
	if err == nil || !strings.Contains(err.Error(), "configured delivery provider required") || output.Len() != 0 {
		t.Fatalf("unconfigured mail must fail, not print acceptance: %v %s", err, output.String())
	}
}

type cliSMTPObservation struct {
	message string
	err     error
}

// This SMTP boundary accepts an encrypted saved credential and one message;
// no HTTP server, API key, mailer interface mock or job worker is involved.
func cliSMTPFixture(t *testing.T, reject bool) (string, <-chan cliSMTPObservation) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	observations := make(chan cliSMTPObservation, 1)
	go func() {
		observation := cliSMTPObservation{}
		defer func() { observations <- observation }()
		conn, err := listener.Accept()
		if err != nil {
			observation.err = err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := textproto.NewReader(bufio.NewReader(conn))
		reply := func(line string) { fmt.Fprint(conn, line+"\r\n") }
		reply("220 CLI SMTP fixture")
		for {
			line, err := reader.ReadLine()
			if err != nil {
				if err != io.EOF {
					observation.err = err
				}
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				reply("250-localhost\r\n250 AUTH PLAIN")
			case strings.HasPrefix(line, "AUTH PLAIN "):
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
				if err != nil || string(decoded) != "\x00ops-user\x00saved-mail-password" {
					observation.err = fmt.Errorf("encrypted mail credential was not used")
					reply("535 authentication failed")
					return
				}
				reply("235 authenticated")
			case strings.HasPrefix(line, "RCPT TO:") && reject:
				reply("550 reflected saved-mail-password private-body")
			case line == "DATA":
				reply("354 send message")
				encoded, err := reader.ReadDotBytes()
				observation.message, observation.err = string(encoded), err
				reply("250 accepted")
			case line == "QUIT":
				reply("221 bye")
				return
			default:
				reply("250 ok")
			}
		}
	}()
	return listener.Addr().String(), observations
}

func configureCLIMailFixture(t *testing.T, dir, address string) {
	t.Helper()
	t.Setenv("GORUN_ENV", "dev")
	cfg, err := config.Load([]string{"--data", dir})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.BootWithOptions(context.Background(), cfg, []kernel.Module{settingsmod.New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	host, port, _ := net.SplitHostPort(address)
	number, _ := strconv.Atoi(port)
	secret, err := mailer.EncryptSecret(cfg.SecretKey, "saved-mail-password")
	if err != nil {
		t.Fatal(err)
	}
	for key, saved := range map[string]any{"mail.host": host, "mail.port": number, "mail.username": "ops-user", "mail.password": secret, "mail.from": "site@example.test", "mail.from_name": "مرسى", "mail.tls": "none"} {
		if err := app.Deps.Settings.Set(context.Background(), key, saved); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Deps.Jobs.Enqueue(context.Background(), "operator.pending", map[string]string{"keep": "queued"}); err != nil {
		t.Fatal(err)
	}
}

func TestMailCLISendsWithSavedEncryptedConfigWithoutHTTPOrWorkers(t *testing.T) {
	address, observations := cliSMTPFixture(t, false)
	dir := t.TempDir()
	configureCLIMailFixture(t, dir, address)
	var output bytes.Buffer
	input := `{"to":"owner@example.test","subject":"Marsa alert","text":"private-body <script>alert(1)</script>\nSecond line"}`
	if err := Run([]string{"mail", "send", "--message-stdin", "--data", dir}, strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	var outcome struct {
		Accepted bool   `json:"accepted"`
		Delivery string `json:"delivery"`
	}
	if err := json.Unmarshal(output.Bytes(), &outcome); err != nil || !outcome.Accepted || outcome.Delivery != "not_verified" {
		t.Fatalf("provider acceptance must remain separate from inbox delivery: %s, %v", output.String(), err)
	}
	select {
	case observation := <-observations:
		if observation.err != nil {
			t.Fatal(observation.err)
		}
		message, err := mail.ReadMessage(strings.NewReader(observation.message))
		if err != nil || message.Header.Get("Subject") != "Marsa alert" {
			t.Fatalf("SMTP message headers: %v", err)
		}
		htmlBody := cliMessageHTML(t, message)
		if !strings.Contains(htmlBody, "&lt;script&gt;") || strings.Contains(htmlBody, "<script>") {
			t.Fatal("plaintext input became executable HTML")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP did not receive a message")
	}
	database, err := openDB([]string{"--data", dir})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var pending int
	if err := database.QueryRow("select count(*) from jobs where kind='operator.pending' and status='queued'").Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("mail command consumed unrelated queued work: %d %v", pending, err)
	}
	for _, secret := range []string{"private-body", "owner@example.test", "saved-mail-password"} {
		if strings.Contains(output.String(), secret) {
			t.Fatal("mail command output leaked input or credentials")
		}
	}
}

func TestMailCLIProviderRejectionFailsWithoutLeakingResponse(t *testing.T) {
	address, observations := cliSMTPFixture(t, true)
	dir := t.TempDir()
	configureCLIMailFixture(t, dir, address)
	var output bytes.Buffer
	err := Run([]string{"mail", "send", "--message-stdin", "--data", dir}, strings.NewReader(`{"to":"owner@example.test","subject":"alert","text":"private-body"}`), &output)
	if err == nil || output.Len() != 0 || strings.Contains(err.Error(), "saved-mail-password") || strings.Contains(err.Error(), "private-body") {
		t.Fatalf("provider rejection must fail without response reflection: %v", err)
	}
	select {
	case observation := <-observations:
		if observation.message != "" {
			t.Fatal("rejected envelope received a message")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP client did not close after rejection")
	}
}

func cliMessageHTML(t *testing.T, message *mail.Message) string {
	t.Helper()
	_, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	parts := multipart.NewReader(message.Body, params["boundary"])
	for {
		part, err := parts.NextPart()
		if err == io.EOF {
			t.Fatal("SMTP message is missing HTML alternative")
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(part.Header.Get("Content-Type"), "text/html") {
			body, err := io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			return string(body)
		}
	}
}

func TestMailCLICannotBeOverriddenByExtension(t *testing.T) {
	Configure(WithCommands(Command{Name: "mail", Run: func([]string, io.Reader, io.Writer) error { return nil }}))
	t.Cleanup(func() { Configure() })
	var output bytes.Buffer
	err := Run([]string{"mail"}, strings.NewReader(""), &output)
	if err == nil || !strings.Contains(err.Error(), mailSendUsage) || output.Len() != 0 {
		t.Fatalf("extension replaced the core mail command: %v", err)
	}
}
