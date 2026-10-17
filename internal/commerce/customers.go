package commerce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

// ensureCustomer finds or creates the user for email (new users get role
// customer and no password; they sign in with magic links) and its
// customers row. It writes through the pool: never call it inside InTx.
func (s *service) ensureCustomer(ctx context.Context, email string) (customerID string, u *auth.User, err error) {
	users, ok := s.users()
	if !ok {
		return "", nil, errors.New("commerce: auth service not registered")
	}
	u, err = users.GetByEmail(ctx, email)
	if errors.Is(err, auth.ErrNotFound) {
		u, err = users.Create(ctx, email, "", "", auth.RoleCustomer)
		if errors.Is(err, auth.ErrEmailTaken) { // lost a race with a concurrent checkout
			u, err = users.GetByEmail(ctx, email)
		}
	}
	if err != nil {
		var ve *auth.ValidationError
		if errors.As(err, &ve) {
			return "", nil, &ValidationError{Fields: map[string]string{"email": "commerce.error.email_invalid"}}
		}
		return "", nil, fmt.Errorf("commerce: customer user: %w", err)
	}
	id := db.NewID()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO customers (id, user_id, created_at) VALUES (?, ?, ?) ON CONFLICT (user_id) DO NOTHING`,
		id, u.ID, s.nowString()); err != nil {
		return "", nil, fmt.Errorf("commerce: create customer: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM customers WHERE user_id = ?`, u.ID).Scan(&customerID); err != nil {
		return "", nil, fmt.Errorf("commerce: load customer: %w", err)
	}
	return customerID, u, nil
}

// customerIDForUser returns the customers.id of userID, or ErrNotFound.
func (s *service) customerIDForUser(ctx context.Context, userID string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM customers WHERE user_id = ?`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("commerce: customer for user: %w", err)
	}
	return id, nil
}

// customerContact returns the user, email, locale and role used to reach a customer.
func (s *service) customerContact(ctx context.Context, customerID string) (userID, email, locale, role string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(u.id, ''), COALESCE(u.email, ''), COALESCE(u.locale, ''), COALESCE(u.role, '')
		FROM customers c LEFT JOIN users u ON u.id = c.user_id WHERE c.id = ?`, customerID).Scan(&userID, &email, &locale, &role)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return userID, email, locale, role, err
}
