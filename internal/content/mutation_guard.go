package content

import (
	"context"
	"database/sql"
)

type unapprovedMutationKey struct{}

// GuardUnapprovedMutation restricts a caller to unchanged, non-live content at
// commit time. Only trusted server code can attach this context value; request
// JSON cannot authorize a live mutation. Normal API/editor saves are unaffected.
func GuardUnapprovedMutation(ctx context.Context) context.Context {
	return context.WithValue(ctx, unapprovedMutationKey{}, true)
}
func guardedMutation(ctx context.Context) bool {
	value, _ := ctx.Value(unapprovedMutationKey{}).(bool)
	return value
}

func (s *service) guardMutationTx(ctx context.Context, tx *sql.Tx, groups bool, snapshots ...*Item) error {
	if !guardedMutation(ctx) {
		return nil
	}
	for _, snapshot := range snapshots {
		if snapshot == nil {
			continue
		}
		current, err := s.getItem(ctx, tx, snapshot.ID)
		if err != nil {
			return err
		}
		if isLive(current.Status) || EditToken(current) != EditToken(snapshot) {
			return ErrConflict
		}
		if err := checkOwner(ctx, current); err != nil {
			return err
		}
		if !groups || current.TranslationGroup == "" {
			continue
		}
		// Group membership and live states are checked while holding the same write
		// transaction that changes the links, including members hidden to the caller.
		rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+` FROM content c WHERE c.translation_group=?`, current.TranslationGroup)
		if err != nil {
			return err
		}
		var failure error
		for rows.Next() {
			member, err := scanItem(rows)
			if err != nil {
				failure = err
				break
			}
			if isLive(member.Status) {
				failure = ErrConflict
				break
			}
			if err = checkOwner(ctx, member); err != nil {
				failure = err
				break
			}
		}
		if failure == nil {
			failure = rows.Err()
		}
		closeErr := rows.Close()
		if failure != nil {
			return failure
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
