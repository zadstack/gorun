package site

import "testing"

func TestBuilderFormValidatesErrors(t *testing.T) {
	def := FormDefinition{Slug: "booking", Source: formBuilder, Open: true, Fields: []FormField{
		{Name: "email", Type: fieldEmail, Required: true, State: statePinned},
		{Name: "count", Type: fieldNumber, State: statePinned},
		{Name: "day", Type: fieldDate, State: statePinned},
		{Name: "size", Type: fieldChoice, Choices: []string{"S", "M"}, State: statePinned},
		{Name: "old", Type: fieldText, State: stateDiscovered},
	}}
	_, errs := validateBuilder(def, map[string]string{"count": "x", "day": "2026-13-40", "size": "XL", "old": "v", "extra": "e"})
	for _, f := range []string{"email", "count", "day", "size"} {
		if errs[f] == "" {
			t.Errorf("no error for %s: %v", f, errs)
		}
	}
	if errs["email"] != "core.validation.required" {
		t.Errorf("email error = %q", errs["email"])
	}
	data, errs := validateBuilder(def, map[string]string{"email": "a@example.com", "count": "2.5", "day": "2026-11-03", "size": "M", "old": "v", "extra": "e"})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(data) != 4 || data["old"] != "" || data["extra"] != "" {
		t.Fatalf("data kept unknown or discovered fields: %v", data)
	}
}

func TestFieldOrder(t *testing.T) {
	got := fieldOrder("application/x-www-form-urlencoded", nil, []byte("b=1&a=2&b=3&c%5F=4"))
	if want := "b,a,c_"; join(got) != want {
		t.Fatalf("order = %v", got)
	}
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
