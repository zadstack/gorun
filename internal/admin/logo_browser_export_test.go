package admin

import (
	"context"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"net/http"
)

// LogoBrowserTransport exists only in the Go test binary, never gorun serve.
func LogoBrowserTransport(client *http.Client) func() {
	previous := identityLogoClient
	identityLogoClient = func() *http.Client { return client }
	return func() { identityLogoClient = previous }
}

// LogoBrowserSeed creates a synthetic encrypted profile in a throwaway site.
func LogoBrowserSeed(ctx context.Context, d *kernel.Deps) (string, error) {
	h := &handlers{d: d}
	secret, err := h.encryptConnectionSecrets(connectionSecrets{Token: "synthetic-logo-browser-only"})
	if err != nil {
		return "", err
	}
	id := db.NewID()
	c := connectionRecord{ID: id, Version: db.NewID(), Name: "Local fake image adapter", Kind: "ai", Provider: "openai", Endpoint: "https://logo-browser.invalid/v1", Model: "image-fixture-v1", Secret: secret}
	return id, d.Settings.Set(ctx, connectionPrefix+id, c)
}
