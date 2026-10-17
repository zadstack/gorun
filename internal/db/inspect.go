package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"sort"
)

// SchemaInspection compares schema_migrations with the migration files a
// binary ships. Entries are "module/name", sorted.
type SchemaInspection struct {
	// Unknown are applied migrations this binary has no file for: a newer
	// binary migrated the database (the schema is ahead), or a module was
	// removed.
	Unknown []string `json:"unknown"`
	// Mismatched are applied migrations whose file in this binary differs
	// from the content recorded when they ran.
	Mismatched []string `json:"mismatched"`
}

// Ahead reports whether the database holds migrations this binary does not
// know.
func (s SchemaInspection) Ahead() bool { return len(s.Unknown) > 0 }

// InspectMigrations reads schema_migrations and compares it with known, the
// migration files of every module this binary ships (module name → FS as
// given to Migrate; a nil FS means the module has no migrations). Rows of a
// module missing from known are all unknown. Rows without a checksum (applied
// before checksums, or by an older binary) are never mismatched.
func InspectMigrations(ctx context.Context, d *sql.DB, known map[string]fs.FS) (SchemaInspection, error) {
	out := SchemaInspection{Unknown: []string{}, Mismatched: []string{}}
	sums, err := knownChecksums(known)
	if err != nil {
		return out, err
	}
	if err := ensureMigrationsTable(ctx, d); err != nil {
		return out, fmt.Errorf("inspect migrations: %w", err)
	}
	rows, err := d.QueryContext(ctx, `SELECT module, name, checksum FROM schema_migrations ORDER BY module, name`)
	if err != nil {
		return out, fmt.Errorf("inspect migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var module, name string
		var stored sql.NullString
		if err := rows.Scan(&module, &name, &stored); err != nil {
			return out, fmt.Errorf("inspect migrations: %w", err)
		}
		key := module + "/" + name
		sum, ok := sums[key]
		switch {
		case !ok:
			out.Unknown = append(out.Unknown, key)
		case stored.Valid && stored.String != sum:
			out.Mismatched = append(out.Mismatched, key)
		}
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("inspect migrations: %w", err)
	}
	sort.Strings(out.Unknown)
	sort.Strings(out.Mismatched)
	return out, nil
}

// knownChecksums maps "module/name" to the checksum of every known file.
func knownChecksums(known map[string]fs.FS) (map[string]string, error) {
	sums := map[string]string{}
	for module, files := range known {
		if files == nil {
			continue
		}
		names, err := fs.Glob(files, "migrations/*.sql")
		if err != nil {
			return nil, fmt.Errorf("inspect migrations: list %s: %w", module, err)
		}
		for _, p := range names {
			body, err := fs.ReadFile(files, p)
			if err != nil {
				return nil, fmt.Errorf("inspect migrations: read %s/%s: %w", module, path.Base(p), err)
			}
			sums[module+"/"+path.Base(p)] = Checksum(string(body))
		}
	}
	return sums, nil
}
