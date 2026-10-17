package saas

import (
	"context"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
)

func TestHealthChecksIgnoreForeignValuesAndSurfaceQueryErrors(t *testing.T) {
	s, _, _ := setup(t)
	in := "not a check list"
	out, e := s.healthChecks(context.Background(), in)
	if e != nil || out != in {
		t.Fatal("foreign filter value must pass through unchanged", out, e)
	}
	var nilChecks *[]admin.HealthCheck
	if out, e = s.healthChecks(context.Background(), nilChecks); e != nil || out != any(nilChecks) {
		t.Fatal(out, e)
	}
	if _, e = s.DB.Exec(`ALTER TABLE saas_key_versions RENAME COLUMN protocol TO proto`); e != nil {
		t.Fatal(e)
	}
	checks := []admin.HealthCheck{}
	if _, e = s.healthChecks(context.Background(), &checks); e == nil || len(checks) != 0 {
		t.Fatal("query failure must be reported, not hidden", checks, e)
	}
}
