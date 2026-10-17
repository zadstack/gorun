package content

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/zadstack/gorun/internal/auth"
)

func termsEditToken(terms map[string][]string) string {
	normalized := make(map[string][]string, len(terms))
	for key, values := range terms {
		normalized[key] = slices.Clone(values)
		slices.Sort(normalized[key])
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// The normal editor may intentionally change terms; its separate terms token
// protects assignments while preserving AtomicUpdater's unchanged-terms contract.
func (s *service) updateEditorAtomic(ctx context.Context, id string, in Input, expected, expectedTerms string) (*Item, error) {
	if auth.UserFromContext(ctx) != nil && !auth.Can(ctx, "content.edit") {
		return nil, ErrForbidden
	}
	existing, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = checkOwner(ctx, existing); err != nil {
		return nil, err
	}
	if expected == "" || expectedTerms == "" || EditToken(existing) != expected || termsEditToken(existing.Terms) != expectedTerms {
		return nil, ErrConflict
	}
	keep := s.revisionsKeep(ctx)
	return s.save(ctx, existing, in, saveOpts{inTx: func(tx *sql.Tx) error {
		current, err := s.getItem(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = checkOwner(ctx, current); err != nil {
			return err
		}
		if EditToken(current) != expected {
			return ErrConflict
		}
		current.Terms, err = s.readTermsTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if termsEditToken(current.Terms) != expectedTerms {
			return ErrConflict
		}
		return s.addRevision(ctx, tx, current, keep)
	}})
}
