package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/media"
)

const (
	testR2Account = "0123456789abcdef0123456789abcdef"
	testAccessKey = "AKIDSTORAGETEST0001"
	testSecretKey = "storage-secret-value-0001"
)

func storageKeys() *string {
	s := testAccessKey + ":" + testSecretKey
	return &s
}

func TestStorageSetR2BindsMediaDestination(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Storage(d))
	st, fields, err := r.Set(ctx, "storage", map[string]string{"provider": "r2", "account": testR2Account, "bucket": "site-media", "purpose": "media"}, storageKeys(), "cli")
	if err != nil || len(fields) > 0 {
		t.Fatalf("set: %v %v", fields, err)
	}
	if st.State != StateConfigured || st.Source != "settings" || st.Provider != "r2" || st.Fields["bucket"] != "site-media" || st.Fields["account"] != testR2Account || st.Fields["purpose"] != "media" || !st.SecretSet {
		t.Fatalf("status: %+v", st)
	}
	id := d.Settings.GetString(ctx, "storage.active.media", "")
	if id == "" {
		t.Fatal("storage.active.media not set")
	}
	if b := d.Settings.GetString(ctx, "storage.active.backup", ""); b != "" {
		t.Fatalf("backup bound too: %q", b)
	}
	dest, err := media.LoadDestination(ctx, d.Settings, id)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := media.DestinationConfig(dest, d.Config.SecretKey)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "https://"+testR2Account+".r2.cloudflarestorage.com" || cfg.Bucket != "site-media" || cfg.AccessKey != testAccessKey || cfg.SecretKey != testSecretKey {
		t.Fatalf("destination: %+v", cfg)
	}

	// A partial set keeps the stored location and keys: only the purpose changes.
	st, fields, err = r.Set(ctx, "storage", map[string]string{"purpose": "both"}, nil, "cli")
	if err != nil || len(fields) > 0 || st.Fields["purpose"] != "both" || st.Fields["bucket"] != "site-media" {
		t.Fatalf("partial set: %+v %v %v", st, fields, err)
	}
	if d.Settings.GetString(ctx, "storage.active.backup", "") == "" {
		t.Fatal("backup not bound by purpose=both")
	}
}

func TestStorageStatusSaysConfigSourceAndOverride(t *testing.T) {
	t.Setenv("GORUN_S3_ENDPOINT", "https://s3.example.com")
	t.Setenv("GORUN_S3_BUCKET", "env-bucket")
	t.Setenv("GORUN_S3_ACCESS_KEY", "ENVACCESS")
	t.Setenv("GORUN_S3_SECRET_KEY", "env-secret")
	d := testDeps(t)
	ctx := context.Background()
	s := Storage(d)
	st, err := s.Status(ctx)
	if err != nil || st.Source != "config" || st.State != StateConfigured || st.Fields["bucket"] != "env-bucket" {
		t.Fatalf("config status: %+v %v", st, err)
	}
	if _, ok := st.Fields["overridden_config"]; ok {
		t.Fatalf("no override yet: %+v", st)
	}
	r := NewRegistry(d, s)
	st, fields, err := r.Set(ctx, "storage", map[string]string{"provider": "r2", "account": testR2Account, "bucket": "site-media", "purpose": "media"}, storageKeys(), "cli")
	if err != nil || len(fields) > 0 {
		t.Fatalf("set: %v %v", fields, err)
	}
	if st.Source != "settings" || st.Fields["overridden_config"] != true || st.Fields["bucket"] != "site-media" {
		t.Fatalf("override status: %+v", st)
	}
}

func TestStorageKeysLineMustHaveBothParts(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Storage(d))
	for _, line := range []string{"abc", "abc:", ":abc"} {
		secret := line
		_, fields, err := r.Set(ctx, "storage", map[string]string{"provider": "r2", "account": testR2Account, "bucket": "site-media", "purpose": "media"}, &secret, "cli")
		if err != nil || fields["keys"] == "" {
			t.Fatalf("%q: %v %v", line, fields, err)
		}
	}
	if id := d.Settings.GetString(ctx, "storage.active.media", ""); id != "" {
		t.Fatalf("bound with bad keys: %q", id)
	}
}

func TestStorageStatusHasNoKeys(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Storage(d))
	if _, fields, err := r.Set(ctx, "storage", map[string]string{"provider": "s3", "endpoint": "https://s3.example.com", "region": "eu-west-1", "bucket": "site-backups", "purpose": "backups"}, storageKeys(), "cli"); err != nil || len(fields) > 0 {
		t.Fatalf("set: %v %v", fields, err)
	}
	st, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), testAccessKey) || strings.Contains(string(raw), testSecretKey) {
		t.Fatalf("status leaks keys: %s", raw)
	}
	if !st[0].SecretSet || st[0].Fields["purpose"] != "backups" || st[0].Provider != "s3" {
		t.Fatalf("status: %s", raw)
	}
}

func TestStorageOffKeepsProfiles(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Storage(d))
	if _, fields, err := r.Set(ctx, "storage", map[string]string{"provider": "r2", "account": testR2Account, "bucket": "site-media", "purpose": "both"}, storageKeys(), "cli"); err != nil || len(fields) > 0 {
		t.Fatalf("set: %v %v", fields, err)
	}
	if err := r.Off(ctx, "storage", "cli"); err != nil {
		t.Fatal(err)
	}
	if d.Settings.GetString(ctx, "storage.active.media", "") != "" || d.Settings.GetString(ctx, "storage.active.backup", "") != "" {
		t.Fatal("still active after off")
	}
	profiles, err := d.Settings.All(ctx, "connections.profile.")
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles: %d %v", len(profiles), err)
	}
	st, err := Storage(d).Status(ctx)
	if err != nil || st.State != StateOff || st.Source != "local" {
		t.Fatalf("status after off: %+v %v", st, err)
	}
}
