package backup

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestManifestHMACRoundTripAndWrongKey(t *testing.T) {
	dir, path := backupSource(t)
	key := bytes.Repeat([]byte{7}, 32)
	res, err := CreateWith(dir, path, time.Now(), Options{SecretKey: key})
	if err != nil {
		t.Fatal(err)
	}
	m, err := ReadManifest(res.Manifest)
	if err != nil || m.HMAC == "" {
		t.Fatalf("signed manifest: %+v %v", m, err)
	}
	ok, err := CheckManifestHMAC(m, key)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if ok, err := CheckManifestHMAC(m, bytes.Repeat([]byte{9}, 32)); err == nil && ok {
		t.Fatal("wrong key accepted")
	}
	rep, err := InspectWith(res.Manifest, key)
	if err != nil || rep.Readiness == "blocked" || !rep.IntegrityOK {
		t.Fatalf("signed inspect: %+v %v", rep, err)
	}
}

func TestManifestHMACBlocksTamperedSignature(t *testing.T) {
	dir, path := backupSource(t)
	key := bytes.Repeat([]byte{3}, 32)
	res, err := CreateWith(dir, path, time.Now(), Options{SecretKey: key})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte(`"hmac": "`), []byte(`"hmac": "00`), 1)
	if err := os.WriteFile(res.Manifest, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := InspectWith(res.Manifest, key)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Readiness != "blocked" {
		t.Fatalf("tampered hmac readiness=%s errors=%v", rep.Readiness, rep.Errors)
	}
	found := false
	for _, e := range rep.Errors {
		if e == "manifest_hmac_mismatch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected manifest_hmac_mismatch, got %v", rep.Errors)
	}
}

func TestManifestHMACBlocksMissingKey(t *testing.T) {
	dir, path := backupSource(t)
	key := bytes.Repeat([]byte{5}, 32)
	res, err := CreateWith(dir, path, time.Now(), Options{SecretKey: key})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := InspectWith(res.Manifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Readiness != "blocked" {
		t.Fatalf("signed manifest without key: %+v", rep)
	}
}

func TestManifestHMACRequiredWhenKeySupplied(t *testing.T) {
	dir, path := backupSource(t)
	res, err := Create(dir, path, time.Now()) // unsigned legacy pair
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{1}, 32)
	rep, err := InspectWith(res.Manifest, key)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Readiness != "blocked" {
		t.Fatalf("unsigned with key should block: %+v", rep)
	}
	found := false
	for _, e := range rep.Errors {
		if e == "manifest_hmac_required" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected manifest_hmac_required, got %v", rep.Errors)
	}
}
