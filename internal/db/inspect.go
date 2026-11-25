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
	// Pending lists bundled migrations without an applied record.
	Pending []string `json:"-"`
	// LegacyMetadata indicates that the table or its checksum column is missing.
	LegacyMetadata bool `json:"-"`
}

// Ahead reports whether the database holds migrations this binary does not
// know.
func (s SchemaInspection) Ahead() bool { return len(s.Unknown) > 0 }

// InspectMigrations reads schema_migrations and compares it with known, the
// migration files of every module this binary ships (module name → FS as
// given to Migrate; a nil FS means the module has no migrations). Rows of a
// module missing from known are all unknown. Rows without a checksum (applied
// before checksums, or by an older binary) are never mismatched. Inspection
// does not create or upgrade the migration table.
func InspectMigrations(ctx context.Context, d *sql.DB, known map[string]fs.FS) (SchemaInspection, error) {
	out := SchemaInspection{Unknown: []string{}, Mismatched: []string{}}
	sums, err := knownChecksums(known)
	if err != nil {
		return out, err
	}
	var columns, checksums int
	err = d.QueryRowContext(ctx, `SELECT count(*), count(CASE WHEN name = 'checksum' THEN 1 END) FROM pragma_table_info('schema_migrations')`).Scan(&columns, &checksums)
	if err != nil {
		return out, fmt.Errorf("inspect migrations: %w", err)
	}
	out.LegacyMetadata = checksums == 0
	if columns == 0 {
		out.Pending = pendingMigrations(sums)
		return out, nil
	}
	query := `SELECT module, name, checksum FROM schema_migrations ORDER BY module, name`
	if out.LegacyMetadata {
		query = `SELECT module, name, NULL FROM schema_migrations ORDER BY module, name`
	}
	rows, err := d.QueryContext(ctx, query)
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
		delete(sums, key)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("inspect migrations: %w", err)
	}
	sort.Strings(out.Unknown)
	sort.Strings(out.Mismatched)
	out.Pending = pendingMigrations(sums)
	return out, nil
}

func pendingMigrations(sums map[string]string) []string {
	names := make([]string, 0, len(sums))
	for name := range sums {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
