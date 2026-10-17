package wpmigrate

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/zadstack/gorun/internal/auth"
)

// importUser maps a WordPress author by email: an existing GoRun user is
// reused, otherwise an active "author" with a random password is created
// (they sign in with a magic link).
func (s *service) importUser(ctx context.Context, m *migrationRow, it *itemRow) error {
	var u WPUser
	if err := json.Unmarshal(it.Payload, &u); err != nil {
		return err
	}
	if u.Email == "" {
		it.Status, it.Error = ItemSkipped, "no_email"
		return nil
	}
	existing, err := s.users.GetByEmail(ctx, u.Email)
	switch {
	case err == nil:
		it.LocalKind, it.LocalID = "user", existing.ID
		it.Would = map[string]any{"action": "reuse", "email": u.Email}
		return nil
	case !errors.Is(err, auth.ErrNotFound):
		return err
	}
	it.Would = map[string]any{"action": "create", "email": u.Email, "role": roleAuthor}
	if m.DryRun {
		return nil
	}
	name := firstNonEmpty(u.Name, u.Slug, u.Email)
	created, err := s.users.Create(ctx, u.Email, name, randomPassword(), roleAuthor)
	if errors.Is(err, auth.ErrEmailTaken) {
		if created, err = s.users.GetByEmail(ctx, u.Email); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	it.LocalKind, it.LocalID = "user", created.ID
	s.logEntry(ctx, m.ID, "info", it.ID, "user_created", map[string]any{"email": u.Email, "note": "wpmigrate.log.magic_link"})
	return nil
}

// roleAuthor is the seeded GoRun role for imported WordPress authors.
const roleAuthor = "author"

func randomPassword() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
