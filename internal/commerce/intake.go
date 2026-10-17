package commerce

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var intakeKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

func validIntakeSchema(fields []IntakeField) bool {
	if len(fields) > 12 {
		return false
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if !intakeKey.MatchString(f.Key) || seen[f.Key] || strings.TrimSpace(f.Label) == "" || utf8.RuneCountInString(f.Label) > 120 || f.MaxLength < 1 || f.MaxLength > 4000 {
			return false
		}
		seen[f.Key] = true
	}
	return true
}

// Intake is scoped to a cart product and frozen into its order line. Unknown
// fields are rejected, so arbitrary customer metadata is never silently stored.
func validateIntake(cart *Cart, values map[string]map[string]string) error {
	products := map[string]bool{}
	for _, line := range cart.Lines {
		products[line.Product.ID] = true
		allowed := map[string]bool{}
		for _, field := range line.Product.Intake {
			allowed[field.Key] = true
			value := values[line.Product.ID][field.Key]
			if field.Required && strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > field.MaxLength {
				return cartError("intake." + field.Key)
			}
		}
		for key := range values[line.Product.ID] {
			if !allowed[key] {
				return cartError("intake")
			}
		}
	}
	for id := range values {
		if !products[id] {
			return cartError("intake")
		}
	}
	return nil
}

func intakeFromForm(form url.Values) map[string]map[string]string {
	out := map[string]map[string]string{}
	for key, values := range form {
		if !strings.HasPrefix(key, "intake.") {
			continue
		}
		parts := strings.Split(key, ".")
		if len(parts) != 3 || len(values) != 1 {
			out["invalid"] = map[string]string{}
			continue
		}
		if out[parts[1]] == nil {
			out[parts[1]] = map[string]string{}
		}
		out[parts[1]][parts[2]] = values[0]
	}
	return out
}
