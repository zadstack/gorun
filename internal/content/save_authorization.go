package content

import "database/sql"

// SaveAuthorization is an extension's transaction-scoped authorization input.
// Hooks must use Tx for database reads and must not commit or retain it.
// This hook supplements (never replaces) native ownership and permissions.
type SaveAuthorization struct {
	Tx   *sql.Tx
	Item *Item
}
