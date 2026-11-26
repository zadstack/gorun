package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/observe"
	"github.com/zadstack/gorun/internal/settings"
)

// Storage and backup bucket binding for the Integrations hub.
//
// Binding reuses the existing pieces: a saved S3 connection profile
// (connections.profile.*) carries the endpoint, region, bucket and the
// S3 access keys; a managed destination (storage.destination.*) is the
// encrypted copy activated for one purpose; storage.active.media and
// storage.active.backup select the destination each purpose uses. Media
// objects live under sites/<site-id>/uploads/ and backups under
// backups/<site-id>/, so the two never share a prefix even in one bucket.
// A Cloudflare API token is not an S3 credential: R2 needs its own access
// key pair, created in the R2 dashboard for the bucket.

// storageBindInput is the form / API body. Source "r2" derives the
// endpoint from the selected Cloudflare account; "s3" takes it verbatim.
type storageBindInput struct {
	Source     string   `form:"source" json:"source"`
	Name       string   `form:"name" json:"name"`
	Endpoint   string   `form:"endpoint" json:"endpoint"`
	Region     string   `form:"region" json:"region"`
	Bucket     string   `form:"bucket" json:"bucket"`
	AccessKey  string   `form:"access_key" json:"access_key"`
	SecretKey  string   `form:"secret_key" json:"secret_key"`
	Purposes   []string `form:"purposes" json:"purposes"`
	ConfirmAll bool     `form:"confirm_both" json:"confirm_both"`
	Connection string   `form:"connection_id" json:"connection_id" doc:"Reuse the credentials of an existing S3 profile instead of entering keys"`
}

type storageBindResult struct {
	ConnectionID string            `json:"connection_id"`
	Destinations map[string]string `json:"destinations" doc:"purpose → destination id"`
	Prefixes     map[string]string `json:"prefixes" doc:"purpose → fixed object prefix inside the bucket"`
}

// storagePrefixes are the fixed per-purpose prefixes (see media and backup).
func storagePrefixes(site string) map[string]string {
	if site == "" {
		site = "<site-id>"
	}
	return map[string]string{"media": "sites/" + site + "/uploads/", "backup": "backups/" + site + "/"}
}

// r2Endpoint is the S3 API endpoint of an account's R2 buckets.
func r2Endpoint(accountID string) string {
	return "https://" + accountID + ".r2.cloudflarestorage.com"
}

// StorageBindInput is the storage bind body, for callers outside admin
// (the services layer).
type StorageBindInput = storageBindInput

// storageBind is a validated bind, ready to write: the profile (rewritten
// or kept), the destinations per purpose and the Cloudflare snapshot an
// R2 binding was validated against.
type storageBind struct {
	h        *handlers
	c        connectionRecord
	expected string
	rewrite  bool
	dests    map[string]media.Destination
	cf       *cfSnapshot
}

// storageHandlers is the part of handlers the bind path needs, built from
// d: the admin's observe service when it is published (a server), else an
// empty one (a CLI boot), in which an R2 inventory lookup finds nothing.
func storageHandlers(d *kernel.Deps) *handlers {
	obs, ok := kernel.Get[*observe.Service](d, observe.ServiceName)
	if !ok {
		obs = observe.New(d.DB, d.Jobs, d.Log)
	}
	return &handlers{d: d, observe: obs}
}

// BindStorage is the Integrations bind path over d: it validates in,
// saves (or updates) the S3 profile and activates a destination per
// purpose in one transaction, then queues the read-only bucket check when
// the admin's checks are registered. It returns purpose → destination id
// and field errors (i18n keys); nothing is written when any field fails.
func BindStorage(ctx context.Context, d *kernel.Deps, in StorageBindInput) (map[string]string, map[string]string, error) {
	h := storageHandlers(d)
	b, fields, err := h.prepareStorageBind(ctx, in)
	if err != nil || len(fields) > 0 {
		return nil, fields, err
	}
	var ids map[string]string
	if err := db.InTx(ctx, d.DB, func(tx *sql.Tx) (err error) { ids, err = b.writeTx(ctx, tx); return err }); err != nil {
		return nil, nil, err
	}
	b.queueCheck(ctx)
	return ids, nil, nil
}

// BindStorageTx is BindStorage inside the caller's transaction, for a
// caller that commits more with it. It queues no bucket check: that
// writes outside tx and would wait on tx's own lock.
func BindStorageTx(ctx context.Context, d *kernel.Deps, tx *sql.Tx, in StorageBindInput) (map[string]string, map[string]string, error) {
	b, fields, err := storageHandlers(d).prepareStorageBind(ctx, in)
	if err != nil || len(fields) > 0 {
		return nil, fields, err
	}
	ids, err := b.writeTx(ctx, tx)
	return ids, nil, err
}

// bindStorage is BindStorage for the admin form and API: same validation
// and transaction, plus the result's profile id and object prefixes.
func (h *handlers) bindStorage(ctx context.Context, in storageBindInput) (storageBindResult, map[string]string, error) {
	var res storageBindResult
	b, fields, err := h.prepareStorageBind(ctx, in)
	if err != nil || len(fields) > 0 {
		return res, fields, err
	}
	var ids map[string]string
	if err := db.InTx(ctx, h.d.DB, func(tx *sql.Tx) (err error) { ids, err = b.writeTx(ctx, tx); return err }); err != nil {
		return res, nil, err
	}
	res.ConnectionID, res.Destinations = b.c.ID, ids
	site, _ := media.StorageSiteID(ctx, h.d.DB)
	res.Prefixes = storagePrefixes(site)
	b.queueCheck(ctx)
	return res, nil, nil
}

// prepareStorageBind validates in and builds the writes, reading only
// committed state. Field errors are i18n keys.
func (h *handlers) prepareStorageBind(ctx context.Context, in storageBindInput) (*storageBind, map[string]string, error) {
	fields := map[string]string{}
	purposes := map[string]bool{}
	for _, p := range in.Purposes {
		switch p {
		case "media", "backup":
			purposes[p] = true
		default:
			fields["purposes"] = "admin.integrations.storage.error.purpose"
		}
	}
	if len(purposes) == 0 {
		fields["purposes"] = "admin.integrations.storage.error.purpose"
	}
	if purposes["media"] && purposes["backup"] && !in.ConfirmAll {
		fields["confirm_both"] = "admin.integrations.storage.error.confirm"
	}
	c := connectionRecord{Kind: "s3", Name: strings.TrimSpace(in.Name), Bucket: strings.TrimSpace(in.Bucket), Region: strings.TrimSpace(in.Region)}
	if c.Region == "" {
		c.Region = "auto"
	}
	var cfBindingSnapshot *cfSnapshot
	switch in.Source {
	case "r2":
		snapshot, err := h.cloudflareSnapshot(ctx)
		if err != nil {
			return nil, nil, err
		}
		if snapshot.AccountID == "" {
			fields["source"] = "admin.integrations.storage.error.account"
		} else if !h.cloudflareResourceDiscoveredFor(ctx, snapshot, "r2", c.Bucket) {
			fields["source"] = "admin.integrations.storage.error.inventory"
		} else {
			c.Endpoint = r2Endpoint(snapshot.AccountID)
			cfBindingSnapshot = &snapshot
		}
	case "s3", "":
		var err error
		if c.Endpoint, err = normalizeConnectionEndpoint(in.Endpoint); err != nil {
			fields["endpoint"] = "admin.integrations.storage.error.endpoint"
		}
	default:
		fields["source"] = "admin.integrations.storage.error.source"
	}
	if c.Name == "" {
		c.Name = c.Bucket
	}
	if fields["endpoint"] == "" && fields["source"] == "" && validateConnection(c) != nil {
		if !connectionBucketPattern.MatchString(c.Bucket) {
			fields["bucket"] = "admin.integrations.storage.error.bucket"
		} else if !connectionRegionPattern.MatchString(c.Region) {
			fields["region"] = "admin.integrations.storage.error.region"
		} else {
			fields["name"] = "admin.connections.invalid"
		}
	}
	// Credentials: new keys, or the keys of an existing S3 profile. An
	// existing profile is reused in place when its location matches.
	secrets := connectionSecrets{AccessKey: strings.TrimSpace(in.AccessKey), SecretKey: strings.TrimSpace(in.SecretKey)}
	var previous connectionRecord
	if in.Connection != "" {
		p, err := h.loadConnection(ctx, in.Connection)
		if err != nil || p.Kind != "s3" {
			fields["connection_id"] = "admin.connections.invalid"
		} else {
			previous = p
			if secrets.AccessKey == "" && secrets.SecretKey == "" {
				if secrets, err = h.connectionSecrets(p); err != nil {
					fields["connection_id"] = "admin.integrations.storage.error.credentials"
				}
			}
		}
	}
	if !validConnectionSecret(secrets.AccessKey) || !validConnectionSecret(secrets.SecretKey) || secrets.AccessKey == "" || secrets.SecretKey == "" {
		fields["access_key"] = "admin.integrations.storage.error.credentials"
	}
	if len(fields) > 0 {
		return nil, fields, nil
	}
	if previous.ID != "" && (previous.Endpoint != c.Endpoint || previous.Bucket != c.Bucket || previous.Region != c.Region) {
		previous = connectionRecord{} // a different location becomes a new profile
	}
	if previous.ID == "" {
		// The same location already saved as a profile is updated in place
		// (credential rotation keeps the destination identity), never duplicated.
		existing, err := h.listConnections(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range existing {
			if p.Kind == "s3" && p.Endpoint == c.Endpoint && p.Bucket == c.Bucket && p.Region == c.Region {
				previous = p
				break
			}
		}
	}
	expected := ""
	rewrite := true
	if previous.ID != "" {
		c.ID, expected = previous.ID, previous.Version
		if strings.TrimSpace(in.Name) == "" {
			c.Name = previous.Name // a blank name never renames an existing profile
		}
		// Only the binding changed: keep the profile, its version and its
		// saved check evidence instead of rewriting and clearing them.
		if prev, err := h.connectionSecrets(previous); err == nil && prev == secrets && c.Name == previous.Name {
			rewrite = false
			c = previous
		}
	} else {
		c.ID = db.NewID()
	}
	if rewrite {
		var err error
		if c.Secret, err = h.encryptConnectionSecrets(secrets); err != nil {
			return nil, nil, err
		}
		c = changedConnection(c)
	}
	cfg := media.S3Config{Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, AccessKey: secrets.AccessKey, SecretKey: secrets.SecretKey}
	dests := map[string]media.Destination{}
	for p := range purposes {
		d, err := media.PrepareDestination(c.Name, p, cfg, h.d.Config.SecretKey)
		if err != nil {
			fields["bucket"] = "admin.integrations.storage.error.bucket"
			return nil, fields, nil
		}
		dests[p] = d
	}
	return &storageBind{h: h, c: c, expected: expected, rewrite: rewrite, dests: dests, cf: cfBindingSnapshot}, nil, nil
}

// writeTx writes the profile and destinations in tx: a failure leaves
// neither. It returns purpose → destination id.
func (b *storageBind) writeTx(ctx context.Context, tx *sql.Tx) (map[string]string, error) {
	h, c := b.h, b.c
	if b.cf != nil {
		current := map[string]string{}
		rows, err := tx.QueryContext(ctx, `SELECT key,value_json FROM settings WHERE key IN (?,?)`, settingCFToken, settingCFAccount)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key, value string
			if err := rows.Scan(&key, &value); err != nil {
				rows.Close()
				return nil, err
			}
			var valid any
			if json.Unmarshal([]byte(value), &valid) != nil {
				rows.Close()
				return nil, errConnectionConflict
			}
			current[key] = value
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if !maps.Equal(b.cf.raw, current) {
			return nil, errConnectionConflict
		}
	}
	if b.rewrite {
		if err := h.writeConnectionTx(ctx, tx, c, b.expected, "connections.save", false); err != nil {
			return nil, err
		}
	}
	ids := map[string]string{}
	for p, d := range b.dests {
		if err := media.SaveDestinationTx(ctx, tx, h.d.Settings, d); err != nil {
			return nil, err
		}
		h.d.Audit.RecordTx(ctx, tx, "storage.activate", "destination", d.ID, map[string]string{"purpose": d.Purpose, "connection": c.ID, "via": "integrations"})
		ids[p] = d.ID
	}
	return ids, nil
}

// queueCheck queues the read-only reachability check of the profile; the
// write/read/delete probe is a separate explicit request under
// system.capability_test. Without the admin's checks (a CLI boot) there
// is nothing to queue.
func (b *storageBind) queueCheck(ctx context.Context) {
	if _, err := b.h.observe.Refresh(ctx, kindConnection, b.c.ID); errors.Is(err, observe.ErrUnknownKind) {
		return // no admin checks registered here (a CLI boot)
	} else if err != nil {
		b.h.d.Log.Warn("integrations: queue bucket check", "err", err)
	}
}

// storageBinding describes one purpose's resolved destination and where
// it comes from, for display: no credentials, only location.
type storageBinding struct {
	Purpose       string `json:"purpose"`
	Source        string `json:"source" enum:"settings,config,local,none" doc:"settings: a managed destination; config: the [s3] section or GORUN_S3_* environment; local: the data directory"`
	DestinationID string `json:"destination_id,omitempty"`
	Name          string `json:"name,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	Region        string `json:"region,omitempty"`
	Bucket        string `json:"bucket,omitempty"`
	Prefix        string `json:"prefix"`
	Configured    bool   `json:"configured"`
	Reason        string `json:"reason"`
	Remediation   string `json:"remediation"`
	ConnectionID  string `json:"connection_id,omitempty" doc:"The S3 profile with the same location, when one exists"`
	Readable      bool   `json:"readable" doc:"False when the destination is saved but its credentials cannot be decrypted"`
}

func (h *handlers) storageBindings(ctx context.Context) ([]storageBinding, error) {
	site, _ := media.StorageSiteID(ctx, h.d.DB)
	prefixes := storagePrefixes(site)
	profiles, err := h.listConnections(ctx)
	if err != nil {
		return nil, err
	}
	var out []storageBinding
	for _, purpose := range []string{"media", "backup"} {
		b := storageBinding{Purpose: purpose, Prefix: prefixes[purpose], Source: "none", Reason: "not_configured"}
		var id string
		err := h.d.Settings.Get(ctx, "storage.active."+purpose, &id)
		if err != nil && !errors.Is(err, settings.ErrNotFound) {
			return nil, err
		}
		if id != "" {
			b.Source, b.DestinationID = "settings", id
			d, err := media.LoadDestination(ctx, h.d.Settings, id)
			if err != nil || d.Purpose != purpose {
				b.Reason, b.Remediation = "destination_unreadable", "Re-bind the bucket for this purpose."
			} else {
				b.Name = d.Name
				if cfg, err := media.DestinationConfig(d, h.d.Config.SecretKey); err != nil {
					b.Reason, b.Remediation = "destination_credentials_unreadable", "Re-enter the bucket credentials with the current site secret key."
				} else {
					b.Endpoint, b.Region, b.Bucket, b.Readable, b.Configured = cfg.Endpoint, cfg.Region, cfg.Bucket, true, true
					b.Reason, b.Remediation = "configured_not_verified", "Run the read-only check, then the write/read/delete probe."
				}
			}
		} else if purpose == "media" {
			if st, ok := h.activeStorage(); ok {
				info := st.Describe(ctx)
				b.Configured, b.Reason, b.Remediation = info.Configured, info.Reason, info.Remediation
				if info.Kind == "s3" {
					b.Source, b.Endpoint, b.Region, b.Bucket, b.Readable = "config", h.d.Config.S3.Endpoint, h.d.Config.S3.Region, h.d.Config.S3.Bucket, true
				} else {
					b.Source = "local"
				}
			}
		} else {
			b.Remediation = "Bind a private bucket for encrypted backups; nothing is uploaded until an explicit remote backup."
		}
		if b.Endpoint != "" {
			for _, p := range profiles {
				if p.Kind == "s3" && strings.TrimRight(p.Endpoint, "/") == strings.TrimRight(b.Endpoint, "/") && p.Bucket == b.Bucket && p.Region == b.Region {
					b.ConnectionID = p.ID
					break
				}
			}
		}
		out = append(out, b)
	}
	return out, nil
}

// r2BucketBound reports whether a discovered R2 bucket of the selected
// account is one of the active destinations.
func r2BucketBound(bindings []storageBinding, accountID, bucket string) []string {
	var purposes []string
	if accountID == "" {
		return purposes
	}
	host := strings.ToLower(accountID + ".r2.cloudflarestorage.com")
	for _, b := range bindings {
		if b.Bucket != bucket || b.Endpoint == "" {
			continue
		}
		if u, err := url.Parse(b.Endpoint); err == nil && strings.ToLower(u.Hostname()) == host {
			purposes = append(purposes, b.Purpose)
		}
	}
	return purposes
}
