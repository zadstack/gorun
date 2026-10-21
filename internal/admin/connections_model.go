package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/settings"
)

const connectionPrefix = "connections.profile."
const maxConnections = 40

var connectionIDPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
var connectionBucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var connectionRegionPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
var errConnectionInvalid = errors.New("invalid connection")
var errConnectionConflict = errors.New("connection changed")

type connectionRecord struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Endpoint     string `json:"endpoint"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	Region       string `json:"region,omitempty"`
	Secret       string `json:"secret,omitempty"`
	TestedAt     string `json:"tested_at,omitempty"`
	TestStatus   string `json:"test_status,omitempty"`
	TestCount    int    `json:"test_count,omitempty"`
	TestProtocol string `json:"test_protocol,omitempty"`
	TestVersion  string `json:"test_version,omitempty"`
}

type connectionSecrets struct {
	Token     string `json:"token,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
}

type connectionProbeResult struct {
	Count    int
	Protocol string
}

func normalizeConnectionEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 500 || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", errConnectionInvalid
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return "", errConnectionInvalid
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() || !ip.IsGlobalUnicast()) {
		return "", errConnectionInvalid
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func validateConnection(c connectionRecord) error {
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 100 {
		return errConnectionInvalid
	}
	if _, err := normalizeConnectionEndpoint(c.Endpoint); err != nil {
		return err
	}
	switch c.Kind {
	case "mcp":
	case "ai":
		if (c.Provider != "openai" && c.Provider != "anthropic") || !aiModelRe.MatchString(c.Model) {
			return errConnectionInvalid
		}
	case "s3":
		u, _ := url.Parse(c.Endpoint)
		if u.Path != "" || !connectionBucketPattern.MatchString(c.Bucket) || !connectionRegionPattern.MatchString(c.Region) {
			return errConnectionInvalid
		}
	default:
		return errConnectionInvalid
	}
	return nil
}

func (h *handlers) loadConnection(ctx context.Context, id string) (connectionRecord, error) {
	var c connectionRecord
	if !connectionIDPattern.MatchString(id) {
		return c, errConnectionInvalid
	}
	if err := h.d.Settings.Get(ctx, connectionPrefix+id, &c); err != nil {
		return c, err
	}
	if c.ID != id {
		return c, errConnectionInvalid
	}
	return c, nil
}
func (h *handlers) connectionSecrets(c connectionRecord) (connectionSecrets, error) {
	var secrets connectionSecrets
	if c.Secret == "" {
		return secrets, nil
	}
	raw, err := mailer.DecryptSecret(h.d.Config.SecretKey, c.Secret)
	if err != nil {
		return secrets, err
	}
	err = json.Unmarshal([]byte(raw), &secrets)
	return secrets, err
}
func (h *handlers) encryptConnectionSecrets(s connectionSecrets) (string, error) {
	if s == (connectionSecrets{}) {
		return "", nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return mailer.EncryptSecret(h.d.Config.SecretKey, string(raw))
}
func validConnectionSecret(s string) bool {
	return len(s) <= 2048 && !strings.ContainsAny(s, "\r\n\x00")
}
func changedConnection(c connectionRecord) connectionRecord {
	c.Version = db.NewID()
	c.TestedAt = ""
	c.TestStatus = ""
	c.TestCount = 0
	c.TestProtocol = ""
	c.TestVersion = ""
	return c
}
func recordConnectionProbe(c connectionRecord, result connectionProbeResult, err error) connectionRecord {
	c.TestedAt = time.Now().UTC().Format(time.RFC3339)
	c.TestStatus = "pass"
	c.TestVersion = c.Version
	c.TestCount = result.Count
	c.TestProtocol = result.Protocol
	if err != nil {
		c.TestStatus = "fail"
		c.TestCount = 0
		c.TestProtocol = ""
	}
	return c
}
func connectionMissing(err error) bool {
	return errors.Is(err, settings.ErrNotFound) || errors.Is(err, errConnectionInvalid)
}
