// Package config loads GoRun configuration.
//
// Precedence (highest first): flags > environment > <DataDir>/gorun.toml > defaults.
// The data dir itself is resolved from --data / GORUN_DATA / default only.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	defaultDataDir = "./data"
	defaultAddr    = ":8080"
	defaultTheme   = "default"
	secretKeyLen   = 32
	tomlFile       = "gorun.toml"
	secretFile     = "secret.key"
	dbFile         = "gorun.db"
)

// Config is the resolved runtime configuration.
type Config struct {
	// Coolify deployment integration: operator-only environment, never editable by a web request.
	UpdateURL, UpdateToken, UpdateResource string
	// Update configures the staged update lifecycle (trusted release source,
	// verification policy, health links). Operator-only, like the Coolify fields.
	Update UpdateConfig

	Addons          map[string]string // Optional addon modes: enabled, disabled or auto.
	TrustedProxies  []string          // GORUN_TRUSTED_PROXIES; empty trusts no forwarded headers
	CSPScriptSrc    []string          // GORUN_CSP_SCRIPT_SRC: extra public-page script sources (see PublicScriptSources)
	Env             string            // "dev" | "prod"
	DataDir         string            // GORUN_DATA, default ./data
	BaseURL         string            // GORUN_BASE_URL
	Addr            string            // GORUN_ADDR default 127.0.0.1:8080 (dev), :8080 (prod)
	SecretKey       []byte            // GORUN_SECRET_KEY (32 bytes after base64 decode) — required in prod, generated+persisted in dev
	AllowPrivateNet bool              // GORUN_ALLOW_PRIVATE_NET
	// MCPSameOwnerApproval (GORUN_MCP_SAME_OWNER_APPROVAL) lets an API key
	// approve MCP calls requested by another key of the same owner. Off by
	// default: approvals by key need a different person.
	MCPSameOwnerApproval bool
	// HSTSIncludeSubdomains (GORUN_HSTS_INCLUDE_SUBDOMAINS) and HSTSPreload
	// (GORUN_HSTS_PRELOAD) extend the HSTS header sent on https requests.
	// Off by default; preload requires includeSubDomains.
	HSTSIncludeSubdomains, HSTSPreload bool
	// Metrics (GORUN_METRICS) serves GET /metrics in Prometheus text format.
	// Off by default. MetricsToken (GORUN_METRICS_TOKEN) optional Bearer for scrapers.
	Metrics      bool
	MetricsToken string
	Stripe       struct{ SecretKey, WebhookSecret string } // STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET
	Theme        string                                    // GORUN_THEME default "default"
	SetupToken   string                                    // GORUN_SETUP_TOKEN: honored in every environment; required for prod setup (≥ 16 chars)
	S3           S3                                        // [s3] / GORUN_S3_*: media in an S3-compatible bucket
}

// UpdateConfig is the operator configuration of staged core updates. An
// empty SourceURL disables requests; reads still work. Hosts and
// credentials never come from web requests.
type UpdateConfig struct {
	SourceURL   string   // GORUN_UPDATE_SOURCE_URL / [update] source_url: HTTPS release index (JSON)
	SourceToken string   // GORUN_UPDATE_SOURCE_TOKEN / [update] source_token: optional bearer for the index
	Artifact    string   // GORUN_UPDATE_ARTIFACT / [update] artifact: which artifact of a release to activate
	HealthLinks []string // GORUN_UPDATE_HEALTH_LINKS / [update] health_links: absolute URLs that must answer 2xx after activation
	// AllowDowngrade lets compatibility pass for a lower target version.
	AllowDowngrade bool // GORUN_UPDATE_ALLOW_DOWNGRADE / [update] allow_downgrade
	// AllowMissingDigest accepts releases whose source publishes no digest.
	AllowMissingDigest bool // GORUN_UPDATE_ALLOW_MISSING_DIGEST / [update] allow_missing_digest
	// ActivationTimeout bounds waiting for the provider or operator (default 15m).
	ActivationTimeout time.Duration // GORUN_UPDATE_ACTIVATION_TIMEOUT / [update] activation_timeout ("15m")
}

// S3 is an S3-compatible bucket for media (empty = media on disk).
type S3 struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey, PublicURL string
}

// MinSetupTokenLen is the shortest GORUN_SETUP_TOKEN accepted.
const MinSetupTokenLen = 16

// DBPath is the SQLite database file inside DataDir.
func (c Config) DBPath() string { return filepath.Join(c.DataDir, dbFile) }

// fileConfig mirrors gorun.toml. Pointers distinguish "unset" from zero values.
type fileConfig struct {
	Addons          map[string]string `toml:"addons"`
	TrustedProxies  []string          `toml:"trusted_proxies"`
	CSPScriptSrc    []string          `toml:"csp_script_src"`
	Env             *string           `toml:"env"`
	BaseURL         *string           `toml:"base_url"`
	Addr            *string           `toml:"addr"`
	SecretKey       *string           `toml:"secret_key"`
	AllowPrivateNet *bool             `toml:"allow_private_net"`
	MCPSameOwner    *bool             `toml:"mcp_same_owner_approval"`
	HSTSSubdomains  *bool             `toml:"hsts_include_subdomains"`
	HSTSPreload     *bool             `toml:"hsts_preload"`
	Metrics         *bool             `toml:"metrics"`
	MetricsToken    *string           `toml:"metrics_token"`
	Theme           *string           `toml:"theme"`
	SetupToken      *string           `toml:"setup_token"`
	Stripe          struct {
		SecretKey     *string `toml:"secret_key"`
		WebhookSecret *string `toml:"webhook_secret"`
	} `toml:"stripe"`
	Update struct {
		SourceURL          *string  `toml:"source_url"`
		SourceToken        *string  `toml:"source_token"`
		Artifact           *string  `toml:"artifact"`
		HealthLinks        []string `toml:"health_links"`
		AllowDowngrade     *bool    `toml:"allow_downgrade"`
		AllowMissingDigest *bool    `toml:"allow_missing_digest"`
		ActivationTimeout  *string  `toml:"activation_timeout"`
	} `toml:"update"`
	S3 struct {
		Endpoint  *string `toml:"endpoint"`
		Region    *string `toml:"region"`
		Bucket    *string `toml:"bucket"`
		AccessKey *string `toml:"access_key"`
		SecretKey *string `toml:"secret_key"`
		PublicURL *string `toml:"public_url"`
	} `toml:"s3"`
}

// Load resolves the configuration from args (flags, without the program or
// subcommand name), the environment, the TOML file and defaults.
func Load(args []string) (Config, error) { return load(args, true) }

// LoadReadOnly validates existing configuration without generating a dev secret.
func LoadReadOnly(args []string) (Config, error) { return load(args, false) }

func load(args []string, allowGenerate bool) (Config, error) {
	fl, set, err := parseFlags(args)
	if err != nil {
		return Config{}, err
	}
	c := Config{Env: "dev", DataDir: defaultDataDir, Theme: defaultTheme}
	if v := os.Getenv("GORUN_DATA"); v != "" {
		c.DataDir = v
	}
	if set["data"] {
		c.DataDir = fl.DataDir
	}
	var secret string
	if err := applyFile(&c, &secret); err != nil {
		return Config{}, err
	}
	if err := applyEnv(&c, &secret); err != nil {
		return Config{}, err
	}
	applyFlags(&c, fl, set)
	if err := validateAddons(c.Addons); err != nil {
		return Config{}, err
	}
	if _, err := c.TrustedProxyNetworks(); err != nil {
		return Config{}, err
	}
	if _, err := c.PublicScriptSources(); err != nil {
		return Config{}, err
	}
	if c.HSTSPreload && !c.HSTSIncludeSubdomains {
		return Config{}, errors.New("config: GORUN_HSTS_PRELOAD requires GORUN_HSTS_INCLUDE_SUBDOMAINS (hsts_preload needs hsts_include_subdomains)")
	}
	if c.Env != "dev" && c.Env != "prod" {
		return Config{}, fmt.Errorf("config: env must be dev or prod, got %q", c.Env)
	}
	if c.Addr == "" {
		c.Addr = defaultAddr
		if c.Env == "dev" {
			c.Addr = "127.0.0.1:8080"
		}
	}
	c.SetupToken = strings.TrimSpace(c.SetupToken)
	if c.SetupToken != "" && len(c.SetupToken) < MinSetupTokenLen {
		return Config{}, fmt.Errorf("config: GORUN_SETUP_TOKEN must be at least %d characters", MinSetupTokenLen)
	}
	if c.BaseURL == "" && (strings.HasPrefix(c.Addr, ":") || c.Addr == "127.0.0.1:8080") && c.Env != "prod" {
		_, port, _ := net.SplitHostPort(c.Addr)
		c.BaseURL = "http://localhost:" + port
	}
	if c.BaseURL == "" && c.Env == "dev" {
		host, port, err := net.SplitHostPort(c.Addr)
		ip := net.ParseIP(host)
		if err == nil && (strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())) {
			c.BaseURL = "http://" + net.JoinHostPort(host, port)
		}
	}
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if c.Env == "prod" {
		if err := checkPublicBaseURL(c.BaseURL); err != nil {
			return Config{}, err
		}
	}
	key, err := resolveSecretMode(c, secret, allowGenerate)
	if err != nil {
		return Config{}, err
	}
	c.SecretKey = key
	return c, nil
}

// checkPublicBaseURL fails fast when prod has no usable public base URL:
// links, redirects, cookies and HSTS all derive from it, and a localhost
// default would silently produce broken absolute URLs.
func checkPublicBaseURL(raw string) error {
	fail := func(why string) error {
		return fmt.Errorf("config: GORUN_BASE_URL %s in prod: set it to the public URL, e.g. https://example.com (got %q)", why, raw)
	}
	if raw == "" {
		return fail("is required")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fail("must be an absolute http(s) URL")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fail("must not be localhost")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return fail("must not be a loopback or unspecified address")
	}
	return nil
}

func parseFlags(args []string) (Config, map[string]bool, error) {
	var fl Config
	fs := flag.NewFlagSet("gorun", flag.ContinueOnError)
	fs.StringVar(&fl.Env, "env", "", "environment: dev or prod")
	fs.StringVar(&fl.DataDir, "data", "", "data directory (db, media, secret)")
	fs.StringVar(&fl.BaseURL, "base-url", "", "public base URL")
	fs.StringVar(&fl.Addr, "addr", "", "listen address")
	fs.StringVar(&fl.Theme, "theme", "", "active theme slug")
	fs.StringVar(&fl.SetupToken, "setup-token", "", "token required by /admin/setup in prod")
	fs.BoolVar(&fl.AllowPrivateNet, "allow-private-net", false, "allow outbound requests to private networks")
	if err := fs.Parse(args); err != nil {
		return Config{}, nil, fmt.Errorf("config: %w", err)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return fl, set, nil
}

func applyFile(c *Config, secret *string) error {
	path := filepath.Join(c.DataDir, tomlFile)
	var fc fileConfig
	md, err := toml.DecodeFile(path, &fc)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return fmt.Errorf("config: %s: unknown keys %v", path, undecoded)
	}
	c.Addons = fc.Addons
	c.TrustedProxies = append([]string(nil), fc.TrustedProxies...)
	c.CSPScriptSrc = append([]string(nil), fc.CSPScriptSrc...)
	setStr(&c.Env, fc.Env)
	setStr(&c.BaseURL, fc.BaseURL)
	setStr(&c.Addr, fc.Addr)
	setStr(&c.Theme, fc.Theme)
	setStr(&c.SetupToken, fc.SetupToken)
	setStr(secret, fc.SecretKey)
	setStr(&c.Stripe.SecretKey, fc.Stripe.SecretKey)
	setStr(&c.Stripe.WebhookSecret, fc.Stripe.WebhookSecret)
	setStr(&c.S3.Endpoint, fc.S3.Endpoint)
	setStr(&c.S3.Region, fc.S3.Region)
	setStr(&c.S3.Bucket, fc.S3.Bucket)
	setStr(&c.S3.AccessKey, fc.S3.AccessKey)
	setStr(&c.S3.SecretKey, fc.S3.SecretKey)
	setStr(&c.S3.PublicURL, fc.S3.PublicURL)
	setStr(&c.Update.SourceURL, fc.Update.SourceURL)
	setStr(&c.Update.SourceToken, fc.Update.SourceToken)
	setStr(&c.Update.Artifact, fc.Update.Artifact)
	if fc.Update.HealthLinks != nil {
		c.Update.HealthLinks = append([]string(nil), fc.Update.HealthLinks...)
	}
	if fc.Update.ActivationTimeout != nil {
		d, err := time.ParseDuration(*fc.Update.ActivationTimeout)
		if err != nil || d <= 0 {
			return fmt.Errorf("config: %s: update.activation_timeout must be a positive duration", path)
		}
		c.Update.ActivationTimeout = d
	}
	for dst, v := range map[*bool]*bool{&c.Update.AllowDowngrade: fc.Update.AllowDowngrade, &c.Update.AllowMissingDigest: fc.Update.AllowMissingDigest} {
		if v != nil {
			*dst = *v
		}
	}
	if fc.AllowPrivateNet != nil {
		c.AllowPrivateNet = *fc.AllowPrivateNet
	}
	for dst, v := range map[*bool]*bool{&c.MCPSameOwnerApproval: fc.MCPSameOwner, &c.HSTSIncludeSubdomains: fc.HSTSSubdomains,
		&c.HSTSPreload: fc.HSTSPreload, &c.Metrics: fc.Metrics} {
		if v != nil {
			*dst = *v
		}
	}
	setStr(&c.MetricsToken, fc.MetricsToken)
	return nil
}

func setStr(dst *string, v *string) {
	if v != nil {
		*dst = *v
	}
}

func applyEnv(c *Config, secret *string) error {
	envStr(&c.UpdateURL, "GORUN_UPDATE_COOLIFY_URL")
	envStr(&c.UpdateToken, "GORUN_UPDATE_COOLIFY_TOKEN")
	envStr(&c.UpdateResource, "GORUN_UPDATE_COOLIFY_RESOURCE")
	envStr(&c.Update.SourceURL, "GORUN_UPDATE_SOURCE_URL")
	envStr(&c.Update.SourceToken, "GORUN_UPDATE_SOURCE_TOKEN")
	envStr(&c.Update.Artifact, "GORUN_UPDATE_ARTIFACT")
	if v, ok := os.LookupEnv("GORUN_UPDATE_HEALTH_LINKS"); ok {
		c.Update.HealthLinks = strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	}
	for dst, name := range map[*bool]string{&c.Update.AllowDowngrade: "GORUN_UPDATE_ALLOW_DOWNGRADE", &c.Update.AllowMissingDigest: "GORUN_UPDATE_ALLOW_MISSING_DIGEST"} {
		if v, ok := os.LookupEnv(name); ok {
			*dst = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
		}
	}
	if v := os.Getenv("GORUN_UPDATE_ACTIVATION_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("config: GORUN_UPDATE_ACTIVATION_TIMEOUT must be a positive duration")
		}
		c.Update.ActivationTimeout = d
	}
	if raw, ok := os.LookupEnv("GORUN_ADDONS"); ok {
		var modes map[string]string
		if err := json.Unmarshal([]byte(raw), &modes); err != nil {
			return fmt.Errorf("config: invalid GORUN_ADDONS JSON object: %w", err)
		}
		if c.Addons == nil {
			c.Addons = map[string]string{}
		}
		for name, mode := range modes {
			c.Addons[name] = mode
		}
	}
	if v, ok := os.LookupEnv("GORUN_TRUSTED_PROXIES"); ok {
		c.TrustedProxies = nil
		if strings.TrimSpace(v) != "" {
			c.TrustedProxies = strings.Split(v, ",")
		}
	}
	if v, ok := os.LookupEnv("GORUN_CSP_SCRIPT_SRC"); ok {
		c.CSPScriptSrc = strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	}
	envStr(&c.Env, "GORUN_ENV")
	envStr(&c.BaseURL, "GORUN_BASE_URL")
	envStr(&c.Addr, "GORUN_ADDR")
	envStr(&c.Theme, "GORUN_THEME")
	envStr(&c.SetupToken, "GORUN_SETUP_TOKEN")
	envStr(&c.MetricsToken, "GORUN_METRICS_TOKEN")
	envStr(secret, "GORUN_SECRET_KEY")
	envStr(&c.Stripe.SecretKey, "STRIPE_SECRET_KEY")
	envStr(&c.Stripe.WebhookSecret, "STRIPE_WEBHOOK_SECRET")
	envStr(&c.S3.Endpoint, "GORUN_S3_ENDPOINT")
	envStr(&c.S3.Region, "GORUN_S3_REGION")
	envStr(&c.S3.Bucket, "GORUN_S3_BUCKET")
	envStr(&c.S3.AccessKey, "GORUN_S3_ACCESS_KEY")
	envStr(&c.S3.SecretKey, "GORUN_S3_SECRET_KEY")
	envStr(&c.S3.PublicURL, "GORUN_S3_PUBLIC_URL")
	if v := os.Getenv("GORUN_ALLOW_PRIVATE_NET"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("config: GORUN_ALLOW_PRIVATE_NET: %w", err)
		}
		c.AllowPrivateNet = b
	}
	for name, dst := range map[string]*bool{"GORUN_MCP_SAME_OWNER_APPROVAL": &c.MCPSameOwnerApproval,
		"GORUN_HSTS_INCLUDE_SUBDOMAINS": &c.HSTSIncludeSubdomains, "GORUN_HSTS_PRELOAD": &c.HSTSPreload,
		"GORUN_METRICS": &c.Metrics} {
		if v := os.Getenv(name); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("config: %s: %w", name, err)
			}
			*dst = b
		}
	}
	return nil
}

func envStr(dst *string, name string) {
	if v := os.Getenv(name); v != "" {
		*dst = v
	}
}

func applyFlags(c *Config, fl Config, set map[string]bool) {
	if set["env"] {
		c.Env = fl.Env
	}
	if set["base-url"] {
		c.BaseURL = fl.BaseURL
	}
	if set["addr"] {
		c.Addr = fl.Addr
	}
	if set["theme"] {
		c.Theme = fl.Theme
	}
	if set["setup-token"] {
		c.SetupToken = fl.SetupToken
	}
	if set["allow-private-net"] {
		c.AllowPrivateNet = fl.AllowPrivateNet
	}
}

// resolveSecretMode decodes the configured secret, or in dev reads
// <DataDir>/secret.key, generating and persisting one when allowGenerate is
// set so sessions survive restarts.
func resolveSecretMode(c Config, encoded string, allowGenerate bool) ([]byte, error) {
	if encoded != "" {
		return decodeSecret(encoded)
	}
	if c.Env == "prod" {
		return nil, errors.New("config: GORUN_SECRET_KEY is required in prod (base64 of 32 random bytes)")
	}
	path := filepath.Join(c.DataDir, secretFile)
	if b, err := os.ReadFile(path); err == nil {
		return decodeSecret(strings.TrimSpace(string(b)))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	if !allowGenerate {
		return nil, errors.New("config: existing secret is required for read-only access")
	}
	key := make([]byte, secretKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("config: generate secret: %w", err)
	}
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("config: create data dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("config: persist secret: %w", err)
	}
	return key, nil
}

func decodeSecret(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	}
	if err != nil {
		return nil, fmt.Errorf("config: secret key is not valid base64: %w", err)
	}
	if len(key) != secretKeyLen {
		return nil, fmt.Errorf("config: secret key must decode to %d bytes, got %d", secretKeyLen, len(key))
	}
	return key, nil
}

func validateAddons(modes map[string]string) error {
	for name, mode := range modes {
		if strings.TrimSpace(name) == "" {
			return errors.New("config: addon name is empty")
		}
		switch mode {
		case "enabled", "disabled", "auto":
		default:
			return fmt.Errorf("config: addon %s mode must be enabled, disabled or auto", name)
		}
	}
	return nil
}
