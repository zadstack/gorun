package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"strconv"

	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/services"
)

const mailSetUsage = "usage: gorun mail set --provider smtp|resend [--host H] [--port N] [--tls starttls|tls|none] [--username U] [--from ADDR] [--from-name N] [--reply-to ADDR] [--secret-stdin | --clear-secret] [--data dir]"

// mailSetFields maps each mail set flag to its gorun service set mail field.
var mailSetFields = map[string]string{
	"provider": "provider", "host": "host", "port": "port", "tls": "tls", "username": "username",
	"from": "from", "from-name": "from_name", "reply-to": "reply_to",
}

// mailSetCmd is gorun mail set, kept from 0.27 as an alias of gorun service
// set mail: each flag given becomes that field (fields not given keep their
// stored value), validated with the admin's own admin.MailConfig. The secret
// (SMTP password or Resend API key) comes from the first line of stdin only
// (--secret-stdin), is stored encrypted and is never printed; --clear-secret
// removes it; otherwise the stored one is kept.
func mailSetCmd(args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("mail set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	provider := fs.String("provider", "", "smtp or resend")
	fs.String("host", "", "SMTP server")
	port := fs.Int("port", 0, "SMTP port (587 when 0)")
	fs.String("tls", mailer.TLSStartTLS, "starttls, tls (implicit) or none")
	fs.String("username", "", "SMTP user")
	fs.String("from", "", "sender address")
	fs.String("from-name", "", "sender name")
	fs.String("reply-to", "", "reply-to address")
	secretStdin := fs.Bool("secret-stdin", false, "read the SMTP password or Resend API key from the first line of stdin")
	clearSecret := fs.Bool("clear-secret", false, "remove the stored secret")
	dataDir := fs.String("data", "", "data directory")
	if err := fs.Parse(args); err != nil || len(fs.Args()) != 0 || *provider == "" || (*secretStdin && *clearSecret) {
		return errors.New(mailSetUsage)
	}
	values := map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		if field, ok := mailSetFields[f.Name]; ok {
			values[field] = f.Value.String()
		}
	})
	if _, ok := values["port"]; ok {
		values["port"] = strconv.Itoa(*port)
		if *port == 0 {
			values["port"] = "" // the form's default, 587
		}
	}
	var secret *string
	switch {
	case *secretStdin:
		s, err := readServiceSecret(in)
		if err != nil {
			return err
		}
		secret = &s
	case *clearSecret:
		empty := ""
		secret = &empty
	}
	return withServices(*dataDir, func(ctx context.Context, r *services.Registry) error {
		return setService(ctx, r, "mail", values, secret, out)
	})
}
