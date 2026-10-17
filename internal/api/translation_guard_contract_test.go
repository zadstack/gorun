package api_test

import "testing"

func TestTranslationLinkAdvertisesOptionalExpectedMembers(t *testing.T) {
	doc := contractDocument(t)
	op := contractOperation(doc, "/v1/content/{id}/translations/link", "post")
	if _, ok := op["responses"].(map[string]any)["409"]; !ok {
		t.Fatal("atomic conflict response missing")
	}
	schema := requestSchema(doc, op)
	property, ok := schema["properties"].(map[string]any)["expected_ids"].(map[string]any)
	if !ok {
		t.Fatal("expected_ids capability missing")
	}
	typ := property["type"]
	if typ != "array" {
		t.Fatalf("expected array: %v", typ)
	}
	if property["items"].(map[string]any)["type"] != "string" {
		t.Fatal("IDs must be strings")
	}
	for _, key := range schema["required"].([]any) {
		if key == "expected_ids" {
			t.Fatal("guard must remain optional")
		}
	}
}
