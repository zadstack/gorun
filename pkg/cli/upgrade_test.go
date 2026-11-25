//go:build linux || darwin

package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestBinaryUpgrade(t *testing.T) {
	for _, tc := range []struct {
		name, target, explicit, wantErr                 string
		badSum, dry, held, yes, rollback                bool
		traversal, readOnly, binaryOnly, missingDefault bool
	}{
		{name: "upgrade and rollback", target: "v1.10.0", rollback: true},
		{name: "checksum mismatch", target: "v1.10.0", badSum: true, wantErr: "checksum mismatch"},
		{name: "implicit downgrade refused", target: "v1.1.0", wantErr: "refusing downgrade"},
		{name: "explicit downgrade", target: "v1.1.0", explicit: "v1.1.0"},
		{name: "dry run", target: "v1.10.0", dry: true},
		{name: "unsafe archive", target: "v1.10.0", traversal: true, wantErr: "unsafe release archive member"},
		{name: "read-only themes directory", target: "v1.10.0", readOnly: true, wantErr: releaseUpgradeDocs},
		{name: "binary only", target: "v1.10.0", readOnly: true, binaryOnly: true, rollback: true},
		{name: "install missing default", target: "v1.10.0", missingDefault: true, rollback: true},
		{name: "live server refused", target: "v1.10.0", held: true, wantErr: "systemctl stop gorun"},
		{name: "live server override", target: "v1.10.0", held: true, yes: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir, _ := seed(t)
			cfg, err := config.LoadReadOnly([]string{"--data", dataDir})
			if err != nil {
				t.Fatal(err)
			}
			if tc.dry {
				if err := os.Remove(filepath.Join(dataDir, kernel.DataLockFile)); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			}
			if tc.held {
				lock, err := kernel.LockDataDir(dataDir)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { lock.Release() })
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "gorun")
			old := versionScript("v1.2.0")
			if err := os.WriteFile(path, old, 0o755); err != nil {
				t.Fatal(err)
			}
			themesDir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("GORUN_THEMES", themesDir)
			for _, slug := range []string{"business", "default", "brand"} {
				if tc.missingDefault && slug == "default" {
					continue
				}
				writeTestTheme(t, themesDir, slug, "old")
			}
			if !tc.missingDefault {
				writeTestTheme(t, filepath.Join(themesDir, ".previous"), "default", "older")
			}
			if tc.readOnly {
				if err := os.Chmod(themesDir, 0o555); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(themesDir, 0o755)
			}
			platform := runtime.GOOS + "-" + runtime.GOARCH
			releases, requests := testBinaryReleases(t, tc.target, platform, tc.badSum, tc.traversal)
			var out bytes.Buffer
			u := binaryUpgrade{releases, path, "1.2.0", platform, &out}
			err = u.upgrade(context.Background(), cfg, tc.explicit, tc.yes, tc.dry, tc.binaryOnly)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("upgrade error = %v, want %q. Output: %s", err, tc.wantErr, &out)
			}
			changed := tc.wantErr == "" && !tc.dry
			files, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			backups, err := filepath.Glob(filepath.Join(dataDir, "backups", "*.manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			assertUpgradeFile(t, testThemeFile(themesDir, "brand"), []byte("old"))
			if tc.dry && (!strings.Contains(out.String(), "Bundled slugs to refresh: business, default") || !strings.Contains(out.String(), themesDir)) {
				t.Fatalf("missing dry-run theme plan: %s", &out)
			}
			if !changed || tc.binaryOnly {
				assertUpgradeFile(t, testThemeFile(themesDir, "default"), []byte("old"))
				assertUpgradeFile(t, testThemeFile(themesDir, "business"), []byte("old"))
				assertUpgradeFile(t, testThemeFile(filepath.Join(themesDir, ".previous"), "default"), []byte("older"))
			}
			if !changed {
				assertUpgradeFile(t, path, old)
				if len(files) != 1 || len(backups) != 0 {
					t.Fatalf("unexpected files %v or backups %v", files, backups)
				}
				if tc.dry {
					if _, err := os.Stat(filepath.Join(dataDir, kernel.DataLockFile)); !os.IsNotExist(err) {
						t.Fatalf("dry run created a data lock: %v", err)
					}
				}
				if tc.dry && (!strings.Contains(out.String(), "SHA-256") || !strings.Contains(out.String(), "Create and verify a backup") || !strings.Contains(out.String(), "Atomically replace")) {
					t.Fatalf("incomplete dry run: %s", &out)
				}
				if tc.wantErr == "refusing downgrade" && requests.Load() != 2 {
					t.Fatalf("expected only latest release resolution, got %d requests", requests.Load())
				}
				if tc.held && requests.Load() != 0 {
					t.Fatalf("locked upgrade made %d requests", requests.Load())
				}
				return
			}
			if tc.binaryOnly {
				if !strings.Contains(out.String(), "WARN: --binary-only skips themes in "+themesDir) ||
					!strings.Contains(out.String(), "Bundled slugs left at the old release: business, default") {
					t.Fatalf("missing binary-only warning: %s", &out)
				}
			} else {
				for _, slug := range []string{"business", "default"} {
					assertUpgradeFile(t, testThemeFile(themesDir, slug), []byte("new"))
					if !tc.missingDefault || slug != "default" {
						assertUpgradeFile(t, testThemeFile(filepath.Join(themesDir, ".previous"), slug), []byte("old"))
					}
				}
				if _, err := os.Stat(filepath.Join(themesDir, "portfolio")); !os.IsNotExist(err) {
					t.Fatalf("installed unselected portfolio theme: %v", err)
				}
			}
			assertUpgradeFile(t, path, versionScript(tc.target))
			assertUpgradeFile(t, path+".previous", old)
			if len(files) != 3 || len(backups) != 1 || !strings.Contains(out.String(), "systemctl start gorun") {
				t.Fatalf("files %v, backups %v, output %s", files, backups, &out)
			}
			raw, err := os.ReadFile(path + ".previous.json")
			if err != nil {
				t.Fatal(err)
			}
			var meta previousBinary
			if err := json.Unmarshal(raw, &meta); err != nil {
				t.Fatal(err)
			}
			if meta.Version != "v1.2.0" || meta.DataDir != dataDir || filepath.Base(backups[0]) != meta.BackupStamp+".manifest.json" || !strings.Contains(out.String(), "Backup stamp: "+meta.BackupStamp) {
				t.Fatalf("metadata %+v, backup %v, output %s", meta, backups, &out)
			}
			if !tc.binaryOnly && (len(meta.Themes) != 2 || meta.Themes[0].Slug != "business" ||
				meta.Themes[1] != (previousTheme{Slug: "default", Added: tc.missingDefault})) {
				t.Fatalf("unexpected theme metadata: %+v", meta)
			}
			if tc.rollback {
				lock, err := kernel.LockDataDir(dataDir)
				if err != nil {
					t.Fatal(err)
				}
				if err := u.rollback(); err == nil || !strings.Contains(err.Error(), "locked") {
					t.Errorf("rollback with running server: %v", err)
				}
				lock.Release()
				assertUpgradeFile(t, path, versionScript(tc.target))
				out.Reset()
				if err := u.rollback(); err != nil {
					t.Fatal(err)
				}
				assertUpgradeFile(t, path, old)
				assertUpgradeFile(t, testThemeFile(themesDir, "business"), []byte("old"))
				if tc.missingDefault {
					if _, err := os.Stat(filepath.Join(themesDir, "default")); !os.IsNotExist(err) {
						t.Fatalf("rollback should remove newly installed default: %v", err)
					}
				} else {
					assertUpgradeFile(t, testThemeFile(themesDir, "default"), []byte("old"))
				}
				if !strings.Contains(out.String(), "backup-restore --input "+quoteShell(backups[0])) || !strings.Contains(out.String(), "migrations stay applied") {
					t.Fatalf("rollback output: %s", &out)
				}
				dest := filepath.Join(filepath.Dir(dataDir), "gorun-restored-"+meta.BackupStamp)
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Fatalf("restore ran unexpectedly: %v", err)
				}
			}
		})
	}
}

func TestBinaryUpgradeContainerDetection(t *testing.T) {
	for _, tc := range []struct {
		name, file, cgroup, env string
		want                    bool
	}{
		{name: "native"},
		{name: "Docker", file: "/.dockerenv", want: true},
		{name: "Podman", file: "/run/.containerenv", want: true},
		{name: "Kubernetes", cgroup: "0::/kubepods.slice", want: true},
		{name: "Coolify", env: "COOLIFY_RESOURCE_UUID", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stat := func(path string) (os.FileInfo, error) {
				if path == tc.file {
					return nil, nil
				}
				return nil, os.ErrNotExist
			}
			read := func(path string) ([]byte, error) {
				if path == "/proc/self/cgroup" {
					return []byte(tc.cgroup), nil
				}
				return nil, os.ErrNotExist
			}
			getenv := func(key string) string {
				if key == tc.env {
					return "test"
				}
				return ""
			}
			container := containerInstall(stat, read, getenv)
			if container != tc.want {
				t.Fatalf("container detected = %t, want %t", container, tc.want)
			}
			err := checkBinaryInstall("linux", "amd64", container)
			if container && (err == nil || !strings.Contains(err.Error(), imageUpgradeDocs)) || !container && err != nil {
				t.Fatalf("binary installation check: %v", err)
			}
		})
	}
}

func TestBinaryUpgradePreservesPreviousExecutableWhenMetadataPathIsBlocked(t *testing.T) {
	dataDir, _ := seed(t)
	cfg, err := config.LoadReadOnly([]string{"--data", dataDir})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gorun")
	active, previous := versionScript("v1.2.0"), versionScript("v1.1.0")
	if err := os.WriteFile(path, active, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".previous", previous, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".previous.json", 0o700); err != nil {
		t.Fatal(err)
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	releases, requests := testBinaryReleases(t, "v1.3.0", platform, false, false)
	u := binaryUpgrade{releases, path, "1.2.0", platform, &bytes.Buffer{}}
	if err := u.upgrade(context.Background(), cfg, "v1.3.0", false, false, false); err == nil || !strings.Contains(err.Error(), "rollback destination") {
		t.Fatalf("expected blocked rollback destination, got %v", err)
	}
	assertUpgradeFile(t, path, active)
	assertUpgradeFile(t, path+".previous", previous)
	if requests.Load() != 0 {
		t.Fatal("upgrade fetched assets with an invalid rollback destination")
	}
}

func TestBinaryUpdateCommandsRefuseImageDeployment(t *testing.T) {
	t.Setenv("COOLIFY_RESOURCE_UUID", "test")
	for _, command := range []string{"upgrade", "rollback"} {
		err := Run([]string{command}, strings.NewReader(""), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), imageUpgradeDocs) {
			t.Errorf("%s in Coolify: %v", command, err)
		}
	}
}

func testBinaryReleases(t *testing.T, target, platform string, badSum, traversal bool) (binaryReleases, *atomic.Int32) {
	t.Helper()
	v := strings.TrimPrefix(target, "v")
	bundle := "gorun-" + v + "-" + platform
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tr := tar.NewWriter(gz)
	body := versionScript(target)
	if err := tr.WriteHeader(&tar.Header{Name: bundle + "/gorun", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Write(body); err != nil {
		t.Fatal(err)
	}
	members := []string{"default/templates/page.html", "business/templates/page.html", "portfolio/templates/page.html"}
	if traversal {
		members = append(members, "default/../../escape")
	}
	for _, member := range members {
		if err := tr.WriteHeader(&tar.Header{Name: bundle + "/themes/" + member, Mode: 0o644, Size: 3}); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Write([]byte("new")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive.Bytes())
	if badSum {
		sum[0] ^= 1
	}
	var requests atomic.Int32
	base := "/releases/download/" + target + "/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "/releases/tag/"+target, http.StatusFound)
		case "/releases/tag/" + target:
			fmt.Fprintln(w, "release")
		case base + bundle + ".tar.gz":
			w.Write(archive.Bytes())
		case base + "SHA256SUMS-" + v + ".txt":
			fmt.Fprintf(w, "%x  %s.tar.gz\n", sum, bundle)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return binaryReleases{srv.URL, srv.Client()}, &requests
}

func versionScript(tag string) []byte {
	return []byte("#!/bin/sh\nprintf 'GoRun " + tag + "\\n'\n")
}

func assertUpgradeFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s = %q, %v; want %q", path, got, err, want)
	}
}

func testThemeFile(dir, slug string) string {
	return filepath.Join(dir, slug, "templates", "page.html")
}

func writeTestTheme(t *testing.T, dir, slug, content string) {
	t.Helper()
	file := testThemeFile(dir, slug)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptedThemeSwapRestoresCurrentAndPreviousThemes(t *testing.T) {
	parent := t.TempDir()
	live, stage := filepath.Join(parent, "themes"), filepath.Join(parent, "stage")
	for _, slug := range []string{"business", "default"} {
		writeTestTheme(t, live, slug, "old")
		writeTestTheme(t, filepath.Join(live, ".previous"), slug, "older")
	}
	writeTestTheme(t, stage, "business", "new")
	root, err := os.OpenRoot(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	renames := themeRenames{root: root}
	plan := []previousTheme{{Slug: "business"}, {Slug: "default"}}
	if err := swapUpgradeThemes(&renames, "themes", "stage", plan); !os.IsNotExist(err) {
		t.Fatalf("expected missing staged default, got %v", err)
	}
	if err := renames.restore(); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"business", "default"} {
		assertUpgradeFile(t, testThemeFile(live, slug), []byte("old"))
		assertUpgradeFile(t, testThemeFile(filepath.Join(live, ".previous"), slug), []byte("older"))
	}
}

func TestThemeRollbackRejectsUnsafePaths(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeTestTheme(t, dir, "default", "new")
	outside := t.TempDir()
	writeTestTheme(t, outside, "default", "old")
	if err := os.Symlink(outside, filepath.Join(dir, ".previous")); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"../default", "default"} {
		if err := validateThemeRollback(dir, []previousTheme{{Slug: slug}}); err == nil {
			t.Fatalf("accepted unsafe rollback for %q", slug)
		}
	}
	assertUpgradeFile(t, testThemeFile(outside, "default"), []byte("old"))
}
