package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"

	gpbackup "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/update"
)

var doctorBuildPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.+-]{0,79}$`)

func doctorWebConfig(cfg config.Config, issues config.DiagnosticIssues, effectiveURL string, report *siteDoctorReport) {
	u, err := url.Parse(effectiveURL)
	if issues.PublicBaseURL != nil {
		report.add("public_base_url", "fail", "The production boot URL is missing or unusable.", "Set GORUN_BASE_URL to the public HTTPS URL or attach the public domain on the container platform. GORUN_TRIAL=1 is only for disposable dev-mode sites.")
	} else if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		report.add("public_base_url", "fail", "The effective site base URL is missing or invalid.", "Set GORUN_BASE_URL to an absolute HTTP(S) URL and correct any site.base_url override in Settings → General.")
	} else {
		report.add("public_base_url", "ok", "The effective site base URL is a valid HTTP(S) URL.", "")
	}
	https := err == nil && u.Scheme == "https"
	secureFallback := httpx.SecureCookie(&http.Request{}, cfg.BaseURL)
	switch {
	case issues.TrustedProxies != nil:
		report.add("trusted_proxies", "fail", "The trusted proxy list contains an invalid address or CIDR.", "Set GORUN_TRUSTED_PROXIES to the actual proxy addresses or CIDRs. Exclude all-address networks.")
	case len(cfg.TrustedProxies) == 0 && https:
		report.add("trusted_proxies", "warn", "No proxy is trusted for the HTTPS site. GoRun serve listens over HTTP.", "If a reverse proxy terminates HTTPS, set GORUN_TRUSTED_PROXIES to its IP or CIDR and forward X-Forwarded-Proto: https.")
	case len(cfg.TrustedProxies) == 0:
		report.add("trusted_proxies", "ok", "Proxy headers are disabled.", "")
	default:
		report.add("trusted_proxies", "ok", "Trusted proxy addresses are configured. Header delivery was not checked.", "")
	}
	switch {
	case issues.HSTS != nil:
		report.add("https_security", "fail", "HSTS preload requires the includeSubDomains setting.", "Unset GORUN_HSTS_PRELOAD or enable GORUN_HSTS_INCLUDE_SUBDOMAINS after verifying HTTPS on all subdomains.")
	case !https && secureFallback:
		report.add("https_security", "warn", "The site URL uses HTTP but the configured fallback sets Secure cookies.", "Set site.base_url to the public HTTPS URL or align GORUN_BASE_URL with the intended HTTP installation.")
	case !https && (cfg.HSTSIncludeSubdomains || cfg.HSTSPreload):
		report.add("https_security", "warn", "HSTS options are enabled without an HTTPS site URL.", "Set the public URL to HTTPS or disable the HSTS options.")
	case https && !secureFallback:
		report.add("https_security", "warn", "The HTTPS site URL has no HTTPS cookie fallback. HSTS and Secure cookies depend on trusted HTTPS requests.", "Set GORUN_BASE_URL to the public HTTPS URL and configure the TLS proxy to forward the scheme.")
	case https && (len(cfg.TrustedProxies) == 0 || issues.TrustedProxies != nil):
		report.add("https_security", "warn", "Cookies have a Secure fallback, but HSTS needs a trusted HTTPS request.", "Configure the TLS proxy and GORUN_TRUSTED_PROXIES.")
	case https:
		report.add("https_security", "ok", "Secure cookies have an HTTPS fallback. HSTS is sent when the trusted proxy reports HTTPS.", "")
	default:
		report.add("https_security", "ok", "The site URL does not request HTTPS settings.", "")
	}
}

func doctorDiskSpace(free uint64, ok bool, report *siteDoctorReport) {
	switch {
	case !ok:
		report.add("disk_space", "warn", "Available disk space could not be measured.", "Check the filesystem holding the data directory with your operating system tools.")
	case free < 1<<30:
		report.add("disk_space", "warn", fmt.Sprintf("%.2f GiB available (below 1 GiB).", float64(free)/(1<<30)), "Free space or expand the filesystem holding the data directory.")
	default:
		report.add("disk_space", "ok", fmt.Sprintf("%.2f GiB available.", float64(free)/(1<<30)), "")
	}
}

func doctorBackup(cfg config.Config, now time.Time, report *siteDoctorReport) {
	const fix = "Run gorun backup with the same data directory and configure a regular backup schedule."
	entries, err := gpbackup.List(cfg.DataDir)
	if err != nil {
		report.add("backup_age", "warn", "The local backup directory could not be read.", "Check permissions on the backups directory and run gorun backup.")
		return
	}
	for _, entry := range entries {
		complete := entry.LocalState == "paired_not_inspected" || (entry.LocalState == "legacy_unverified" && !entry.HasManifest)
		if !complete || entry.Time.IsZero() || entry.Time.After(now) {
			continue
		}
		age := now.Sub(entry.Time)
		message := fmt.Sprintf("Latest complete local backup is %.1f days old.", age.Hours()/24)
		if age > 7*24*time.Hour {
			report.add("backup_age", "warn", fmt.Sprintf("Latest complete local backup is %.1f days old (over 7 days).", age.Hours()/24), fix)
		} else {
			report.add("backup_age", "ok", message, "")
		}
		return
	}
	report.add("backup_age", "warn", "No complete local backup was found.", fix)
}

func doctorUpdates(ctx context.Context, source update.Source, build string, report *siteDoctorReport) {
	if !update.IsRelease(build) {
		report.add("updates", "warn", "The running build is not a comparable release tag.", "Compare this build to the release index manually.")
		return
	}
	latest, err := source.Latest(ctx)
	switch {
	case err != nil || !doctorBuildPattern.MatchString(latest.Version) || !update.IsRelease(latest.Version):
		report.add("updates", "warn", "Could not determine the latest release from the configured index.", "Check the release index configuration and connectivity, then retry --check-updates.")
	case update.Newer(latest.Version, build):
		report.add("updates", "warn", "A newer GoRun release is available ("+latest.Version+").", "Review the release notes and follow docs/UPGRADE.md.")
	default:
		report.add("updates", "ok", "The release index reports no newer version.", "")
	}
}
