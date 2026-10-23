// Package edgeprovision creates isolated GoRun Edge resources in an explicitly
// approved Cloudflare account. It never modifies DNS or imports Full site data.
package edgeprovision

import "context"

type Request struct {
	Label     string `json:"label"`
	Worker    string `json:"worker"`
	SiteID    string `json:"site_id"`
	Title     string `json:"title"`
	Locale    string `json:"locale"`
	AccountID string `json:"account_id"`
}

type Resources struct {
	Worker   string `json:"worker"`
	D1Name   string `json:"d1_name"`
	R2Bucket string `json:"r2_bucket"`
	D1ID     string `json:"d1_id,omitempty"`
	Origin   string `json:"origin,omitempty"`
}

type Inspection struct {
	AccountID        string `json:"account_id"`
	WorkersSubdomain string `json:"workers_subdomain"`
	WorkerAvailable  bool   `json:"worker_available"`
	D1Available      bool   `json:"d1_available"`
	BucketAvailable  bool   `json:"bucket_available"`
	// Read access cannot prove write permission without making a change.
	WritePermissionsVerified bool     `json:"write_permissions_verified"`
	RequiredPermissions      []string `json:"required_permissions"`
}

type Seed struct {
	ManagerID      string
	ManagerKeyHash string
	ManagerKeyHint string
}

// Adapter methods perform one bounded stage. The controller must persist a
// running checkpoint before each mutation and stop for reconciliation whenever
// the provider outcome is uncertain. The adapter never retries mutations.
type Adapter interface {
	Inspect(context.Context, string, Request, Resources) (Inspection, error)
	CreateDB(context.Context, string, Request, Resources) (string, error)
	CreateBucket(context.Context, string, Request, Resources) (string, error)
	ApplySchema(context.Context, string, Request, Resources, Seed) error
	UploadWorker(context.Context, string, Request, Resources, string) error
	EnableWorker(context.Context, string, Request, Resources) (string, error)
}
