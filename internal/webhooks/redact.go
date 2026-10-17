package webhooks

import "net/url"

// RedactURL returns raw as origin + path, with "?…" in place of any query
// string: endpoints often carry a token there. Lists, the audit log and
// list API responses show this form; storage and the edit views keep the
// full URL. An unparsable URL redacts to "".
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	out := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath}).String()
	if u.RawQuery != "" || u.ForceQuery {
		out += "?…"
	}
	return out
}

// Redacted returns a copy of list with every URL passed through RedactURL.
func Redacted(list []Webhook) []Webhook {
	out := make([]Webhook, len(list))
	for i, w := range list {
		w.URL = RedactURL(w.URL)
		out[i] = w
	}
	return out
}
