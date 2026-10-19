package theme

import "testing"

func TestThemeSettingsGroupsAndSafeResetScope(t *testing.T) {
	fields := []settingView{{Key: "header_style"}, {Key: "footer_style"}, {Key: idAccent}, {Key: idCustomCSS, Error: "invalid"}}
	groups := themeSettingsGroups(fields)
	if len(groups) != 4 || groups[0].Key != "header" || groups[1].Key != "footer" || groups[2].Key != "design" || groups[3].Key != "advanced" || !groups[3].HasError {
		t.Fatal(groups)
	}
	if !isIdentityResetKey(idAccent) || !isIdentityResetKey(idBody) || isIdentityResetKey("header_style") || isIdentityResetKey("unknown") {
		t.Fatal("reset scope escaped supported inheritance fields")
	}
}
