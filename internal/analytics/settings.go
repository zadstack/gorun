package analytics

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/settings"
)

const (
	// SettingBackupExclude leaves analytics.db out of backups (JSON bool,
	// default false). internal/backup's callers read it by this name.
	SettingBackupExclude = "analytics.backup_exclude"

	settingsPath = "/settings/analytics"
	permSettings = "settings.manage"

	// confirmWord is what the operator types to delete analytics data.
	confirmWord = "DELETE"

	// maxExclude bounds the excluded-prefix list.
	maxExclude = 200
)

type settingsData struct {
	Enabled, BackupExclude bool
	Exclude                string
	GeoFound               bool
	GeoPath                string
	Today                  string
	Confirm                string
	Events                 int
	EventsText             string
	First                  string // first event day, "" when empty
}

func (m *Module) boolSetting(ctx context.Context, key string, def bool) bool {
	var v bool
	switch err := m.settings.Get(ctx, key, &v); {
	case err == nil:
		return v
	case !errors.Is(err, settings.ErrNotFound):
		m.log.Error("analytics: read setting", "key", key, "err", err)
	}
	return def
}

// geoFound reports a loaded GeoIP database.
func (m *Module) geoFound() bool {
	_, ok := m.geo.(mmdbGeo)
	return ok
}

func (a *adminUI) settingsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m := a.m
	data := settingsData{
		Enabled:       m.boolSetting(ctx, SettingEnabled, true),
		BackupExclude: m.boolSetting(ctx, SettingBackupExclude, false),
		Exclude:       m.settings.GetString(ctx, SettingExclude, ""),
		GeoFound:      m.geoFound(),
		GeoPath:       filepath.Join(a.d.Config.DataDir, GeoFile),
		Today:         dayKey(m.store.clock()),
		Confirm:       confirmWord,
	}
	err := m.store.With(ctx, func(d *sql.DB) error {
		var first sql.NullString
		if err := d.QueryRowContext(ctx, `SELECT COUNT(*), MIN(ts) FROM events`).Scan(&data.Events, &first); err != nil {
			return err
		}
		if first.Valid && len(first.String) >= 10 {
			data.First = first.String[:10]
		}
		return nil
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	data.EventsText = formatInt(i18n.Locale(ctx), data.Events)
	a.tpl.Render(w, r, "pages/analytics/settings.html", data)
}

func (a *adminUI) back(w http.ResponseWriter, r *http.Request, kind, msg string) {
	httpx.SetFlash(w, kind, msg)
	http.Redirect(w, r, "/admin"+settingsPath, http.StatusSeeOther)
}

// cleanExclude trims each line and drops empty ones. Every prefix must be
// a path (start with one "/"); they are plain string prefixes.
func cleanExclude(raw string) (string, bool) {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r", "\n"), "\n") {
		p := strings.TrimSpace(line)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || len(p) > 512 || strings.ContainsAny(p, " \t\\") {
			return "", false
		}
		out = append(out, p)
	}
	if len(out) > maxExclude {
		return "", false
	}
	return strings.Join(out, "\n"), true
}

func (a *adminUI) saveSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	exclude, ok := cleanExclude(r.PostFormValue("exclude"))
	if !ok {
		a.back(w, r, "error", "analytics.settings.error_exclude")
		return
	}
	values := []struct {
		key string
		v   any
	}{
		{SettingEnabled, r.PostFormValue("enabled") == "1"},
		{SettingExclude, exclude},
		{SettingBackupExclude, r.PostFormValue("backup_exclude") == "1"},
	}
	for _, kv := range values {
		if err := a.d.Settings.Set(ctx, kv.key, kv.v); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	a.d.Audit.Record(ctx, "analytics.settings", "settings", ModuleName, map[string]any{
		"enabled": values[0].v, "exclude": strings.Count(exclude, "\n") + btoi(exclude != ""), "backup_exclude": values[2].v})
	a.back(w, r, "success", "analytics.settings.flash.saved")
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// deleteData deletes analytics data before a day (mode=before, date=
// YYYY-MM-DD, UTC) or all of it (mode=all), after the operator typed
// confirmWord. The queued events are flushed first, so nothing older
// reappears; the deletion is audited and the file VACUUMed.
func (a *adminUI) deleteData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if strings.TrimSpace(r.PostFormValue("confirm")) != confirmWord {
		a.back(w, r, "error", "analytics.settings.error_confirm")
		return
	}
	st := a.m.store
	mode := r.PostFormValue("mode")
	var before time.Time
	switch mode {
	case "before":
		d, err := time.Parse(time.DateOnly, r.PostFormValue("date"))
		if err != nil || d.After(startOfDay(st.clock())) {
			a.back(w, r, "error", "analytics.settings.error_date")
			return
		}
		before = d
	case "all":
	default:
		a.back(w, r, "error", "analytics.settings.error_date")
		return
	}
	if a.m.buf != nil {
		if err := a.m.buf.Flush(ctx); err != nil {
			a.d.Log.Warn("analytics: flush before delete", "err", err)
		}
	}
	var res deleted
	var err error
	if mode == "all" {
		res, err = st.DeleteAll(ctx)
	} else {
		res, err = st.DeleteBefore(ctx, before)
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	meta := map[string]any{"mode": mode, "events": res.Events, "daily": res.Daily}
	if mode == "before" {
		meta["before"] = dayKey(before)
	}
	a.d.Audit.Record(ctx, "analytics.delete", "analytics", mode, meta)
	a.back(w, r, "success", "analytics.settings.flash.deleted")
}

// deleted counts the rows a deletion removed.
type deleted struct{ Events, Daily int64 }

// DeleteBefore removes the events before day's UTC midnight and the daily
// rows of the days before it, then VACUUMs analytics.db.
func (s *Store) DeleteBefore(ctx context.Context, day time.Time) (deleted, error) {
	cut := startOfDay(day)
	return s.deleteRows(ctx,
		[]string{`DELETE FROM events WHERE ts < ?`, `DELETE FROM daily WHERE day < ?`},
		[][]any{{db.FormatTime(cut)}, {dayKey(cut)}})
}

// DeleteAll empties events and daily and drops every salt but today's
// (UTC), then VACUUMs analytics.db. Today's salt is kept so a visitor
// seen again today keeps the same hash.
func (s *Store) DeleteAll(ctx context.Context) (deleted, error) {
	return s.deleteRows(ctx,
		[]string{`DELETE FROM events`, `DELETE FROM daily`, `DELETE FROM salts WHERE day <> ?`},
		[][]any{nil, nil, {dayKey(s.clock())}})
}

func (s *Store) deleteRows(ctx context.Context, stmts []string, args [][]any) (deleted, error) {
	var out deleted
	err := s.With(ctx, func(d *sql.DB) error {
		err := db.InTx(ctx, d, func(tx *sql.Tx) error {
			for i, q := range stmts {
				res, err := tx.ExecContext(ctx, q, args[i]...)
				if err != nil {
					return err
				}
				n, _ := res.RowsAffected()
				switch i {
				case 0:
					out.Events = n
				case 1:
					out.Daily = n
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		_, err = d.ExecContext(ctx, `VACUUM`)
		return err
	})
	return out, err
}
