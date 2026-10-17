package wpmigrate

import (
	"context"
	"errors"
)

// ServiceName is the kernel service name of Service.
const ServiceName = "wpmigrate"

// ProductImporterService is the kernel service name an adapter registers a
// ProductImporter under (the commerce adapter, at integration).
const ProductImporterService = "wpmigrate.product_importer"

// Permission gates the wizard, the API and the MCP tools.
const Permission = "migrations.run"

// WPProduct is a WooCommerce catalog product as read by the migrator. It is
// identical to commerce.WPProduct (the adapter maps it 1:1). Categories are
// category names; Meta holds meta_data (e.g. _mcbb_server, _mcbb_plan,
// _mcbb_months) when the source exposes it (wc/v3 with auth, or WXR).
type WPProduct struct {
	ID                                        int
	Slug, Name, Description, ShortDescription string
	PriceMinor                                int64
	Currency                                  string
	Categories                                []string
	ImageURLs                                 []string
	Meta                                      map[string]string
}

// ProductImporter creates or updates a catalog product from a WordPress
// product (implemented by the commerce module's adapter).
type ProductImporter interface {
	UpsertFromWP(ctx context.Context, p WPProduct) (localID string, err error)
}

// ProductMediaReplacer is optionally implemented by the ProductImporter:
// when a re-imported WordPress file replaces a media item, the migrator
// asks it to move product images from oldID to newID before deleting the
// old media. Without it, the old media is kept whenever this source
// imported products.
type ProductMediaReplacer interface {
	ReplaceMediaRef(ctx context.Context, oldID, newID string) error
}

// Service is the migrator API used by the admin wizard, REST and MCP.
type Service interface {
	Discover(ctx context.Context, sourceID string) (*Report, error)
	Preview(ctx context.Context, migrationID string) (*PreviewResult, error)
	Start(ctx context.Context, migrationID string) error
	Pause(ctx context.Context, migrationID string) error
	Resume(ctx context.Context, migrationID string) error
	RetryFailed(ctx context.Context, migrationID string) error
	Status(ctx context.Context, migrationID string) (*Status, error)
	Log(ctx context.Context, migrationID string, limit int) ([]LogEntry, error)
}

// Errors.
var (
	ErrNotFound        = errors.New("wpmigrate: not found")
	ErrAuthFailed      = errors.New("wpmigrate: auth_failed")
	ErrAuthUnavailable = errors.New("wpmigrate: authenticated profile unavailable")
	ErrUnavailable     = errors.New("wpmigrate: unavailable from this source")
	ErrBadState        = errors.New("wpmigrate: invalid state for this action")
	ErrUnreachable     = errors.New("wpmigrate: site_unreachable")
	ErrNotWordPress    = errors.New("wpmigrate: not_wordpress")
	ErrInvalidSource   = errors.New("wpmigrate: invalid source")
)

// Migration statuses.
const (
	StatusDraft     = "draft"
	StatusScanned   = "scanned"
	StatusMapped    = "mapped"
	StatusPreviewed = "previewed"
	StatusRunning   = "running"
	StatusPaused    = "paused"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusValidated = "validated"
)

// Item statuses.
const (
	ItemPending = "pending"
	ItemDone    = "done"
	ItemFailed  = "failed"
	ItemSkipped = "skipped"
	ItemPreview = "preview"
)

// Slug conflict policies (Options.SlugPolicy).
const (
	SlugSkip      = "skip"
	SlugOverwrite = "overwrite"
	SlugSuffix    = "suffix"
)

// Options tune a migration.
type Options struct {
	SlugPolicy  string `json:"slug_policy"`  // skip | overwrite | suffix (default)
	KeepClasses bool   `json:"keep_classes"` // keep wp-block-* classes in converted HTML
	SkipUsers   bool   `json:"skip_users"`   // map every author to the migration's creator
	Currency    string `json:"currency"`     // product currency when the source has none (WXR)
	// RatePerSecond caps outbound requests to the source (REST reads and
	// media downloads), 1–20; 0 = unset: setting wpmigrate.rate_per_second,
	// else 4.
	RatePerSecond float64 `json:"rate_per_second,omitempty"`
}

func (o Options) normalized() Options {
	switch o.SlugPolicy {
	case SlugSkip, SlugOverwrite, SlugSuffix:
	default:
		o.SlugPolicy = SlugSuffix
	}
	o.RatePerSecond = normalizeRate(o.RatePerSecond)
	return o
}

// Status is a migration's progress.
type Status struct {
	ID         string                    `json:"id"`
	SourceID   string                    `json:"source_id"`
	Status     string                    `json:"status"`
	DryRun     bool                      `json:"dry_run"`
	Step       string                    `json:"step"`
	Phase      int                       `json:"phase"`
	Counts     map[string]map[string]int `json:"counts"` // kind → item status → n
	Total      int                       `json:"total"`
	Done       int                       `json:"done"`
	Failed     int                       `json:"failed"`
	Skipped    int                       `json:"skipped"`
	Pending    int                       `json:"pending"`
	Percent    int                       `json:"percent"`
	StartedAt  string                    `json:"started_at"`
	FinishedAt string                    `json:"finished_at"`
	CreatedAt  string                    `json:"created_at"`
}

// LogEntry is one migration_log row.
type LogEntry struct {
	ID      string         `json:"id"`
	At      string         `json:"at"`
	Level   string         `json:"level"`
	ItemID  string         `json:"item_id,omitempty"`
	Message string         `json:"message"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// PreviewResult summarizes a dry run.
type PreviewResult struct {
	MigrationID        string                    `json:"migration_id"`
	Counts             map[string]map[string]int `json:"counts"` // kind → would-action (create|update|reuse|skip|...) → n
	SlugConflicts      []SlugConflict            `json:"slug_conflicts"`
	Shortcodes         map[string]int            `json:"shortcodes"`
	UnknownBlocks      map[string]int            `json:"unknown_blocks"`
	MarkdownRepaired   int                       `json:"markdown_repaired" doc:"Bodies whose raw Markdown would be converted to HTML."`
	MediaCount         int                       `json:"media_count"`
	MediaBytes         int64                     `json:"media_bytes"`
	Notes              []string                  `json:"notes"` // i18n keys (e.g. wpmigrate.preview.needs_commerce)
	ContentStateCounts map[string]map[string]int `json:"content_state_counts,omitempty" doc:"Selected readable source content rows by type and source state, including planned skips. These are not transferred or validated totals; planned operation counts are separate."`
	ContentStates      []ContentStatePlan        `json:"content_states,omitempty"`
	// OptionsKept: a re-run reused this migration and kept its stored
	// options; the options passed to this call were ignored.
	OptionsKept bool `json:"options_kept,omitempty"`
}

type ContentStatePlan struct {
	Type         string `json:"type"`
	WPID         string `json:"wp_id"`
	SourceStatus string `json:"source_status"`
	LocalStatus  string `json:"local_status"`
	PublishAt    string `json:"publish_at,omitempty"`
	SourceDate   string `json:"source_date,omitempty"`
}

// SlugConflict is a WordPress slug that cannot be kept as is.
type SlugConflict struct {
	Type      string `json:"type"`
	WPID      string `json:"wp_id"`
	Slug      string `json:"slug"`
	FinalSlug string `json:"final_slug"`
	Policy    string `json:"policy"`
	Reason    string `json:"reason"` // existing | duplicate_in_source
}

// Result is the outcome of ImportWXRFile.
type Result struct {
	MigrationID string                    `json:"migration_id"`
	SourceID    string                    `json:"source_id"`
	Status      string                    `json:"status"`
	Counts      map[string]map[string]int `json:"counts"`
	Report      *ValidateReport           `json:"report,omitempty"`
	OptionsKept bool                      `json:"options_kept,omitempty"` // see PreviewResult.OptionsKept
}
