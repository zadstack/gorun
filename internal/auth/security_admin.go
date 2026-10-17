package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

type securityAttempt struct {
	ID, At, IP, Email, Reason, Source string
	Blockable                         bool
}
type securityBlock struct{ IP, ExpiresAt string }
type securityPage struct {
	Attempts []securityAttempt
	Blocks   []securityBlock
	Error    string
}

var errSecurityConflict = errors.New("security: changed or invalid request")

func (h *handlers) registerSecurity() {
	h.deps.Admin.AddNav(kernel.NavItem{ID: "auth.security", LabelKey: "auth.security.title", Icon: "shield", Href: "/admin/security/login-attempts", Permission: "security.manage", Group: "system", Position: 820})
	r := h.deps.Router.Admin.With(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFromContext(r.Context())
			if u == nil || u.Role != RoleAdmin || !Can(r.Context(), "security.manage") || ActorFromContext(r.Context()).Kind == "api_key" {
				http.Error(w, "Forbidden", 403)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Get("/security/login-attempts", h.securityShow)
	r.Post("/security/ip-block", h.securityBlock)
	r.Post("/security/ip-unblock", h.securityUnblock)
}
func (h *handlers) securityShow(w http.ResponseWriter, r *http.Request) {
	h.securityRender(w, r, 200, "")
}
func (h *handlers) securityRender(w http.ResponseWriter, r *http.Request, status int, problem string) {
	data := securityPage{Attempts: []securityAttempt{}, Blocks: []securityBlock{}, Error: problem}
	rows, e := h.deps.DB.QueryContext(r.Context(), `SELECT id,at,ip,meta_json FROM audit_log WHERE action='user.login_failed' ORDER BY id DESC LIMIT 50`)
	if e != nil {
		h.serverError(w, r, e)
		return
	}
	for rows.Next() {
		var x securityAttempt
		var raw string
		if e = rows.Scan(&x.ID, &x.At, &x.IP, &raw); e != nil {
			break
		}
		var meta map[string]string
		if json.Unmarshal([]byte(raw), &meta) != nil {
			continue
		}
		x.Email = attemptedIdentity(meta["email"])
		x.Reason = "auth.security.reason.other"
		switch meta["reason"] {
		case "invalid", "locked", "ip_locked", "overloaded", "staff_only":
			x.Reason = "auth.security.reason." + meta["reason"]
		}
		x.Source = meta["ip_source"]
		x.Blockable = (x.Source == "peer" || x.Source == "trusted_proxy") && blockableIP(r, x.IP)
		data.Attempts = append(data.Attempts, x)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		h.serverError(w, r, e)
		return
	}
	rows, e = h.deps.DB.QueryContext(r.Context(), `SELECT ip,expires_at FROM auth_ip_blocks WHERE expires_at>? ORDER BY expires_at LIMIT 512`, db.Now())
	if e != nil {
		h.serverError(w, r, e)
		return
	}
	for rows.Next() {
		var x securityBlock
		if e = rows.Scan(&x.IP, &x.ExpiresAt); e != nil {
			break
		}
		data.Blocks = append(data.Blocks, x)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		h.serverError(w, r, e)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.tpl.RenderWith(w, r, status, "admin", "pages/auth/security.html", data)
}
func (h *handlers) securityBlock(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		h.securityRender(w, r, 422, "auth.security.invalid")
		return
	}
	hours, e := strconv.Atoi(r.FormValue("hours"))
	if e != nil || (hours != 1 && hours != 24 && hours != 168) {
		h.securityRender(w, r, 422, "auth.security.invalid")
		return
	}
	id := r.FormValue("event_id")
	if len(id) > 32 {
		h.securityRender(w, r, 422, "auth.security.invalid")
		return
	}
	e = db.InTx(r.Context(), h.deps.DB, func(tx *sql.Tx) error {
		var ip, raw string
		if e := tx.QueryRowContext(r.Context(), `SELECT ip,meta_json FROM audit_log WHERE id=? AND action='user.login_failed'`, id).Scan(&ip, &raw); e != nil {
			return errSecurityConflict
		}
		var meta map[string]string
		if json.Unmarshal([]byte(raw), &meta) != nil || (meta["ip_source"] != "peer" && meta["ip_source"] != "trusted_proxy") || !blockableIP(r, ip) {
			return errSecurityConflict
		}
		var count int
		if e := tx.QueryRowContext(r.Context(), `SELECT count(*) FROM auth_ip_blocks WHERE expires_at>? AND ip!=?`, db.Now(), ip).Scan(&count); e != nil {
			return e
		}
		if count >= 512 {
			return errSecurityConflict
		}
		address, _ := publicBlockIP(ip)
		ip = address.String()
		expires := db.FormatTime(time.Now().Add(time.Duration(hours) * time.Hour))
		_, e := tx.ExecContext(r.Context(), `INSERT INTO auth_ip_blocks(ip,expires_at,actor_id,event_id,created_at) VALUES(?,?,?,?,?) ON CONFLICT(ip) DO UPDATE SET expires_at=excluded.expires_at,actor_id=excluded.actor_id,event_id=excluded.event_id,created_at=excluded.created_at`, ip, expires, UserFromContext(r.Context()).ID, id, db.Now())
		if e != nil {
			return e
		}
		h.deps.Audit.RecordTx(r.Context(), tx, "security.ip_block", "ip", ip, map[string]string{"expires_at": expires, "event_id": id})
		return nil
	})
	if e != nil {
		if errors.Is(e, errSecurityConflict) {
			h.securityRender(w, r, 409, "auth.security.invalid")
		} else {
			h.serverError(w, r, e)
		}
		return
	}
	http.Redirect(w, r, "/admin/security/login-attempts", 303)
}
func (h *handlers) securityUnblock(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		h.securityRender(w, r, 422, "auth.security.invalid")
		return
	}
	ip, ok := publicBlockIP(r.FormValue("ip"))
	if !ok || len(r.FormValue("expires_at")) > 40 {
		h.securityRender(w, r, 422, "auth.security.invalid")
		return
	}
	e := db.InTx(r.Context(), h.deps.DB, func(tx *sql.Tx) error {
		result, e := tx.ExecContext(r.Context(), `DELETE FROM auth_ip_blocks WHERE ip=? AND expires_at=?`, ip.String(), r.FormValue("expires_at"))
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return errSecurityConflict
		}
		h.deps.Audit.RecordTx(r.Context(), tx, "security.ip_unblock", "ip", ip.String(), nil)
		return nil
	})
	if e != nil {
		if errors.Is(e, errSecurityConflict) {
			h.securityRender(w, r, 409, "auth.security.invalid")
		} else {
			h.serverError(w, r, e)
		}
		return
	}
	http.Redirect(w, r, "/admin/security/login-attempts", 303)
}
