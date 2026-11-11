package kernel

import (
	"context"
	"fmt"
	"slices"
)

// Notification is one item of the admin notifications centre. Items are
// derived from module data on each request (nothing is queued); only the
// per-user read state is stored, keyed by Key.
type Notification struct {
	// Key identifies the item for read state. It must change when the
	// item should become unread again (e.g. include the newest row's ID).
	Key string
	// TitleKey is an i18n key; Detail is a user value shown under it
	// (isolated with <bdi>), Count an optional number for TitleKey's %d.
	TitleKey, Detail string
	Count            int
	// Href is where to act. At is an RFC 3339 UTC timestamp.
	Href, At string
	// Icon is a Lucide icon name; Tone is "", "info", "warning" or "danger".
	Icon, Tone string
}

// NotificationSource derives notifications for the user in ctx. The admin
// checks Permission (required) before Load; Load filters further (own items,
// items the user may decide) and returns at most limit items, newest first.
type NotificationSource struct {
	ID, Permission string
	Load           func(ctx context.Context, limit int) ([]Notification, error)
}

// AddNotificationSource registers a source; IDs must be unique.
func (a *AdminRegistry) AddNotificationSource(src NotificationSource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if src.ID == "" || src.Permission == "" || src.Load == nil {
		panic("kernel: incomplete notification source")
	}
	if slices.ContainsFunc(a.notify, func(s NotificationSource) bool { return s.ID == src.ID }) {
		panic(fmt.Sprintf("kernel: duplicate notification source %q", src.ID))
	}
	a.notify = append(a.notify, src)
}

// NotificationSources returns the registered sources in registration order.
func (a *AdminRegistry) NotificationSources() []NotificationSource {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]NotificationSource(nil), a.notify...)
}
