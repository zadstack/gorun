package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/diagnostics"
	"github.com/zadstack/gorun/internal/kernel"
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

// doctorCmd inspects the installation without booting the kernel or migrating.
func doctorCmd(args []string, out io.Writer) error {
	asJSON, args := boolFlag(args, "json")
	support, args := boolFlag(args, "support")
	checkUpdates, args := boolFlag(args, "check-updates")
	report := siteDoctorReport{OK: true}
	build := version
	if !doctorBuildPattern.MatchString(build) {
		build = "unknown"
	}
	report.add("version", "ok", "GoRun "+build+".", "")
	if support {
		report.add("platform", "ok", "OS "+runtime.GOOS+", architecture "+runtime.GOARCH+".", "")
	}
	cfg, issues, err := config.LoadForDiagnostics(args)
	if err != nil {
		// Configuration errors can include user-supplied credentials and paths.
		report.add("config", "fail", "Configuration could not be resolved.", "Review flags, GORUN_* environment variables, and gorun.toml.")
	} else {
		if issues.TrustedProxies != nil || issues.HSTS != nil || (issues.PublicBaseURL == nil && issues.Secret != nil) {
			report.add("config", "fail", "Configuration is missing or invalid.", "Review the configuration checks below.")
		} else if issues.PublicBaseURL == nil {
			report.add("config", "ok", "Configuration and existing secret are valid.", "")
		}
		doctorSite(cfg, issues, &report)
		if checkUpdates {
			source, err := update.ConfiguredSource(cfg)
			switch {
			case errors.Is(err, update.ErrNotConfigured):
				report.add("updates", "warn", "No release index is configured.", "Set GORUN_UPDATE_SOURCE_URL to a trusted release index and retry --check-updates.")
			case err != nil:
				report.add("updates", "warn", "The configured release index is invalid.", "Review GORUN_UPDATE_SOURCE_URL and GORUN_UPDATE_ARTIFACT.")
			default:
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				doctorUpdates(ctx, source, build, &report)
				cancel()
			}
		}
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		for _, check := range report.Checks {
			line := fmt.Sprintf("[%s] %s: %s", check.Status, check.ID, check.Message)
			if check.Action != "" {
				line += " Fix: " + check.Action
			}
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
		}
	}
	if !report.OK {
		return &exitError{code: 1, msg: "doctor found problems; follow the suggested fixes above"}
	}
	return nil
}

func doctorSite(cfg config.Config, issues config.DiagnosticIssues, report *siteDoctorReport) {
	if issues.Secret != nil {
		report.add("secret_key", "fail", "The site key is missing, unreadable or does not decode to 32 bytes.", "Restore the original GORUN_SECRET_KEY, secret_key in gorun.toml, or dev secret.key. For a new site only, generate a key with openssl rand -base64 32.")
	} else {
		report.add("secret_key", "ok", "The configured site key decodes to 32 bytes.", "")
	}
	themeRoot := theme.ThemesDir(cfg)
	themes, err := os.Stat(themeRoot)
	themesOK := err == nil && themes.IsDir()
	if themesOK {
		report.add("themes_dir", "ok", "The primary themes directory resolves.", "")
	} else {
		report.add("themes_dir", "fail", "The primary themes directory could not be found.", "Set GORUN_THEMES to the installed themes directory or run from the release or repository root.")
	}
	doctorPort(cfg, report)
	effectiveURL := cfg.BaseURL
	st, err := os.Stat(cfg.DataDir)
	if err != nil || !st.IsDir() {
		report.add("data", "fail", "Data directory is missing or inaccessible.", "Check --data and directory permissions. For a new local site run gorun start.")
		for _, id := range []string{"permissions", "data_lock", "disk_space", "backup_age", "database"} {
			report.add(id, "warn", "Check skipped because the data directory is unavailable.", "Fix the data directory check and rerun gorun doctor.")
		}
	} else {
		report.add("data", "ok", "The data directory exists.", "")
		doctorDataDir(cfg, st, report)
		effectiveURL = doctorDatabase(cfg, themeRoot, report)
	}
	doctorWebConfig(cfg, issues, effectiveURL, report)
}

func doctorDataDir(cfg config.Config, st os.FileInfo, report *siteDoctorReport) {
	if runtime.GOOS != "windows" && st.Mode().Perm()&0200 == 0 {
		report.add("permissions", "fail", "Data directory has no owner write permission.", "Grant the server account write access to the data directory.")
	} else if !doctorDirectoryWritable(cfg.DataDir, st) {
		report.add("permissions", "fail", "The data directory is not writable or searchable by this account.", "Run doctor as the service account and grant it write and search access to the data directory.")
	} else {
		report.add("permissions", "ok", "The data directory is writable and searchable by this account.", "")
	}
	held, err := kernel.ProbeDataDirLock(cfg.DataDir)
	switch {
	case err != nil:
		report.add("data_lock", "warn", "Could not inspect gorun.lock.", "Run doctor as the service account and check gorun.lock permissions.")
	case held:
		report.add("data_lock", "warn", "Another process holds gorun.lock.", "If this is your running site, no action is needed. Stop it before starting another worker on this data directory.")
	default:
		report.add("data_lock", "ok", "No other process holds gorun.lock.", "")
	}
	free, _, ok := diagnostics.DiskUsage(cfg.DataDir)
	doctorDiskSpace(free, ok, report)
	doctorBackup(cfg, time.Now(), report)
}

func doctorDatabase(cfg config.Config, themeRoot string, report *siteDoctorReport) (effectiveURL string) {
	effectiveURL = cfg.BaseURL
	if st, err := os.Stat(filepath.Join(cfg.DataDir, "secret.key")); err == nil && runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
		report.add("secret_permissions", "warn", "The local secret file is readable by other accounts.", "Restrict secret.key to its owner (chmod 600).")
	}
	st, err := os.Stat(cfg.DBPath())
	if err != nil || !st.Mode().IsRegular() {
		report.add("database", "fail", "SQLite database is missing or not a regular file.", "Check --data; run gorun start to initialize a new local site.")
		return
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0600 != 0600 {
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
	doctorSchema(ctx, d, cfg, Modules(cfg), report)
	var storedURL, rawURL string
	if err := d.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = 'site.base_url'`).Scan(&rawURL); err == nil {
		if err := json.Unmarshal([]byte(rawURL), &storedURL); err != nil {
			effectiveURL = ""
		} else if storedURL = strings.TrimSpace(storedURL); storedURL != "" {
			effectiveURL = storedURL
		}
	}
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
	roots := append([]string{themeRoot}, theme.ExtraThemesDirs()...)
	if slug == "" || slug == "." || slug == ".." || strings.ContainsAny(slug, `/\`) {
		report.add("theme", "fail", "The active theme name is invalid.", "Select an installed theme in Appearance → Themes or correct GORUN_THEME.")
		return
	}
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
	return
}

func doctorPort(cfg config.Config, report *siteDoctorReport) {
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err == nil {
		_, err = strconv.ParseUint(port, 10, 16)
	}
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
			report.add("port", "warn", "Port is occupied; the local health endpoint responded and may be your running site.", "If this is your running site, no action is needed. Otherwise choose another --addr port.")
			return
		}
	}
	report.add("port", "fail", "Configured port is occupied by another process or cannot be bound.", "Stop the conflicting process, check interface permissions, or choose another --addr port.")
}

func doctorSchema(ctx context.Context, d *sql.DB, cfg config.Config, mods []kernel.Module, report *siteDoctorReport) {
	selected, err := kernel.SelectModules(ctx, cfg, d, mods)
	if err != nil {
		report.add("addons", "fail", "Startup module activation cannot be resolved.", "Check addon configuration and its required dependencies.")
		report.add("schema", "warn", "Pending migrations could not be determined.", "Fix the addon configuration and rerun gorun doctor.")
		return
	}
	disabled := make(map[string]bool, len(mods))
	for _, m := range mods {
		disabled[m.Name()] = true
	}
	for _, m := range selected {
		delete(disabled, m.Name())
	}
	report.add("addons", "ok", fmt.Sprintf("Module activation resolved: %d enabled, %d disabled.", len(selected), len(disabled)), "")

	inspection, err := kernel.InspectSchema(ctx, d, mods)
	if err != nil {
		report.add("schema", "fail", "Cannot inspect migration metadata.", "Check database health and retry.")
		return
	}
	pending := 0
	for _, name := range inspection.Pending {
		module, _, _ := strings.Cut(name, "/")
		if !disabled[module] {
			pending++
		}
	}
	changed, unknown := len(inspection.Mismatched), len(inspection.Unknown)
	summary := fmt.Sprintf("Migrations: %d pending, %d changed, %d unknown.", pending, changed, unknown)
	switch {
	case changed+unknown > 0:
		report.add("schema", "fail", summary, "Use the release matching the database and investigate migration drift before running gorun migrate.")
	case pending > 0:
		report.add("schema", "fail", summary, "Back up the data directory and run gorun migrate.")
	case inspection.LegacyMetadata:
		report.add("schema", "warn", summary+" Migration checksum metadata is missing.", "Back up the data directory and run gorun migrate.")
	default:
		report.add("schema", "ok", "Recorded migrations match enabled modules and this binary.", "")
	}
}
