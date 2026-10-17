package content

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/zadstack/gorun/internal/auth"
)

var ErrConflict = errors.New("content: changed since it was loaded")

// AtomicUpdater joins a module's dependent write to a validated content save.
// Taxonomy assignments must remain unchanged; regular Update handles taxonomy edits.
// The callback must use tx rather than the database pool.
type AtomicUpdater interface {
	UpdateAtomic(context.Context, string, Input, string, func(*sql.Tx, *Item) error) (*Item, error)
}

func (s *service) UpdateAtomic(ctx context.Context, id string, in Input, expectedUpdatedAt string, joined func(*sql.Tx, *Item) error) (*Item, error) {
	if auth.UserFromContext(ctx) != nil && !auth.Can(ctx, "content.edit") {
		return nil, ErrForbidden
	}
	existing, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkOwner(ctx, existing); err != nil {
		return nil, err
	}
	if expectedUpdatedAt == "" || EditToken(existing) != expectedUpdatedAt {
		return nil, ErrConflict
	}
	if !reflect.DeepEqual(in.Terms, existing.Terms) {
		return nil, ErrConflict
	}
	keep := s.revisionsKeep(ctx)
	return s.save(ctx, existing, in, saveOpts{
		inTx: func(tx *sql.Tx) error {
			current, err := s.getItem(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := checkOwner(ctx, current); err != nil {
				return err
			}
			if EditToken(current) != expectedUpdatedAt {
				return ErrConflict
			}
			current.Terms, err = s.readTermsTx(ctx, tx, id)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(current.Terms, existing.Terms) {
				return ErrConflict
			}
			return s.addRevision(ctx, tx, current, keep)
		},
		afterWrite: joined,
	})
}

// EditToken is an opaque optimistic version of persisted content. Unlike
// second-resolution timestamps, it detects two edits within the same second.
func EditToken(it *Item) string {
	snapshot := *it
	snapshot.Terms = nil
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// EditTokenInTx reads the same persisted snapshot while the caller holds a
// write transaction, so a first draft save cannot accept a stale content base.
func EditTokenInTx(ctx context.Context, tx *sql.Tx, id string) (string, error) {
	it, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM content c WHERE c.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return EditToken(it), nil
}

func (s *service) readTermsTx(ctx context.Context, tx *sql.Tx, id string) (map[string][]string, error) {
	backend := s.terms()
	if backend == nil {
		return map[string][]string{}, nil
	}
	reader, ok := backend.(interface {
		ContentTermsTx(context.Context, *sql.Tx, string) (map[string][]string, error)
	})
	if !ok {
		return nil, errors.New("content: atomic save requires transaction-safe taxonomy reads")
	}
	return reader.ContentTermsTx(ctx, tx, id)
}
