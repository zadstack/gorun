package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/update"
)

func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func releaseIndexJSON(t *testing.T, args ...string) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := Run(append([]string{"release-index"}, args...), strings.NewReader(""), &out); err != nil {
		t.Fatalf("release-index: %v", err)
	}
	return out.Bytes()
}

var (
	imageDigest   = "sha256:" + strings.Repeat("a", 64)
	archiveDigest = "sha256:" + strings.Repeat("b", 64)
)

// The published index must be the document the staged-update client reads:
// it resolves the version, picks the configured artifact and carries the
// migration list, so preflight reports release_data "checked".
func TestReleaseIndexIsReadByTheUpdateClient(t *testing.T) {
	withVersion(t, "v0.18.0")
	body := releaseIndexJSON(t,
		"--artifact", "ghcr.io/zadstack/gorun:0.18.0="+imageDigest,
		// Upper-case hex (as some tools print it) is normalised.
		"--artifact", "gorun-0.18.0-linux-amd64.tar.gz=sha256:"+strings.ToUpper(archiveDigest[7:]))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src, err := update.NewReleaseIndex(srv.URL, "", "linux-amd64", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	latest, err := src.Latest(ctx)
	if err != nil || latest.Version != "0.18.0" {
		t.Fatalf("latest = %+v, %v; want 0.18.0", latest, err)
	}
	a, err := src.Resolve(ctx, "0.18.0")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "gorun-0.18.0-linux-amd64.tar.gz" || a.Digest != archiveDigest {
		t.Fatalf("resolved %s %s", a.ID, a.Digest)
	}
	if len(a.Migrations) == 0 || !slices.IsSorted(a.Migrations) {
		t.Fatalf("migrations must be a non-empty sorted list, got %d", len(a.Migrations))
	}
	if a.Themes != nil {
		t.Fatalf("themes must stay unknown (a site may run its own theme), got %v", a.Themes)
	}
}

// Preflight refuses a target whose list misses any applied migration, so the
// index must name every migration a fresh site records, optional modules and
// kernel infrastructure included.
func TestReleaseIndexCoversAFreshlyMigratedDatabase(t *testing.T) {
	withVersion(t, "0.18.0")
	var doc releaseIndex
	if err := json.Unmarshal(releaseIndexJSON(t, "--artifact", "x="+imageDigest), &doc); err != nil {
		t.Fatal(err)
	}
	listed := doc.Releases[0].Migrations

	d, err := db.Open(filepath.Join(t.TempDir(), "gorun.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err := kernel.MigrateAll(ctx, d, Modules()); err != nil {
		t.Fatal(err)
	}
	rows, err := d.QueryContext(ctx, `SELECT module || '/' || name FROM schema_migrations`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	applied := 0
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		applied++
		if _, found := slices.BinarySearch(listed, key); !found {
			t.Errorf("applied migration %s is missing from the release index", key)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if applied == 0 {
		t.Fatal("no migrations applied")
	}
}

func TestReleaseIndexRefusesBuildVersionsAndBadArtifacts(t *testing.T) {
	cases := []struct {
		name, version string
		args          []string
		want          string
	}{
		{"dev build", "dev", []string{"--artifact", "x=" + imageDigest}, "not a release version"},
		{"git describe", "v0.17.0-3-gabc1234", []string{"--artifact", "x=" + imageDigest}, "not a release version"},
		{"no artifact", "0.18.0", nil, "at least one --artifact"},
		{"short digest", "0.18.0", []string{"--artifact", "x=sha256:abc"}, "want ID=sha256"},
		{"missing id", "0.18.0", []string{"--artifact", "=" + imageDigest}, "want ID=sha256"},
		{"duplicate", "0.18.0", []string{"--artifact", "x=" + imageDigest, "--artifact", "x=" + archiveDigest}, "listed twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withVersion(t, tc.version)
			var out bytes.Buffer
			err := Run(append([]string{"release-index"}, tc.args...), strings.NewReader(""), &out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want %q", err, tc.want)
			}
			if out.Len() != 0 {
				t.Fatalf("printed a partial index: %s", out.String())
			}
		})
	}
}
