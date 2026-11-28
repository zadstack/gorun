package services

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// backupsWithRemote binds site-media for backups, points the backups
// service at it, and returns the registry over a backups service that
// uploads to s3.
func backupsWithRemote(t *testing.T, s3 *fakeS3, retain string) *Registry {
	t.Helper()
	d := testDeps(t)
	r := NewRegistry(d, Storage(d), backupsService{d: d, client: s3.client(t)})
	bindStorage(t, r, "backups")
	id := d.Settings.GetString(context.Background(), "storage.active.backup", "")
	if _, fields, err := r.Set(context.Background(), "backups", map[string]string{"every_hours": "24", "retain": retain, "destination": id}, nil, "cli"); err != nil || len(fields) > 0 {
		t.Fatalf("backups: %v %v", fields, err)
	}
	return r
}

func TestBackupsRunLocal(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Backups(d))
	res, err := r.Run(ctx, "backups", "cli")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Destination != "local" || res.Remote != "" || res.Stamp == "" || res.Bytes <= 0 || res.At.IsZero() || len(res.Objects) != 0 {
		t.Fatalf("result: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(d.Config.DataDir, "backups", res.Stamp+".manifest.json")); err != nil {
		t.Fatalf("no local pair: %v", err)
	}
	st, _ := r.Status(ctx, "backups")
	if st.Fields["last_success"] != res.At.Format(time.RFC3339) {
		t.Fatalf("last_success %v, run at %s", st.Fields["last_success"], res.At.Format(time.RFC3339))
	}
}

func TestBackupsRunUploadsToTheDestination(t *testing.T) {
	s3 := newFakeS3()
	r := backupsWithRemote(t, s3, "0")
	res, err := r.Run(context.Background(), "backups", "cli")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Remote != "success" || res.Destination == "local" || len(res.Objects) < 2 {
		t.Fatalf("result: %+v", res)
	}
	stored := strings.Join(s3.keys(), " ")
	for _, o := range res.Objects {
		if !strings.HasPrefix(o, "backups/") || !strings.HasSuffix(o, ".gorb") || !strings.Contains(stored, "site-media/"+o) {
			t.Fatalf("object %s not in the bucket (%s)", o, stored)
		}
	}
	var last struct {
		Stamp  string `json:"stamp"`
		Status string `json:"status"`
	}
	if err := r.d.Settings.Get(context.Background(), "storage.backup.last_success", &last); err != nil || last.Stamp != res.Stamp || last.Status != "success" {
		t.Fatalf("remote last_success: %+v %v", last, err)
	}
}

func TestBackupsRunWithAFailedUploadKeepsTheLocalPair(t *testing.T) {
	s3 := newFakeS3()
	s3.fail[http.MethodPut] = http.StatusForbidden
	r := backupsWithRemote(t, s3, "0")
	res, err := r.Run(context.Background(), "backups", "cli")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Remote != "failed" || res.Warning != "remote" || res.Stamp == "" || !strings.Contains(res.Message, "saved locally") {
		t.Fatalf("result: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(r.d.Config.DataDir, "backups", res.Stamp+".manifest.json")); err != nil {
		t.Fatalf("local pair gone: %v", err)
	}
}

func TestBackupsRunHonoursRetain(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Backups(d))
	if _, fields, err := r.Set(ctx, "backups", map[string]string{"every_hours": "24", "retain": "1"}, nil, "cli"); err != nil || len(fields) > 0 {
		t.Fatalf("set: %v %v", fields, err)
	}
	first, err := r.Run(ctx, "backups", "cli")
	if err != nil || !first.OK {
		t.Fatalf("first: %+v %v", first, err)
	}
	time.Sleep(1100 * time.Millisecond) // stamps are to the second
	second, err := r.Run(ctx, "backups", "cli")
	if err != nil || !second.OK || second.Pruned == 0 {
		t.Fatalf("second: %+v %v", second, err)
	}
	if _, err := os.Stat(filepath.Join(d.Config.DataDir, "backups", first.Stamp+".manifest.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first pair kept with retain=1: %v", err)
	}
}

func TestRunWithoutARunAction(t *testing.T) {
	d := testDeps(t)
	r := NewRegistry(d, Chat(d))
	if _, err := r.Run(context.Background(), "chat", "cli"); !errors.Is(err, ErrNoRun) {
		t.Fatalf("chat: %v", err)
	}
	if _, err := r.Run(context.Background(), "nope", "cli"); !errors.Is(err, ErrUnknownService) {
		t.Fatalf("unknown: %v", err)
	}
}
