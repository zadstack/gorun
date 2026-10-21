package commerce

import (
	"context"
	"encoding/json"
)

// Product is a provisioning plan, service or private digital download. PriceMinor is in
// the currency's minor unit (fils for AED). SEO uses the seo.FromMap keys.
const (
	ProductProvisioning = "provisioning"
	ProductService      = "service"
	ProductDigital      = "digital"
)

type IntakeField struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Required  bool   `json:"required"`
	MaxLength int    `json:"max_length"`
}

// CurrencyPrice is one plan price in a single ISO currency.
type CurrencyPrice struct {
	PriceID     string `json:"price_id"`
	AmountMinor int64  `json:"amount_minor"`
}

type Product struct {
	Intake           []IntakeField            `json:"intake,omitempty"`
	Kind             string                   `json:"kind"`
	DigitalFile      string                   `json:"digital_file,omitempty"`
	DownloadDays     int                      `json:"download_days"`
	BillingMode      string                   `json:"billing_mode,omitempty"`
	StripePriceID    string                   `json:"stripe_price_id,omitempty"`
	Interval         string                   `json:"interval,omitempty"`
	TrialDays        int                      `json:"trial_days,omitempty"`
	Prices           map[string]CurrencyPrice `json:"prices,omitempty"`
	ID               string                   `json:"id"`
	Slug             string                   `json:"slug"`
	Name             string                   `json:"name"`
	Description      string                   `json:"description"`
	ShortDescription string                   `json:"short_description"`
	PriceMinor       int64                    `json:"price_minor"`
	Currency         string                   `json:"currency"`
	Active           bool                     `json:"active"`
	GroupSlug        string                   `json:"group_slug"`
	GroupName        string                   `json:"group_name"`
	PlanCode         string                   `json:"plan_code"`
	ServerID         string                   `json:"server_id"`
	AccessMonths     int                      `json:"access_months"`
	Features         []string                 `json:"features"`
	ImageMediaID     string                   `json:"image_media_id"`
	Sort             int                      `json:"sort"`
	SEO              map[string]any           `json:"seo"`
	WPProductID      string                   `json:"wp_product_id"`
}

// WPProduct is a WooCommerce catalog product as read by the migrator (its
// adapter maps an identical struct 1:1). Meta holds WooCommerce meta_data
// (e.g. _mcbb_server_id, _mcbb_plan_code, _mcbb_months).
type WPProduct struct {
	ID                                        int
	Slug, Name, Description, ShortDescription string
	PriceMinor                                int64
	Currency                                  string
	Categories                                []string
	ImageURLs                                 []string
	Meta                                      map[string]string
}

// ProductGroup is a shop section: active products sharing GroupSlug.
type ProductGroup struct {
	Slug, Name string
	Products   []Product
}

// Order statuses.
const (
	OrderPending      = "pending"
	OrderPaid         = "paid"
	OrderProvisioning = "provisioning"
	OrderActive       = "active"
	OrderFailed       = "failed"
	OrderRefunded     = "refunded"
	OrderCancelled    = "cancelled"
)

// Entitlement statuses.
const (
	EntitlementPending   = "pending"
	EntitlementActive    = "active"
	EntitlementSuspended = "suspended"
	EntitlementExpired   = "expired"
	EntitlementFailed    = "failed"
)

// Order freezes a single Stripe product or the line items of a manual request.
type Order struct {
	ReceiptStatus       string         `json:"receipt_status,omitempty"`
	ReceiptError        string         `json:"receipt_error,omitempty"`
	ReceiptSentAt       string         `json:"receipt_sent_at,omitempty"`
	RequestStatus       string         `json:"request_status"`
	FulfillmentNote     string         `json:"fulfillment_note"`
	ID                  string         `json:"id"`
	Number              string         `json:"number"`
	CustomerID          string         `json:"customer_id"`
	Email               string         `json:"email"`
	Status              string         `json:"status"`
	Currency            string         `json:"currency"`
	TotalMinor          int64          `json:"total_minor"`
	ProductID           string         `json:"product_id"`
	ProductName         string         `json:"product_name"`
	ProductSnapshot     map[string]any `json:"product_snapshot"`
	StripeSessionID     string         `json:"stripe_session_id"`
	StripePaymentIntent string         `json:"stripe_payment_intent"`
	PaidAt              string         `json:"paid_at"`
	CreatedAt           string         `json:"created_at"`
	UpdatedAt           string         `json:"updated_at"`
}

// Entitlement is a customer's access to a plan on a billing server, created
// by exactly one order.
type Entitlement struct {
	ID          string         `json:"id"`
	CustomerID  string         `json:"customer_id"`
	Email       string         `json:"email"`
	OrderID     string         `json:"order_id"`
	OrderNumber string         `json:"order_number"`
	ProductID   string         `json:"product_id"`
	ProductName string         `json:"product_name"`
	ServerID    string         `json:"server_id"`
	ServerName  string         `json:"server_name"`
	PlanCode    string         `json:"plan_code"`
	ExternalID  string         `json:"external_id"`
	Status      string         `json:"status"`
	StartsAt    string         `json:"starts_at"`
	EndsAt      string         `json:"ends_at"`
	LastSyncAt  string         `json:"last_sync_at"`
	Meta        map[string]any `json:"meta"`
	CreatedAt   string         `json:"created_at"`
}

// Server is a billing server speaking the mcp-billing/v1 protocol. Secret is
// write-only: SaveServer encrypts it when non-empty (empty keeps the stored
// secret) and reads never return it; SecretSet reports whether one exists.
type Server struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	BaseURL     string `json:"base_url"`
	ClientID    string `json:"client_id"`
	Secret      string `json:"secret,omitempty"`
	SecretSet   bool   `json:"secret_set"`
	Protocol    string `json:"protocol"`
	Status      string `json:"status"`
	LastCheckAt string `json:"last_check_at"`
	LastError   string `json:"last_error"`
}

// Plan is one plan advertised by a billing server's GET /catalog.
type Plan struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	NameAr string `json:"name_ar,omitempty"`
}

// Catalog is a billing server's GET /catalog data. Raw keeps the full
// payload (tools, product description…).
type Catalog struct {
	Product map[string]any  `json:"product,omitempty"`
	Plans   []Plan          `json:"plans"`
	Tools   []any           `json:"tools,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

// ListQuery filters Orders and Entitlements. Search matches the email or
// order number; Status filters exactly. Page/PerPage default 1/20 (max 100).
type ListQuery struct {
	Search, Status string
	CustomerID     string
	Page, PerPage  int
}

// Service is the commerce API published as kernel.ServiceCommerce.
type Service interface {
	Products(ctx context.Context, activeOnly bool) ([]Product, error)
	Product(ctx context.Context, slug string) (*Product, error)
	SaveProduct(ctx context.Context, p Product) (*Product, error)
	DeleteProduct(ctx context.Context, id string) error

	UpsertFromWP(ctx context.Context, wp WPProduct) (*Product, error)
	// ReplaceMediaRef re-points every product image from media oldID to
	// newID (the migrator replaced a re-imported file).
	ReplaceMediaRef(ctx context.Context, oldID, newID string) error

	SubmitCart(ctx context.Context, request CartCheckout) (*Order, error)
	UpdateRequest(ctx context.Context, id string, request RequestUpdate) (*Order, error)
	StartCheckout(ctx context.Context, productID, email string) (redirectURL string, orderID string, err error)
	HandleStripeEvent(ctx context.Context, payload []byte, sigHeader string) error

	Orders(ctx context.Context, q ListQuery) ([]Order, int, error)
	Entitlements(ctx context.Context, q ListQuery) ([]Entitlement, int, error)
	CustomerEntitlements(ctx context.Context, userID string) ([]Entitlement, error)

	Suspend(ctx context.Context, entitlementID string) error
	Resume(ctx context.Context, entitlementID string) error
	Reconcile(ctx context.Context, entitlementID string) error
	LoginLink(ctx context.Context, entitlementID string) (string, error)
	// RequirePlan allows the call when accountID has an on or in-grace
	// subscription for plan (empty plan: any such subscription).
	RequirePlan(ctx context.Context, accountID, plan string) error

	Servers(ctx context.Context) ([]Server, error)
	SaveServer(ctx context.Context, s Server) error
	TestServer(ctx context.Context, id string) (Catalog, error)
}
