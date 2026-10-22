package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/diagnostics"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/observe"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/update"
	"github.com/zadstack/gorun/pkg/themekit"
)

type siteDoctorCheck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Action  string `json:"action,omitempty"`
}
type siteDoctorReport struct {
	OK     bool              `json:"ok"`
	Checks []siteDoctorCheck `json:"checks"`
}

func (r *siteDoctorReport) add(id, status, message, action string) {
	r.Checks = append(r.Checks, siteDoctorCheck{id, status, message, action})
	if status == "fail" {
		r.OK = false
	}
}

// doctorCmd deliberately never boots the kernel, migrates, generates a secret,
// takes the worker lock or opens the database in write mode.
func doctorCmd(args []string, out io.Writer) error {
	asJSON, args := boolFlag(args, "json")
	report := siteDoctorReport{OK: true, Checks: []siteDoctorCheck{}}
	cfg, err := config.LoadReadOnly(args)
	if err != nil {
		// Config errors can contain URLs or user-supplied credentials. Do not echo them.
		report.add("config", "fail", "Configuration is missing or invalid.", "For a new local site run gorun start. Otherwise review flags, environment, gorun.toml and the existing secret; doctor never generates or replaces it.")
	} else {
		report.add("config", "ok", "Configuration and existing secret are valid.", "")
		doctorSite(cfg, &report)
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		for _, check := range report.Checks {
			if _, err := fmt.Fprintf(out, "[%s] %s: %s\n", check.Status, check.ID, check.Message); err != nil {
				return err
			}
			if check.Action != "" {
				if _, err := fmt.Fprintf(out, "  Next: %s\n", check.Action); err != nil {
					return err
				}
			}
		}
	}
	if !report.OK {
		return &exitError{code: 1, msg: "doctor found problems; follow the suggested actions above"}
	}
	return nil
}

func doctorSite(cfg config.Config, report *siteDoctorReport) {
	st, err := os.Stat(cfg.DataDir)
	if err != nil || !st.IsDir() {
		report.add("data", "fail", "Data directory is missing or inaccessible.", "Check --data and directory permissions; use gorun start for a new local site.")
		return
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0200 == 0 {
		report.add("permissions", "fail", "Data directory has no owner write permission.", "Grant the server account write access to the data directory.")
	} else {
		report.add("permissions", "ok", "Data directory exists and its permission bits allow owner writes.", "")
	}
	if st, err := os.Stat(filepath.Join(cfg.DataDir, "secret.key")); err == nil && runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
		report.add("secret_permissions", "warn", "The local secret file is readable by other accounts.", "Restrict secret.key to its owner (chmod 600).")
	}
	doctorPort(cfg, report)
	if st, err := os.Stat(cfg.DBPath()); err != nil || !st.Mode().IsRegular() {
		report.add("database", "fail", "SQLite database is missing or not a regular file.", "Check --data; run gorun start to initialize a new local site.")
		return
	}
	if st, err := os.Stat(cfg.DBPath()); err == nil && runtime.GOOS != "windows" && st.Mode().Perm()&0600 != 0600 {
		report.add("database_permissions", "fail", "SQLite permission bits do not allow owner reads and writes.", "Grant the server account read/write access to the existing database; do not replace it.")
	}
	uri, err := db.FileURI(cfg.DBPath())
	if err != nil {
		report.add("database", "fail", "Cannot resolve SQLite path.", "Check the data directory path.")
		return
	}
	d, err := sql.Open("sqlite", uri+"?mode=ro&_pragma=query_only(ON)&_pragma=busy_timeout(1000)")
	if err != nil {
		report.add("database", "fail", "Cannot open SQLite in read-only mode.", "Check file permissions and SQLite availability.")
		return
	}
	defer d.Close()
	d.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var integrity string
	if err := d.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&integrity); err != nil || integrity != "ok" {
		report.add("database", "fail", "SQLite could not pass its read-only integrity check.", "Check permissions and locks; if corruption persists, stop the server and restore a verified backup.")
		return
	}
	report.add("database", "ok", "SQLite quick_check passed (read-only).", "")
	doctorSchema(ctx, d, Modules(cfg), report)
	var admins int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND status = 'active'`).Scan(&admins); err != nil {
		report.add("admin", "fail", "Cannot inspect administrator accounts.", "Review the schema check and apply required migrations with a matching binary.")
	} else if admins == 0 {
		report.add("admin", "fail", "No active administrator account exists.", "Use gorun start for an empty local site, or gorun user create --admin for an existing site.")
	} else {
		report.add("admin", "ok", "An active administrator account exists.", "")
	}
	var pending int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE onboarding_required = 1 AND role = 'admin' AND status = 'active'`).Scan(&pending); err == nil && pending > 0 {
		report.add("onboarding", "warn", "First-login setup is unfinished.", "Sign in at the localhost admin URL and replace the temporary password.")
	}
	slug := cfg.Theme
	var raw string
	if err := d.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = 'site.theme'`).Scan(&raw); err == nil {
		var active string
		if json.Unmarshal([]byte(raw), &active) == nil && active != "" {
			slug = active
		}
	}
	roots := append([]string{theme.ThemesDir(cfg)}, theme.ExtraThemesDirs()...)
	dir, err := findThemeDirForDoctor(roots, slug)
	if err != nil {
		report.add("theme", "fail", "The active theme cannot be found.", "Restore its files or correct GORUN_THEMES; use gorun theme doctor for details.")
		return
	}
	result := themekit.DoctorTheme(dir, roots)
	if !result.OK {
		report.add("theme", "fail", "The active theme failed its filesystem contract checks.", "Run gorun theme doctor for the detailed theme report.")
	} else {
		report.add("theme", "ok", "Active theme files and parent contract passed.", "")
	}
}

func doctorPort(cfg config.Config, report *siteDoctorReport) {
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		report.add("port", "fail", "Invalid listen address.", "Set --addr to a local host:port or :port.")
		return
	}
	if host != "" && host != "localhost" {
		ip := net.ParseIP(host)
		local := ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
		addrs, _ := net.InterfaceAddrs()
		for _, a := range addrs {
			candidate, _, _ := net.ParseCIDR(a.String())
			local = local || ip != nil && ip.Equal(candidate)
		}
		if !local {
			report.add("port", "fail", "Listen address is not an address on this computer.", "Use a local interface address, localhost or :port.")
			return
		}
	}
	listener, err := net.Listen("tcp", cfg.Addr)
	if err == nil {
		listener.Close()
		report.add("port", "ok", "Configured port is available.", "")
		return
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err == nil {
		response.Body.Close()
		if response.StatusCode == 200 {
			report.add("port", "warn", "Port is occupied; a local HTTP health endpoint responds.", "If this is your running site, no action is needed. Otherwise choose another --addr port.")
			return
		}
	}
	report.add("port", "fail", "Configured port is occupied or cannot be bound.", "Stop the conflicting process, check interface permissions, or choose another --addr port.")
}

// Read the recorded migrations directly: the existing schema inspector may
// initialize legacy metadata, which a diagnostic must not do.
func doctorSchema(ctx context.Context, d *sql.DB, mods []kernel.Module, report *siteDoctorReport) {
	known := map[string]fs.FS{"audit": audit.Migrations(), "jobs": jobs.Migrations(), "diagnostics": diagnostics.Migrations(),
		"observe": observe.Migrations(), "update": update.Migrations()}
	enabled := map[string]bool{"audit": true, "jobs": true, "diagnostics": true, "observe": true, "update": true}
	enabledCount, disabledCount := 0, 0
	for _, m := range mods {
		known[m.Name()] = m.Migrations()
		on := true
		if optional, ok := m.(kernel.OptionalModule); ok {
			var err error
			on, err = optional.Enabled(ctx, d)
			if err != nil {
				report.add("addons", "fail", "An addon's activation state cannot be resolved.", "Check addon configuration and its required dependencies.")
				return
			}
		}
		enabled[m.Name()] = on
		if on {
			enabledCount++
		} else {
			disabledCount++
		}
	}
	report.add("addons", "ok", fmt.Sprintf("Module activation resolved: %d enabled, %d disabled.", enabledCount, disabledCount), "")
	rows, err := d.QueryContext(ctx, `SELECT module, name, checksum FROM schema_migrations`)
	if err != nil {
		report.add("schema", "fail", "Migration metadata is missing or too old to inspect.", "Back up the site, then run gorun migrate with the matching release.")
		return
	}
	applied := map[string]sql.NullString{}
	for rows.Next() {
		var module, name string
		var sum sql.NullString
		if err := rows.Scan(&module, &name, &sum); err != nil {
			rows.Close()
			report.add("schema", "fail", "Cannot read migration metadata.", "Use a verified backup and a matching binary.")
			return
		}
		applied[module+"/"+name] = sum
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		report.add("schema", "fail", "Cannot finish reading migration metadata.", "Check database health and retry.")
		return
	}
	pending, changed, unknown := 0, 0, 0
	for module, files := range known {
		if files == nil {
			continue
		}
		names, err := fs.Glob(files, "migrations/*.sql")
		if err != nil {
			report.add("schema", "fail", "Cannot inspect bundled migrations.", "Reinstall a complete GoRun release.")
			return
		}
		for _, name := range names {
			key := module + "/" + path.Base(name)
			sum, ok := applied[key]
			if !ok {
				if enabled[module] {
					pending++
				}
				continue
			}
			body, err := fs.ReadFile(files, name)
			if err != nil {
				report.add("schema", "fail", "Cannot read bundled migration files.", "Reinstall a complete GoRun release.")
				return
			}
			if sum.Valid && sum.String != db.Checksum(string(body)) {
				changed++
			}
			delete(applied, key)
		}
	}
	unknown = len(applied)
	if pending+changed+unknown > 0 {
		report.add("schema", "fail", fmt.Sprintf("Migrations: %d pending, %d changed, %d unknown.", pending, changed, unknown), "Back up first. Apply pending migrations with gorun migrate; for changed/unknown migrations restore the matching release and investigate instead of editing migration history.")
	} else {
		report.add("schema", "ok", "Recorded migrations match enabled modules and this binary.", "")
	}
}
