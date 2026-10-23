package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

const LogoProtocolPNG = "images-png-v1"
const logoBindingPrefix = "identitylogo.binding."

var ErrLogoChanged = errors.New("logo destination or adapter changed")
var ErrLogoUnavailable = errors.New("static PNG adapter unavailable")
var ErrLogoProvider = errors.New("image request failed; outcome may be uncertain; do not retry automatically")
var logoCurrency = regexp.MustCompile(`^[A-Z]{3}$`)

// LogoBinding is an explicit administrator declaration, not model discovery.
// Estimate is a locally reviewed estimate, never a provider-enforced charge cap.
type LogoBinding struct {
	Version           string `json:"version"`
	ConnectionVersion string `json:"connection_version"`
	Protocol          string `json:"protocol"`
	Model             string `json:"model"`
	Quality           string `json:"quality"`
	Background        string `json:"background"`
	EstimateMicros    int64  `json:"estimate_micros"`
	Currency          string `json:"currency"`
	Source            string `json:"source"`
	AsOf              string `json:"as_of"`
	Basis             string `json:"basis"`
}
type LogoImageInput struct {
	ConnectionID, Theme, Prompt string
	Size                        int
	BudgetMicros                int64
	Currency                    string
}
type LogoImageQuote struct {
	Known                                  bool
	AmountMicros                           int64
	Currency, Source, Version, AsOf, Basis string
}
type LogoImagePrepared struct {
	Input                                                                                    LogoImageInput
	Name, Provider, Model, Host, Fingerprint, CapabilityFingerprint, Evidence, PromptVersion string
	StaticPNG                                                                                bool
	Quote                                                                                    LogoImageQuote
	Payload                                                                                  json.RawMessage
}
type LogoProfile struct {
	ID, Version, Name, Provider, Model, Host string
	Binding                                  LogoBinding
	Configured                               bool
	Estimate                                 string
}

type logoDestination struct {
	profile  connectionRecord
	binding  LogoBinding
	prepared LogoImagePrepared
}

func logoDigest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func logoAllowed(ctx context.Context, d *kernel.Deps) bool {
	on, e := kernel.AddonEnabled(ctx, d.DB, "identitylogo")
	return e == nil && on && auth.Can(ctx, "settings.manage") && auth.Can(ctx, "media.upload")
}

func ListIdentityLogoProfiles(ctx context.Context, d *kernel.Deps) ([]LogoProfile, error) {
	if !auth.Can(ctx, "settings.manage") {
		return nil, ErrLogoUnavailable
	}
	rows, e := (&handlers{d: d}).listConnections(ctx)
	if e != nil {
		return nil, e
	}
	out := []LogoProfile{}
	for _, c := range rows {
		if c.Kind != "ai" || validateConnection(c) != nil {
			continue
		}
		var b LogoBinding
		_ = d.Settings.Get(ctx, logoBindingPrefix+c.ID, &b)
		out = append(out, LogoProfile{ID: c.ID, Version: c.Version, Name: c.Name, Provider: c.Provider, Model: c.Model, Host: hostOf(c.Endpoint), Binding: b, Configured: validLogoBinding(c, b) == nil, Estimate: FormatLogoAmount(b.EstimateMicros)})
	}
	return out, nil
}
func validLogoBinding(c connectionRecord, b LogoBinding) error {
	endpoint, _ := url.Parse(c.Endpoint)
	if endpoint == nil || strings.EqualFold(endpoint.Hostname(), "api.anthropic.com") {
		return ErrLogoUnavailable
	}
	if b.Version == "" || b.ConnectionVersion != c.Version || b.Model != c.Model || b.Protocol != LogoProtocolPNG ||
		(b.Quality != "low" && b.Quality != "medium" && b.Quality != "high") || (b.Background != "opaque" && b.Background != "transparent") ||
		b.EstimateMicros < 0 || !logoCurrency.MatchString(b.Currency) || len(b.Basis) > 800 {
		return ErrLogoUnavailable
	}
	if b.EstimateMicros > 0 {
		u, e := url.Parse(b.Source)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(b.Source) > 500 || strings.TrimSpace(b.Basis) == "" {
			return ErrLogoUnavailable
		}
		if _, e = time.Parse("2006-01-02", b.AsOf); e != nil {
			return ErrLogoUnavailable
		}
	}
	return nil
}
func SaveIdentityLogoBinding(ctx context.Context, d *kernel.Deps, id string, b LogoBinding) error {
	if !auth.Can(ctx, "settings.manage") {
		return ErrLogoUnavailable
	}
	c, e := (&handlers{d: d}).loadConnection(ctx, id)
	if e != nil || c.Kind != "ai" || validateConnection(c) != nil {
		return ErrLogoUnavailable
	}
	b.Version = db.NewID()
	if validLogoBinding(c, b) != nil {
		return ErrLogoUnavailable
	}
	return d.Settings.Set(ctx, logoBindingPrefix+id, b)
}

// One SELECT gives profile, encrypted credential and adapter declaration one
// SQLite snapshot. Generate uses the credential from this exact snapshot.
func loadLogoDestination(ctx context.Context, d *kernel.Deps, in LogoImageInput, version string) (logoDestination, error) {
	var dst logoDestination
	if !connectionIDPattern.MatchString(in.ConnectionID) || in.Size != 1024 || len(in.Prompt) > 8000 || strings.TrimSpace(in.Prompt) == "" {
		return dst, ErrLogoUnavailable
	}
	rows, e := d.DB.QueryContext(ctx, `SELECT key,value_json FROM settings WHERE key IN (?,?)`, connectionPrefix+in.ConnectionID, logoBindingPrefix+in.ConnectionID)
	if e != nil {
		return dst, ErrLogoUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var key, raw string
		if rows.Scan(&key, &raw) != nil {
			return dst, ErrLogoUnavailable
		}
		if key == connectionPrefix+in.ConnectionID {
			e = json.Unmarshal([]byte(raw), &dst.profile)
		} else {
			e = json.Unmarshal([]byte(raw), &dst.binding)
		}
		if e != nil {
			return dst, ErrLogoUnavailable
		}
	}
	if rows.Err() != nil || dst.profile.ID != in.ConnectionID || dst.profile.Kind != "ai" || validateConnection(dst.profile) != nil || validLogoBinding(dst.profile, dst.binding) != nil {
		return dst, ErrLogoUnavailable
	}
	c, b := dst.profile, dst.binding
	payload, e := json.Marshal(map[string]any{"model": c.Model, "prompt": in.Prompt, "n": 1, "size": "1024x1024", "output_format": "png", "quality": b.Quality, "background": b.Background})
	if e != nil {
		return dst, e
	}
	bound, _ := json.Marshal(struct {
		Profile       connectionRecord
		Binding       LogoBinding
		PromptVersion string
	}{c, b, version})
	capability, _ := json.Marshal(b)
	dst.prepared = LogoImagePrepared{Input: in, Name: c.Name, Provider: c.Provider, Model: c.Model, Host: hostOf(c.Endpoint), Fingerprint: logoDigest(bound), CapabilityFingerprint: logoDigest(capability), Evidence: "OpenAI-compatible images-png-v1; administrator-configured/unverified; no generation proof", PromptVersion: version, StaticPNG: true, Payload: payload, Quote: LogoImageQuote{Known: b.EstimateMicros > 0, AmountMicros: b.EstimateMicros, Currency: b.Currency, Source: b.Source, Version: b.Version, AsOf: b.AsOf, Basis: b.Basis}}
	return dst, nil
}
func PrepareIdentityLogo(ctx context.Context, d *kernel.Deps, in LogoImageInput, version string) (LogoImagePrepared, error) {
	if !logoAllowed(ctx, d) {
		return LogoImagePrepared{}, ErrLogoUnavailable
	}
	dst, e := loadLogoDestination(ctx, d, in, version)
	return dst.prepared, e
}
func logoHTTPClient() *http.Client {
	c := httpx.NewSafeClientOpts(httpx.SafeClientOptions{Timeout: logoRequestTimeout, NoRedirects: true})
	// Image generation is non-streaming: the provider sends no headers until
	// the image is ready. Keep the safe client's dialer and TLS budget but
	// wait for headers as long as the whole request may take, as aiHTTPClient
	// does; aborting earlier can abandon a generation the provider still bills.
	if transport, ok := c.Transport.(*http.Transport); ok {
		transport.ResponseHeaderTimeout = logoRequestTimeout
	}
	return c
}

const logoRequestTimeout = 90 * time.Second

// afterLogoDestinationLoad is a same-read race test seam, never a runtime hook.
var afterLogoDestinationLoad func()
var identityLogoClient = logoHTTPClient

func GenerateIdentityLogoBounded(ctx context.Context, d *kernel.Deps, approved LogoImagePrepared) ([]byte, error) {
	if !logoAllowed(ctx, d) {
		return nil, ErrLogoUnavailable
	}
	dst, e := loadLogoDestination(ctx, d, approved.Input, approved.PromptVersion)
	if e != nil {
		return nil, ErrLogoChanged
	}
	expected, _ := json.Marshal(approved)
	current, _ := json.Marshal(dst.prepared)
	if !bytes.Equal(expected, current) || !approved.Quote.Known || approved.Quote.AmountMicros > approved.Input.BudgetMicros || approved.Quote.Currency != approved.Input.Currency {
		return nil, ErrLogoChanged
	}
	if afterLogoDestinationLoad != nil {
		afterLogoDestinationLoad()
	}
	secrets, e := (&handlers{d: d}).connectionSecrets(dst.profile)
	if e != nil || secrets.Token == "" {
		return nil, ErrLogoUnavailable
	}
	if !logoAllowed(ctx, d) {
		return nil, ErrLogoUnavailable
	}
	return sendIdentityLogo(ctx, identityLogoClient(), dst.profile.Endpoint, secrets.Token, approved.Payload)
}
func sendIdentityLogo(ctx context.Context, client *http.Client, endpoint, key string, payload []byte) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/images/generations", bytes.NewReader(payload))
	if e != nil {
		return nil, ErrLogoProvider
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, e := client.Do(req)
	if e != nil {
		return nil, ErrLogoProvider
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrLogoProvider
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 12<<20+1))
	if e != nil || len(raw) > 12<<20 {
		return nil, ErrLogoProvider
	}
	var body struct {
		Data []struct {
			Base64 string `json:"b64_json"`
			URL    string `json:"url"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &body) != nil || len(body.Data) != 1 || body.Data[0].Base64 == "" || body.Data[0].URL != "" {
		return nil, ErrLogoProvider
	}
	image, e := base64.StdEncoding.DecodeString(body.Data[0].Base64)
	if e != nil || len(image) == 0 || len(image) > 8<<20 {
		return nil, ErrLogoProvider
	}
	return image, nil
}

// FormatLogoAmount prints currency units without floating-point price rounding.
func FormatLogoAmount(micros int64) string {
	return fmt.Sprintf("%d.%06d", micros/1000000, micros%1000000)
}
