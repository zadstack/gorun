package admin

import (
	"context"
	"strings"
	"testing"
)

func TestArabicInstanceLabelUsesTheFormCharacterLimit(t *testing.T) {
	h := edgeTestHandlers(t)
	seen := []string{}
	client := instanceClient(t, &seen)
	label := strings.Repeat("س", 80)
	connected, _, err := h.connectEdgeInstance(context.Background(), client, edgeInstanceInput{Label: label, Origin: "https://a.example", Key: strings.Repeat("a", 43), Revision: instanceRevision(t, h)})
	if err != nil || connected.Label != label {
		t.Fatalf("80-character Arabic label rejected: %v", err)
	}
	before := len(seen)
	_, _, err = h.connectEdgeInstance(context.Background(), client, edgeInstanceInput{ID: connected.ID, Label: label + "س", Origin: connected.Origin, Revision: instanceRevision(t, h)})
	if err == nil || len(seen) != before {
		t.Fatal("overlong label must fail before remote access")
	}
}
