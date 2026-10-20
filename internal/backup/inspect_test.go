package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

func TestInspectDetectsMissingPairAndCorruption(t *testing.T) {
	for _, mode := range []string{"complete", "missing_archive", "corrupt_db", "wal", "manifest_escape"} {
		t.Run(mode, func(t *testing.T) {
			dir, path := backupSource(t)
			res, err := Create(dir, path, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing_archive":
				if err := os.Remove(res.Files); err != nil {
					t.Fatal(err)
				}
			case "corrupt_db":
				if err := os.WriteFile(res.DB, []byte("damaged"), 0600); err != nil {
					t.Fatal(err)
				}
			case "wal":
				if err := os.WriteFile(res.DB+"-wal", []byte("stale"), 0600); err != nil {
					t.Fatal(err)
				}
			case "manifest_escape":
				raw, err := os.ReadFile(res.Manifest)
				if err != nil {
					t.Fatal(err)
				}
				raw = []byte(strings.Replace(string(raw), res.Stamp+".db", "../gorun.db", 1))
				if err := os.WriteFile(res.Manifest, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			rep, err := Inspect(res.Manifest)
			if mode == "manifest_escape" {
				if err == nil {
					t.Fatal("unsafe manifest accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "complete" {
				if !rep.LocalComplete || !rep.IntegrityOK || rep.ForeignKeyViolations != 0 || rep.Readiness != "local_ok" || rep.Remote.State != "unverified" {
					t.Fatalf("wrong readiness: %+v", rep)
				}
				if len(rep.Warnings) != 0 {
					t.Fatalf("healthy local pair should not carry site-specific warnings: %+v", rep.Warnings)
				}
				if len(rep.CutoverChecklist) == 0 {
					t.Fatal("cutover checklist missing on healthy local pair")
				}
			} else if rep.Readiness != "blocked" || len(rep.Errors) == 0 {
				t.Fatalf("invalid backup accepted: %+v", rep)
			}
			if mode != "wal" {
				if _, err := os.Stat(res.DB + "-wal"); !os.IsNotExist(err) {
					t.Fatal("inspection created WAL")
				}
			}
		})
	}
}

func TestInspectForeignKeysAndMissingExternalDestinations(t *testing.T) {
	dir, path := backupSource(t)
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(parent_id INTEGER REFERENCES parent(id)); PRAGMA foreign_keys=OFF; INSERT INTO child VALUES (7); CREATE TABLE media(id TEXT, storage_key TEXT); INSERT INTO media VALUES ('m','remote/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/sites/fixture/a.png')`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	res, err := Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Inspect(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ForeignKeyViolations != 1 || rep.Readiness != "blocked" || len(rep.ExternalRequirements) != 1 || rep.ExternalRequirements[0].State != "destination_missing" {
		t.Fatalf("external/FK evidence omitted: %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(dir, "secret.key")); !os.IsNotExist(err) {
		t.Fatal("inspection generated recovery secret")
	}
}

func TestInspectExternalMediaKeepsLocalPairReviewRequired(t *testing.T) {
	dir, path := backupSource(t)
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TABLE media(id TEXT, storage_key TEXT); INSERT INTO media VALUES ('m','remote/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/sites/fixture/a.png')`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	res, err := Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Inspect(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.IntegrityOK || rep.Readiness != "review_required" || len(rep.Errors) != 0 {
		t.Fatalf("external media should stay review_required, not blocked: %+v", rep)
	}
	found := false
	for _, w := range rep.Warnings {
		if w == "external_media_required" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("external_media_required missing from warnings: %+v", rep.Warnings)
	}
	if len(rep.CutoverChecklist) == 0 {
		t.Fatal("cutover checklist should still list expected recovery requirements")
	}
}

func TestInspectRelativePathsWithURICharacters(t *testing.T) {
	parent := t.TempDir()
	dataDir := filepath.Join(parent, "site ? # %")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(dataDir, "gorun.db")
	d, err := db.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TABLE fixture(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	res, err := Create(dataDir, database, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(parent)
	relative, err := filepath.Rel(parent, res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Inspect(relative)
	if err != nil || report.Readiness == "blocked" || !report.IntegrityOK {
		t.Fatalf("relative manifest rejected: %+v %v", report, err)
	}
}
