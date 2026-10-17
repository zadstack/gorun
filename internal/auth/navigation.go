package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

const maxNavigationItems = 512

func storedHiddenNavigation(ctx context.Context, d *kernel.Deps) (map[string]bool, error) {
	hidden := map[string]bool{}
	u := UserFromContext(ctx)
	if u == nil {
		return hidden, nil
	}
	var raw string
	err := d.DB.QueryRowContext(ctx, `SELECT hidden_json FROM user_navigation_preferences WHERE user_id=?`, u.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return hidden, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	if err = json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, fmt.Errorf("navigation preferences: %w", err)
	}
	for _, id := range ids {
		hidden[id] = true
	}
	return hidden, nil
}

func pinnedNavigation(ctx context.Context, d *kernel.Deps) (map[string]bool, error) {
	out := map[string]bool{}
	if UserFromContext(ctx) == nil {
		return out, nil
	}
	source, ok := kernel.Get[kernel.NavigationPins](d, kernel.ServiceNavigationPins)
	if !ok {
		return out, nil
	}
	ids, e := source.PinnedNavigation(ctx)
	if e != nil {
		return nil, e
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
func hiddenNavigation(ctx context.Context, d *kernel.Deps) (map[string]bool, error) {
	hidden, e := storedHiddenNavigation(ctx, d)
	if e != nil {
		return nil, e
	}
	pins, e := pinnedNavigation(ctx, d)
	if e != nil {
		return nil, e
	}
	for id := range pins {
		hidden[id] = true
	}
	return hidden, nil
}

func (h *handlers) navigation(w http.ResponseWriter, r *http.Request) {
	hidden, err := hiddenNavigation(r.Context(), h.deps)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	pinned, err := pinnedNavigation(r.Context(), h.deps)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "admin", "pages/auth/navigation.html", struct {
		Hidden map[string]bool
		Pinned map[string]bool
		Saved  bool
	}{hidden, pinned, r.URL.Query().Get("saved") == "1"})
}

func navigationSet(ids []string, allowed map[string]bool) (map[string]bool, error) {
	if len(ids) > maxNavigationItems {
		return nil, errors.New("too many navigation items")
	}
	out := map[string]bool{}
	for _, id := range ids {
		if len(id) == 0 || len(id) > 256 || !allowed[id] || out[id] {
			return nil, errors.New("invalid navigation item")
		}
		out[id] = true
	}
	return out, nil
}

func (h *handlers) navigationSave(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	if u == nil {
		h.tpl.RenderError(w, r, http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	intent := r.PostForm.Get("intent")
	if intent == "reset" {
		if _, err := h.deps.DB.ExecContext(r.Context(), `DELETE FROM user_navigation_preferences WHERE user_id=?`, u.ID); err != nil {
			h.serverError(w, r, err)
			return
		}
	} else if intent == "save" {
		allowed := map[string]bool{}
		for _, item := range h.deps.Admin.Nav(r.Context()) {
			allowed[item.ID] = true
		}
		available, err := navigationSet(r.PostForm["available"], allowed)
		if err != nil {
			h.tpl.RenderError(w, r, http.StatusBadRequest)
			return
		}
		visible, err := navigationSet(r.PostForm["visible"], available)
		if err != nil {
			h.tpl.RenderError(w, r, http.StatusBadRequest)
			return
		}
		hidden, err := storedHiddenNavigation(r.Context(), h.deps)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		// Only the offered snapshot changes: module items added after the form
		// was opened retain their default visibility.
		pinned, err := pinnedNavigation(r.Context(), h.deps)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		for id := range available {
			if pinned[id] {
				continue
			}
			if visible[id] {
				delete(hidden, id)
			} else {
				hidden[id] = true
			}
		}
		order, err := loadNavigationOrder(r.Context(), h.deps)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		if r.PostForm.Get("order_present") != "" {
			if r.PostForm.Get("order_present") != "1" {
				h.tpl.RenderError(w, r, 400)
				return
			}
			offeredGroups, allowedGroups := map[string]bool{}, map[string]bool{}
			for _, item := range h.deps.Admin.Nav(r.Context()) {
				allowedGroups[item.Group] = true
				if available[item.ID] {
					offeredGroups[item.Group] = true
				}
			}
			if validateNavigationOrder(r.PostForm["item_order"], available) != nil || validateNavigationOrder(r.PostForm["group_order"], offeredGroups) != nil {
				h.tpl.RenderError(w, r, 400)
				return
			}
			order.Items = mergeNavigationOrder(r.PostForm["item_order"], order.Items, allowed)
			order.Groups = mergeNavigationOrder(r.PostForm["group_order"], order.Groups, allowedGroups)
		}
		itemJSON, err := json.Marshal(order.Items)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		groupJSON, err := json.Marshal(order.Groups)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		ids := make([]string, 0, len(hidden))
		for id := range hidden {
			if allowed[id] {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		raw, err := json.Marshal(ids)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		_, err = h.deps.DB.ExecContext(r.Context(), `INSERT INTO user_navigation_preferences(user_id,hidden_json,updated_at,item_order_json,group_order_json) VALUES(?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET hidden_json=excluded.hidden_json,updated_at=excluded.updated_at,item_order_json=excluded.item_order_json,group_order_json=excluded.group_order_json`, u.ID, string(raw), db.Now(), string(itemJSON), string(groupJSON))
		if err != nil {
			h.serverError(w, r, err)
			return
		}
	} else {
		h.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	h.deps.Audit.Record(r.Context(), "user.navigation_"+intent, "user", u.ID, nil)
	http.Redirect(w, r, "/admin/navigation?saved=1", http.StatusSeeOther)
}
