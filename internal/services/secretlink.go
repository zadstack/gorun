package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrLinkGone is a secret link that is unknown, already used, expired,
// made for another service, or made for a provider the service no longer
// uses.
var ErrLinkGone = errors.New("services: link expired or used")

// ErrNoSecret is a secret link asked for a service that has no secret.
var ErrNoSecret = errors.New("services: the service has no secret")

// SecretLinkTTL is how long a secret link stays valid.
const SecretLinkTTL = 15 * time.Minute

// linkPrefix is the settings prefix of stored links; the rest of the key is
// the SHA-256 hex of the token, so the token itself is never stored.
const linkPrefix = "services.secret_link."

// secretLink is a stored link: the service it is for, who asked for it,
// when it stops working, the provider it was made for, and the values
// (never a secret) a remote set held back until the secret comes.
type secretLink struct {
	Name     string            `json:"name"`
	Actor    string            `json:"actor"`
	Expires  time.Time         `json:"expires"`
	Provider string            `json:"provider"`
	Values   map[string]string `json:"values,omitempty"`
}

// setNow replaces the clock of secret links (tests only).
func (r *Registry) setNow(now func() time.Time) { r.now = now }

func linkKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return linkPrefix + hex.EncodeToString(sum[:])
}

// SecretLink makes a one-time link to the admin page where a person with
// settings.manage types the service's secret: valid SecretLinkTTL, once, for
// this service and its current provider only. actorID is who asked for it
// (audited on use). Expired links left behind are deleted here.
func (r *Registry) SecretLink(ctx context.Context, name, actorID string) (string, time.Time, error) {
	s, ok := r.by[name]
	if !ok {
		return "", time.Time{}, ErrUnknownService
	}
	st, err := s.Status(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	return r.secretLink(ctx, name, actorID, st.Provider, nil)
}

// secretLink is SecretLink for provider, holding values to apply with the
// secret. Only the token's hash is stored.
func (r *Registry) secretLink(ctx context.Context, name, actorID, provider string, values map[string]string) (string, time.Time, error) {
	s, ok := r.by[name]
	if !ok {
		return "", time.Time{}, ErrUnknownService
	}
	if !TakesSecret(s) {
		return "", time.Time{}, ErrNoSecret
	}
	r.sweepLinks(ctx)
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, fmt.Errorf("services: secret link: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	expires := r.now().UTC().Add(SecretLinkTTL).Truncate(time.Second)
	l := secretLink{Name: name, Actor: actorID, Expires: expires, Provider: provider, Values: values}
	if err := r.d.Settings.Set(ctx, linkKey(token), l); err != nil {
		return "", time.Time{}, err
	}
	return "/admin/services/" + url.PathEscape(name) + "/secret?t=" + url.QueryEscape(token), expires, nil
}

// sweepLinks deletes the expired links (best effort).
func (r *Registry) sweepLinks(ctx context.Context) {
	all, err := r.d.Settings.All(ctx, linkPrefix)
	if err != nil {
		return
	}
	now := r.now()
	for k, raw := range all {
		var l secretLink
		if json.Unmarshal(raw, &l) != nil || !now.Before(l.Expires) {
			_ = r.d.Settings.Delete(ctx, k)
		}
	}
}

// link is the stored link of token for the service name, read on q (the
// pool or a transaction). expired reports a link that is past its time: the
// caller deletes it. A link for another service is left as it is.
func (r *Registry) link(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, token, name string) (l secretLink, expired bool, err error) {
	if token == "" {
		return l, false, ErrLinkGone
	}
	var raw string
	err = q.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, linkKey(token)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return l, false, ErrLinkGone
	}
	if err != nil {
		return l, false, fmt.Errorf("services: secret link: %w", err)
	}
	if json.Unmarshal([]byte(raw), &l) != nil || !r.now().Before(l.Expires) {
		return l, true, ErrLinkGone
	}
	if l.Name != name {
		return l, false, ErrLinkGone
	}
	return l, false, nil
}

// liveLink is the stored link of token for the service s, checked on the
// pool: unknown, expired (deleted when seen), other-service and
// other-provider links are ErrLinkGone. The provider a redemption would
// apply is the held provider value, else the one the service uses now; a
// link made for another one is deleted, since its secret would land in the
// wrong provider.
func (r *Registry) liveLink(ctx context.Context, s Service, token string) (secretLink, error) {
	l, expired, err := r.link(ctx, r.d.DB, token, s.Name())
	if expired {
		_ = r.d.Settings.Delete(ctx, linkKey(token))
	}
	if err != nil {
		return l, err
	}
	provider := strings.TrimSpace(l.Values["provider"])
	if provider == "" {
		st, err := s.Status(ctx)
		if err != nil {
			return l, err
		}
		provider = st.Provider
	}
	if provider != l.Provider {
		_ = r.d.Settings.Delete(ctx, linkKey(token))
		return l, ErrLinkGone
	}
	return l, nil
}

// checkLink reports whether token is a live link for name, without using it
// up; an expired link is deleted.
func (r *Registry) checkLink(ctx context.Context, token, name string) error {
	s, ok := r.by[name]
	if !ok {
		return ErrUnknownService
	}
	_, err := r.liveLink(ctx, s, token)
	return err
}

// RedeemSecret stores secret for the service through the link's token,
// together with the values the link holds: the link is deleted in the same
// transaction as the service's Apply, so it works once. An unknown, used,
// expired (deleted when seen), other-service or other-provider link is
// ErrLinkGone and nothing is saved. Field errors (an empty or refused
// secret) save nothing and leave the link usable until it expires.
func (r *Registry) RedeemSecret(ctx context.Context, token, name, secret, via string) (Status, map[string]string, error) {
	s, ok := r.by[name]
	if !ok {
		return Status{}, nil, ErrUnknownService
	}
	held, err := r.liveLink(ctx, s, token)
	if err != nil {
		return Status{}, nil, err
	}
	if strings.TrimSpace(secret) == "" {
		return Status{}, map[string]string{s.Secret(held.Provider): errRequired}, nil
	}
	var actor string
	expired := false
	st, fields, err := r.set(ctx, name, held.Values, &secret, via, func(tx *sql.Tx) error {
		l, exp, err := r.link(ctx, tx, token, name)
		if err != nil {
			expired = exp
			return err
		}
		actor = l.Actor
		return r.d.Settings.DeleteTx(ctx, tx, linkKey(token))
	}, func() map[string]any { return map[string]any{"secret_link": true, "link_actor": actor} })
	if expired {
		_ = r.d.Settings.Delete(ctx, linkKey(token))
	}
	return st, fields, err
}
