package kernel

import (
	"context"
	"database/sql"
	"fmt"
)

// OptionalModule is evaluated before any module migrations, catalog loading or
// route registration. Disabled modules retain their existing data untouched.
// Enabled must only read the database; an empty database is valid.
type OptionalModule interface {
	Enabled(context.Context, *sql.DB) (bool, error)
}

func enabledModules(ctx context.Context, d *sql.DB, mods []Module) ([]Module, error) {
	selected := make([]Module, 0, len(mods))
	for _, m := range mods {
		if optional, ok := m.(OptionalModule); ok {
			enabled, err := optional.Enabled(ctx, d)
			if err != nil {
				return nil, fmt.Errorf("addon %s activation: %w", m.Name(), err)
			}
			if !enabled {
				continue
			}
		}
		selected = append(selected, m)
	}
	if err := validateModules(selected); err != nil {
		return nil, err
	}
	return selected, nil
}
