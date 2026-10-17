package admin_test

import (
	"context"
	"testing"
)

func TestConnectionHelpDoesNotConfigureOrTestProviders(t *testing.T) {
	s := boot(t)
	s.login("help@example.com", "admin", "en")
	_, body := s.do("GET", "/admin/settings/connections", nil)
	mustContain(t, body, "https://developers.openai.com/api/reference/overview", "https://platform.claude.com/docs/en/api/overview", "https://developers.cloudflare.com/r2/get-started/s3/", "https://docs.aws.amazon.com/AmazonS3/latest/userguide/VirtualHosting.html", "data-connection-setup", "Save the connection", "Use official endpoint")
	mustNotContain(t, body, `name="endpoint" value="https://api.openai.com/v1"`)
	rows, err := s.app.Deps.Settings.All(context.Background(), "connections.profile.")
	if err != nil || len(rows) != 0 {
		t.Fatalf("help wrote connection state: %d %v", len(rows), err)
	}
	if got := s.app.Deps.Settings.GetString(context.Background(), "ai.provider", ""); got != "" {
		t.Fatal("help activated provider")
	}
}
