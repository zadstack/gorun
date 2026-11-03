package site

import (
	"context"

	"github.com/zadstack/gorun/internal/kernel"
)

// UpsertBuilderForms exposes upsertBuilderForms to the external tests.
func UpsertBuilderForms(d *kernel.Deps, pageID string, defs []FormDefinition) error {
	return newHandlers(d).upsertBuilderForms(context.Background(), pageID, defs)
}

// FormBySlug exposes formBySlug to the external tests.
func FormBySlug(d *kernel.Deps, slug string) (FormDefinition, error) {
	return newHandlers(d).formBySlug(context.Background(), slug)
}

// ErrNoForm is errNoForm for the external tests.
var ErrNoForm = errNoForm
