package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/zadstack/gorun/internal/kernel"
)

type navigationOrder struct{ Items, Groups []string }

func loadNavigationOrder(ctx context.Context, d *kernel.Deps) (navigationOrder, error) {
	var out navigationOrder
	u := UserFromContext(ctx)
	if u == nil {
		return out, nil
	}
	var items, groups string
	err := d.DB.QueryRowContext(ctx, `SELECT item_order_json,group_order_json FROM user_navigation_preferences WHERE user_id=?`, u.ID).Scan(&items, &groups)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(items), &out.Items); err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(groups), &out.Groups); err != nil {
		return out, err
	}
	return out, nil
}
func validateNavigationOrder(ids []string, offered map[string]bool) error {
	if len(ids) != len(offered) || len(ids) > maxNavigationItems {
		return errors.New("invalid navigation order length")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if len(id) > 256 || !offered[id] || seen[id] {
			return errors.New("invalid navigation order item")
		}
		seen[id] = true
	}
	return nil
}
func mergeNavigationOrder(submitted, prior []string, allowed map[string]bool) []string {
	out := slices.Clone(submitted)
	seen := map[string]bool{}
	for _, id := range out {
		seen[id] = true
	}
	for _, id := range prior {
		if allowed[id] && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}
func orderedNavigation(ctx context.Context, d *kernel.Deps) []kernel.NavItem {
	items := d.Admin.Nav(ctx)
	order, err := loadNavigationOrder(ctx, d)
	if err != nil {
		d.Log.Error("auth: load sidebar order", "err", err)
		return items
	}
	rank := map[string]int{}
	for i, id := range order.Items {
		rank[id] = i + 1
	}
	slices.SortStableFunc(items, func(a, b kernel.NavItem) int {
		ra, rb := rank[a.ID], rank[b.ID]
		if ra == 0 && rb == 0 {
			return 0
		}
		if ra == 0 {
			return 1
		}
		if rb == 0 {
			return -1
		}
		return ra - rb
	})
	return items
}
