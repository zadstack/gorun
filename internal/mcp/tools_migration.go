package mcp

import (
	"context"
	"reflect"

	"github.com/zadstack/gorun/internal/kernel"
)

type migrationIDInput struct {
	MigrationID string `json:"migration_id"`
}

var (
	ctxType = reflect.TypeFor[context.Context]()
	errType = reflect.TypeFor[error]()
)

// bindMethod binds v's method name when its signature is
// func(context.Context, A) (R, error). Optional services (wpmigrate,
// commerce) are consumed this way so mcp needs no import of packages that
// boot before it but may be absent from a build, and any result type works.
func bindMethod[A any](v any, name string) (func(context.Context, A) (any, error), bool) {
	if v == nil {
		return nil, false
	}
	m := reflect.ValueOf(v).MethodByName(name)
	if !m.IsValid() {
		return nil, false
	}
	t := m.Type()
	if t.NumIn() != 2 || t.In(0) != ctxType || t.In(1) != reflect.TypeFor[A]() || t.NumOut() != 2 || t.Out(1) != errType {
		return nil, false
	}
	return func(ctx context.Context, a A) (any, error) {
		out := m.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(a)})
		if err, _ := out[1].Interface().(error); err != nil {
			return nil, err
		}
		return out[0].Interface(), nil
	}, true
}

// migrationTools exposes the WordPress migrator (service "wpmigrate":
// Preview(ctx, migrationID) and Status(ctx, migrationID)) when booted.
func migrationTools(d *kernel.Deps, s *server) []kernel.MCPTool {
	svc, ok := kernel.Get[any](d, serviceWPMigrate)
	if !ok {
		return nil
	}
	var out []kernel.MCPTool
	if preview, ok := bindMethod[string](svc, "Preview"); ok {
		out = append(out, kernel.MCPTool{Name: "preview_migration", Aliases: []string{"run_migration_preview"}, Title: "Preview WordPress migration", Toolset: kernel.MCPToolsetMigration, Idempotent: true, OpenWorld: true, Scopes: []string{scopeMigrate}, Input: migrationIDInput{},
			Description: "Run the dry-run preview of a WordPress migration and return its report. It reads the WordPress source and records the preview plan; no content is created or changed.",
			Handler:     bind(func(ctx context.Context, in migrationIDInput) (any, error) { return preview(ctx, in.MigrationID) })})
	} else {
		s.log.Warn("mcp: wpmigrate service has no Preview(ctx, string) (T, error); preview_migration disabled")
	}
	if status, ok := bindMethod[string](svc, "Status"); ok {
		out = append(out, kernel.MCPTool{Name: "get_migration_status", Title: "Get migration status", Toolset: kernel.MCPToolsetMigration, ReadOnly: true, Scopes: []string{scopeMigrate}, Input: migrationIDInput{},
			Description: "Status and progress counters of a WordPress migration.",
			Handler:     bind(func(ctx context.Context, in migrationIDInput) (any, error) { return status(ctx, in.MigrationID) })})
	} else {
		s.log.Warn("mcp: wpmigrate service has no Status(ctx, string) (T, error); get_migration_status disabled")
	}
	return out
}
