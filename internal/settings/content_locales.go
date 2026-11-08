package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zadstack/gorun/internal/i18n"
)

// ContentLocalesTx reads the writer's current configuration without a render
// context or process memo. Only an absent setting uses legacy defaults;
// database failures and malformed configuration must fail a mutation closed.
func ContentLocalesTx(ctx context.Context, tx *sql.Tx) ([]string, error) {
	var tags []string
	err := GetTx(ctx, tx, i18n.ContentLocalesSetting, &tags)
	if errors.Is(err, ErrNotFound) {
		return i18n.ReadContentLocales(ctx, nil), nil
	}
	if err != nil {
		return nil, err
	}
	locales, err := i18n.ParseContentLocales(tags)
	if err != nil {
		return nil, fmt.Errorf("settings: invalid content languages: %w", err)
	}
	return locales, nil
}
