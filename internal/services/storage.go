package services

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/settings"
)

// Storage is the media and backup buckets, over the Connections profiles
// and the managed destinations the Integrations hub binds
// (storage.active.media, storage.active.backup). GORUN_S3_* (the [s3]
// config section) stays the media source "config" while no destination
// is bound; a bound media destination wins over it. The secret is the
// bucket's key pair as one line ACCESS_KEY:SECRET_KEY.
func Storage(d *kernel.Deps) Service { return storageService{d} }

type storageService struct{ d *kernel.Deps }

// The admin's purposes are media and backup; the service says backups.
var storagePurposes = map[string][]string{"media": {"media"}, "backups": {"backup"}, "both": {"media", "backup"}}

var r2AccountPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

const r2Suffix = ".r2.cloudflarestorage.com"

// Field error keys of the storage service (beside the admin's own).
const (
	errStorageRequired  = "services.error.required"
	errStorageUnknown   = "services.error.unknown_field"
	errStorageKeys      = "services.storage.error.keys"
	errStorageAccount   = "services.storage.error.account"
	errStorageProvider  = "services.storage.error.provider"
	errStoragePublicURL = "services.storage.error.public_url"
)

func (storageService) Name() string        { return "storage" }
func (storageService) Providers() []string { return []string{"r2", "s3"} }
func (storageService) Secret(string) string {
	return "keys"
}

func (storageService) Fields() []Field {
	return []Field{
		{Name: "provider", Type: "enum", Required: true, Values: []string{"r2", "s3"}},
		{Name: "account", Type: "string", Providers: []string{"r2"}},
		{Name: "endpoint", Type: "string", Providers: []string{"s3"}},
		{Name: "region", Type: "string"},
		{Name: "bucket", Type: "string", Required: true},
		{Name: "public_url", Type: "string"},
		{Name: "purpose", Type: "enum", Required: true, Values: []string{"media", "backups", "both"}},
	}
}

// storageDest is one active destination as stored, keys included: it
// never leaves this file except as the location fields of a Status.
type storageDest struct {
	id       string
	cfg      media.S3Config
	readable bool
}

// active reads the destination each admin purpose (media, backup) uses.
func (s storageService) active(ctx context.Context) (map[string]storageDest, error) {
	out := map[string]storageDest{}
	for _, p := range []string{"media", "backup"} {
		var id string
		if err := s.d.Settings.Get(ctx, "storage.active."+p, &id); err != nil && !errors.Is(err, settings.ErrNotFound) {
			return nil, err
		}
		if id == "" {
			continue
		}
		sd := storageDest{id: id}
		if d, err := media.LoadDestination(ctx, s.d.Settings, id); err == nil && d.Purpose == p {
			if cfg, err := media.DestinationConfig(d, s.d.Config.SecretKey); err == nil {
				sd.cfg, sd.readable = cfg, true
			}
		}
		out[p] = sd
	}
	return out, nil
}

// primary is the destination a status describes: media's, else backup's.
func primary(act map[string]storageDest) (storageDest, bool) {
	if d, ok := act["media"]; ok {
		return d, true
	}
	d, ok := act["backup"]
	return d, ok
}

func sameLocation(a, b media.S3Config) bool {
	return strings.TrimRight(a.Endpoint, "/") == strings.TrimRight(b.Endpoint, "/") && a.Bucket == b.Bucket && a.Region == b.Region
}

// purposeOf is the service purpose of the purposes bound to cfg's location.
func purposeOf(act map[string]storageDest, cfg media.S3Config) string {
	m, b := act["media"], act["backup"]
	inMedia := m.id != "" && m.readable && sameLocation(m.cfg, cfg)
	inBackup := b.id != "" && b.readable && sameLocation(b.cfg, cfg)
	switch {
	case inMedia && inBackup:
		return "both"
	case inMedia:
		return "media"
	case inBackup:
		return "backups"
	}
	return ""
}

// providerOf splits an endpoint into r2 and its account, or s3.
func providerOf(endpoint string) (provider, account string) {
	if u, err := url.Parse(endpoint); err == nil {
		host := strings.ToLower(u.Hostname())
		if acct := strings.TrimSuffix(host, r2Suffix); acct != host && r2AccountPattern.MatchString(acct) {
			return "r2", acct
		}
	}
	return "s3", ""
}

func locationFields(f map[string]any, cfg media.S3Config) string {
	provider, account := providerOf(cfg.Endpoint)
	f["provider"], f["endpoint"], f["region"], f["bucket"] = provider, cfg.Endpoint, cfg.Region, cfg.Bucket
	if account != "" {
		f["account"] = account
	}
	return provider
}

func (s storageService) configS3() media.S3Config {
	c := s.d.Config.S3
	return media.S3Config{Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, AccessKey: c.AccessKey, SecretKey: c.SecretKey, PublicURL: c.PublicURL}
}

// Status reports where media and backups go. Source is settings when a
// destination is bound, else config when GORUN_S3_* is set, else local;
// media_source and backups_source say it per purpose, and
// overridden_config says a bound media destination wins over GORUN_S3_*.
// Keys are never reported, only whether they are set.
func (s storageService) Status(ctx context.Context) (Status, error) {
	st := Status{Name: "storage", Missing: []string{}, Fields: map[string]any{}}
	act, err := s.active(ctx)
	if err != nil {
		return st, err
	}
	conf := s.configS3()
	confOn := conf.Enabled()
	st.Fields["media_source"], st.Fields["backups_source"] = "local", "none"
	if confOn {
		st.Fields["media_source"] = "config"
	}
	if _, ok := act["media"]; ok {
		st.Fields["media_source"] = "settings"
	}
	if _, ok := act["backup"]; ok {
		st.Fields["backups_source"] = "settings"
	}
	if p, ok := primary(act); ok {
		st.Source = "settings"
		dests := map[string]string{}
		for purpose, d := range act {
			dests[map[string]string{"media": "media", "backup": "backups"}[purpose]] = d.id
		}
		st.Fields["destinations"] = dests
		if _, ok := act["media"]; ok && confOn {
			st.Fields["overridden_config"] = true
			st.Fields["config_endpoint"], st.Fields["config_bucket"] = conf.Endpoint, conf.Bucket
		}
		if !p.readable {
			st.State, st.Missing = StateIncomplete, []string{"keys"}
			return st, nil
		}
		st.Provider = locationFields(st.Fields, p.cfg)
		st.Fields["purpose"] = purposeOf(act, p.cfg)
		if b, ok := act["backup"]; ok && b.readable && !sameLocation(b.cfg, p.cfg) {
			st.Fields["backups_endpoint"], st.Fields["backups_bucket"] = b.cfg.Endpoint, b.cfg.Bucket
		}
		st.SecretSet = p.cfg.AccessKey != "" && p.cfg.SecretKey != ""
		st.State = StateConfigured
		return st, nil
	}
	if confOn {
		st.Source = "config"
		st.Provider = locationFields(st.Fields, conf)
		st.Fields["purpose"] = "media"
		if conf.PublicURL != "" {
			st.Fields["public_url"] = conf.PublicURL
		}
		st.SecretSet = conf.AccessKey != "" && conf.SecretKey != ""
		st.State = StateConfigured
		if !st.SecretSet {
			st.State, st.Missing = StateIncomplete, []string{"keys"}
		}
		return st, nil
	}
	st.Source, st.State = "local", StateOff
	return st, nil
}

// Apply binds the bucket through the Integrations bind path, merging
// values over the bound location: fields not given keep their value, and
// the stored keys are kept when the endpoint stays the same. A nil secret
// on a new endpoint is a keys field error, so keys never move between
// providers or accounts.
func (s storageService) Apply(ctx context.Context, tx *sql.Tx, values map[string]string, secret *string) (map[string]string, error) {
	fields := map[string]string{}
	known := map[string]bool{}
	for _, f := range s.Fields() {
		known[f.Name] = true
	}
	for k := range values {
		if !known[k] {
			fields[k] = errStorageUnknown
		}
	}
	act, err := s.active(ctx)
	if err != nil {
		return nil, err
	}
	var cur media.S3Config
	curProvider, curAccount, curPurpose := "", "", ""
	if p, ok := primary(act); ok && p.readable {
		cur = p.cfg
		curProvider, curAccount = providerOf(cur.Endpoint)
		curPurpose = purposeOf(act, cur)
	}
	get := func(k, stored string) string {
		if v, ok := values[k]; ok {
			return strings.TrimSpace(v)
		}
		return stored
	}
	provider := get("provider", curProvider)
	region := get("region", cur.Region)
	bucket := get("bucket", cur.Bucket)
	purpose := get("purpose", curPurpose)
	var endpoint string
	switch provider {
	case "r2":
		stored := ""
		if curProvider == "r2" {
			stored = curAccount
		}
		account := strings.ToLower(get("account", stored))
		if account == "" {
			fields["account"] = errStorageRequired
		} else if !r2AccountPattern.MatchString(account) {
			fields["account"] = errStorageAccount
		} else {
			endpoint = "https://" + account + r2Suffix
		}
	case "s3":
		stored := ""
		if curProvider == "s3" {
			stored = cur.Endpoint
		}
		if endpoint = get("endpoint", stored); endpoint == "" {
			fields["endpoint"] = errStorageRequired
		}
	case "":
		fields["provider"] = errStorageRequired
	default:
		fields["provider"] = errStorageProvider
	}
	if bucket == "" {
		fields["bucket"] = errStorageRequired
	}
	// A managed destination serves media through GoRun's /media/; it keeps
	// no public base, so one given here would be silently ignored.
	if values["public_url"] != "" {
		fields["public_url"] = errStoragePublicURL
	}
	purposes, ok := storagePurposes[purpose]
	if purpose == "" {
		fields["purpose"] = errStorageRequired
	} else if !ok {
		fields["purpose"] = "admin.integrations.storage.error.purpose"
	}
	var access, secretKey string
	if secret != nil {
		a, k, found := strings.Cut(strings.TrimRight(*secret, "\r\n"), ":")
		access, secretKey = strings.TrimSpace(a), strings.TrimSpace(k)
		if !found || access == "" || secretKey == "" {
			fields["keys"] = errStorageKeys
		}
	} else if endpoint != "" && cur.Endpoint != "" && strings.TrimRight(endpoint, "/") == strings.TrimRight(cur.Endpoint, "/") {
		access, secretKey = cur.AccessKey, cur.SecretKey
	} else {
		fields["keys"] = errStorageRequired
	}
	if len(fields) > 0 {
		return fields, nil
	}
	in := admin.StorageBindInput{Source: "s3", Endpoint: endpoint, Region: region, Bucket: bucket, AccessKey: access, SecretKey: secretKey, Purposes: purposes, ConfirmAll: purpose == "both"}
	_, bindFields, err := admin.BindStorageTx(ctx, s.d, tx, in)
	if err != nil {
		return nil, err
	}
	for k, v := range bindFields {
		switch k {
		case "purposes", "confirm_both":
			fields["purpose"] = v
		case "source":
			fields["provider"] = v
		case "endpoint":
			if provider == "r2" {
				fields["account"] = v
			} else {
				fields["endpoint"] = v
			}
		case "region":
			fields["region"] = v
		case "access_key", "connection_id":
			fields["keys"] = v
		default: // bucket, and name (derived from the bucket)
			fields["bucket"] = v
		}
	}
	return fields, nil
}

// Test has no synchronous safe test here: the bucket checks are the
// admin's queued observations (Integrations → Storage).
func (storageService) Test(context.Context, string) (TestResult, error) {
	return TestResult{OK: false, At: time.Now().UTC(), Message: "no test"}, nil
}

// Off stops using the bound destinations (media goes back to GORUN_S3_*
// or the data directory); the Connections profiles and the destination
// records stay, for the admin to bind again.
func (s storageService) Off(ctx context.Context, tx *sql.Tx) error {
	for _, k := range []string{"storage.active.media", "storage.active.backup"} {
		if err := s.d.Settings.DeleteTx(ctx, tx, k); err != nil {
			return err
		}
	}
	return nil
}
