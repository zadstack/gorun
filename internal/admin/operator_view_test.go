package admin

import (
	"github.com/zadstack/gorun/internal/auth"
	"testing"
)

func TestOperatorRoleTaskComparisonUsesExactSavedPermissions(t *testing.T) {
	rows := roleTaskRows([]auth.Role{
		{Permissions: []string{"*"}},
		{Permissions: []string{"content.edit"}},
		{Permissions: []string{"content.*"}},
	})
	for _, row := range rows {
		if !row.Grants[0] {
			t.Fatalf("wildcard missing %s", row.LabelKey)
		}
		if row.Grants[2] {
			t.Fatalf("invented namespace wildcard for %s", row.LabelKey)
		}
		if row.Grants[1] != (row.LabelKey == "operator.task.edit") {
			t.Fatalf("unexpected editor permission %s", row.LabelKey)
		}
	}
}
