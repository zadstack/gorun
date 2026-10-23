package kernel

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"path"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/db"
)

// SchemaInspector compares the live schema_migrations table with the
// migrations this binary ships. Boot publishes one under ServiceSchema;
// System Health calls it per request, so it sees a newer binary migrating
// the database while this one runs.
type SchemaInspector func(context.Context) (db.SchemaInspection, error)

// knownMigrations maps every migration namespace this binary ships (kernel
// infrastructure plus every module, enabled or not) to its files.
func knownMigrations(mods []Module) map[string]fs.FS {
	known := map[string]fs.FS{}
	for _, m := range infraMigrations() {
		known[m.name] = m.files
	}
	for _, m := range mods {
		known[m.Name()] = m.Migrations()
	}
	return known
}

// KnownMigrationNames lists every migration this binary ships for mods plus
// the kernel infrastructure, as sorted "module/NNNN_name.sql" keys: the form
// schema_migrations records and a release index publishes, so an update
// preflight can refuse a target that does not know the live data.
func KnownMigrationNames(mods []Module) ([]string, error) {
	out := []string{}
	for module, files := range knownMigrations(mods) {
		if files == nil {
			continue
		}
		names, err := fs.Glob(files, "migrations/*.sql")
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			out = append(out, module+"/"+path.Base(n))
		}
	}
	slices.Sort(out)
	return out, nil
}

// InspectSchema reports applied migrations unknown to this binary (the
// schema is ahead of it) and applied files whose content changed. mods is
// the module list as given to Boot or MigrateAll. Rows of a BootOrder module
// missing from mods are not reported: a partial list (CLI commands that boot
// only settings) cannot judge them.
func InspectSchema(ctx context.Context, d *sql.DB, mods []Module) (db.SchemaInspection, error) {
	known := knownMigrations(mods)
	got, err := db.InspectMigrations(ctx, d, known)
	if err != nil {
		return got, err
	}
	unknown := make([]string, 0, len(got.Unknown))
	for _, key := range got.Unknown {
		module, _, _ := strings.Cut(key, "/")
		if _, listed := known[module]; !listed && slices.Contains(BootOrder, module) {
			continue
		}
		unknown = append(unknown, key)
	}
	got.Unknown = unknown
	return got, nil
}

// warnSchemaAhead logs at WARN when the database holds migrations this
// binary does not know. It never fails boot: an older binary keeps serving
// on an additive newer schema (see docs/ARCHITECTURE.md, "Migrations must
// be additive").
func warnSchemaAhead(ctx context.Context, log *slog.Logger, d *sql.DB, mods []Module) {
	got, err := InspectSchema(ctx, d, mods)
	if err != nil {
		log.Warn("migrate: could not compare schema_migrations with this binary", "err", err)
		return
	}
	if got.Ahead() {
		log.Warn("migrate: database has migrations unknown to this binary (schema is ahead: a newer binary ran, or a module was removed)",
			"count", len(got.Unknown), "migrations", strings.Join(got.Unknown, ", "))
	}
}

// warnChecksumMismatches logs one WARN per applied migration whose file in
// this binary no longer matches the checksum recorded when it was applied.
// Applied migrations are never re-run, so an edit to one silently diverges
// from what existing databases hold; the fix is a new migration file.
func warnChecksumMismatches(log *slog.Logger, reports []db.MigrateReport) {
	for _, r := range reports {
		for _, name := range r.Mismatched {
			log.Warn("migrate: applied migration changed since it ran (checksum mismatch); add a new migration instead of editing it",
				"module", r.Module, "name", name)
		}
	}
}
