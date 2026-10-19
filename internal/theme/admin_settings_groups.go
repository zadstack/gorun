package theme

import "strings"

type themeSettingsGroup struct {
	Key      string
	Fields   []settingView
	HasError bool
}

func isIdentityResetKey(key string) bool {
	switch key {
	case idAccent, idBackground, idText, idHeading, idBody, idScheme, idRadius, idCustomCSS:
		return true
	}
	return false
}
func themeSettingsGroups(fields []settingView) []themeSettingsGroup {
	groups := []themeSettingsGroup{{Key: "header"}, {Key: "footer"}, {Key: "design"}, {Key: "advanced"}}
	for _, field := range fields {
		index := 3
		switch {
		case strings.HasPrefix(field.Key, "header_") || strings.HasPrefix(field.Key, "cta_"):
			index = 0
		case strings.HasPrefix(field.Key, "footer_"):
			index = 1
		case strings.HasPrefix(field.Key, "identity_"):
			index = 2
		}
		groups[index].Fields = append(groups[index].Fields, field)
		groups[index].HasError = groups[index].HasError || field.Error != ""
	}
	out := []themeSettingsGroup{}
	for _, group := range groups {
		if len(group.Fields) > 0 {
			out = append(out, group)
		}
	}
	return out
}
