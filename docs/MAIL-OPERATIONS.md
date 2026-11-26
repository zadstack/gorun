# Mail from an operational command

`gorun mail send` sends one message using the site's saved SMTP or Resend settings, including its encrypted provider secret. It works when `gorun serve` is stopped. It starts only the settings module, with no job workers, scheduler, HTTP listener or API key.

Configure mail through Settings → Mail, `PATCH /api/v1/settings` with `settings.manage`, or `gorun service set mail` (alias `gorun mail set`) from the shell. The API and the command validate the values like the native form and encrypt secrets with the site's secret key. `gorun settings set` deliberately refuses secret values; do not put passwords or provider keys in process arguments.

## Configure mail from the shell

`gorun mail set` is an alias of `gorun service set mail` (see [docs/SERVICES.md](SERVICES.md), which covers every service and the `key=value` form). It validates with the form's own rules. The SMTP password or Resend API key is read from the first line of stdin with `--secret-stdin`, never from an argument, stored encrypted and never printed. Only the flags given change; the other fields and the stored secret are kept unless `--secret-stdin` or `--clear-secret` is given. In a container image, run the binary directly:

```sh
printf '%s\n' "$SMTP_TOKEN" |
  docker exec -i <container> /app/gorun service set mail provider=smtp host=smtp.mx.cloudflare.net port=465 tls=tls \
    username=api_token from=noreply@example.com --secret-stdin
```

The 0.27 flag form (`gorun mail set --provider smtp --host … --secret-stdin`) still works. `tls` is `starttls` (default), `tls` (implicit, port 465) or `none`. For Resend use `provider=resend from=<address> --secret-stdin` with the API key on stdin. The command records `service.update` (field names only, `via: cli`) in the audit log and prints `service mail: <state>`. Then `gorun service get mail` (and `gorun info`, under `mail`) reports the result.

Send a reviewed, non-sensitive operational message to an authorized recipient:

```sh
printf '%s\n' '{"to":"operator@example.com","subject":"Site check failed","text":"The health check failed. Review the private service logs."}' |
  gorun mail send --message-stdin --data /srv/my-site
```

The JSON object accepts only `to`, `subject` and `text`. It must fit within 32 KiB. `to` is one plain email address; `subject` is a non-empty single line of at most 256 bytes; `text` must not be empty. Unknown keys, trailing data and header injection are rejected before the site is opened. The HTML alternative escapes the plain text.

A successful command prints:

```json
{"accepted":true,"delivery":"not_verified"}
```

This means the SMTP server or provider accepted the message. It does **not** prove delivery to the recipient's inbox. The command fails with a nonzero exit status when mail is not configured or sending fails; it does not silently log the message as delivery. Sending is bounded by a 45-second context.

The command records `mail.cli.accepted` or `mail.cli.failed` in the site audit log, without recipient, subject, body or provider secret in that audit metadata. Command output contains no message data. Run under the site service account with access to the existing data directory and secret key.

For systemd `OnFailure`, pass a fixed minimal summary rather than raw journal output or form content. Keep deduplication and recovery state in the monitoring wrapper. The command itself sends exactly once per invocation and does not retry ambiguous provider failures; review those failures before a manual resend.

Contact forms use their existing separate event queue and delivery state. Enable `forms.notify` and choose `forms.recipients` (or `contact.email`, falling back to the oldest active administrator) only after configuring and testing the transport. Existing submissions whose notification is off are not automatically replayed. The native inbox exposes an explicit resend action. `sent` is provider acceptance, not an independently verified inbox receipt. Customer receipts remain a separate opt-in setting.
