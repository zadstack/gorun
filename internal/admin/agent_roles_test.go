package admin

import "testing"

func TestAgentRoleExamplesStayWithinBundleBoundsWithoutBindings(t *testing.T) {
	for _, locale := range []string{"en", "ar"} {
		roles := agentRoles(locale)
		if len(roles) != 6 {
			t.Fatal(len(roles))
		}
		ids := map[string]bool{}
		for _, role := range roles {
			if role.ID == "" || ids[role.ID] || role.Name == "" || role.Goal == "" || role.Example == "" || role.Setup == "" || len(role.Skills) != 5 {
				t.Fatal(role.ID)
			}
			ids[role.ID] = true
			if e := validateAgentBundle(agentBundle{Skills: role.Skills}); e != nil {
				t.Fatal(locale, role.ID, e)
			}
		}
	}
}
