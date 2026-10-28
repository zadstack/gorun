package backup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadbackReceiptRequiresCorrectKeyAndAuthenticatedMetadata(t *testing.T) {
	dir, database := backupSource(t)
	key := bytes.Repeat([]byte{42}, 32)
	res, err := CreateWith(dir, database, time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), Options{SecretKey: key})
	if err != nil {
		t.Fatal(err)
	}
	receipt := UploadReceipt{State: "uploaded", ReadBackVerified: true, DestinationID: "private-destination", Files: []UploadFile{{Name: res.Stamp + ".db", State: "uploaded", ObjectKey: "backups/site/" + res.Stamp + ".db.gorb", CheckedAt: "fixture"}, {Name: res.Stamp + ".tar.gz", State: "uploaded", ObjectKey: "backups/site/" + res.Stamp + ".tar.gz.gorb", CheckedAt: "fixture"}}}
	if err = StoreUploadReceiptWithKey(res, receipt, key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(dir), res.Stamp+".remote.json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := ListWithKey(dir, key)
	if err != nil || len(listed) != 1 || !listed[0].Remote.ReadBackVerified {
		t.Fatal("keyed admin entry lost readback", err)
	}
	wrong, err := ListWithKey(dir, bytes.Repeat([]byte{1}, 32))
	if err != nil || wrong[0].Remote.ReadBackVerified || wrong[0].Remote.State != "unverified" {
		t.Fatal("wrong list key trusted")
	}
	verified := receiptAtWithKey(Dir(dir), res.Stamp, key)
	if !verified.ReadBackVerified || verified.State != "uploaded" {
		t.Fatal("signed proof rejected")
	}
	if receiptAt(Dir(dir), res.Stamp).ReadBackVerified {
		t.Fatal("unkeyed inspection trusted readback")
	}
	if receiptAtWithKey(Dir(dir), res.Stamp, bytes.Repeat([]byte{1}, 32)).State != "unverified" {
		t.Fatal("wrong site key trusted")
	}
	for _, field := range []string{"destination", "object", "stamp", "flag", "manifest", "signature"} {
		t.Run(field, func(t *testing.T) {
			var changed UploadReceipt
			if err := json.Unmarshal(encoded, &changed); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "destination":
				changed.DestinationID = "another"
			case "object":
				changed.Files[0].ObjectKey = "other-object"
			case "stamp":
				changed.Stamp = "20261013T120000Z"
			case "flag":
				changed.ReadBackVerified = false
			case "manifest":
				changed.ManifestHMAC = "00"
			case "signature":
				changed.HMAC = ""
			}
			raw, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			observed := receiptAtWithKey(Dir(dir), res.Stamp, key)
			if observed.ReadBackVerified || field != "signature" && observed.State != "unverified" {
				t.Fatal("modified proof trusted", field)
			}
		})
	}
	if err = os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	copied, err := CreateWith(dir, database, time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC), Options{SecretKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(Dir(dir), copied.Stamp+".remote.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if receiptAtWithKey(Dir(dir), copied.Stamp, key).State != "unverified" {
		t.Fatal("copied stamp trusted")
	}
	inspected, err := InspectWith(res.Manifest, key)
	if err != nil || !inspected.Remote.ReadBackVerified {
		t.Fatal("keyed inspection lost readback proof", err)
	}
	for _, check := range inspected.CutoverChecklist {
		if check == "remote_readback_not_verified" {
			t.Fatal("verified historical readback hidden")
		}
	}
}
