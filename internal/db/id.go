package db

import "github.com/oklog/ulid/v2"

// NewID returns a new ULID string (26 chars, lexically sortable by time).
// ulid.Make uses a process-wide, mutex-guarded monotonic entropy source, so
// IDs generated within the same millisecond still sort in creation order.
func NewID() string {
	return ulid.Make().String()
}
