package kernel

import "context"

const ServiceNavigationPins = "admin.navigation_pins"

// NavigationPins provides personal dashboard placements without coupling auth
// to the optional admin module's tables. Pins remain hidden after preference reset.
type NavigationPins interface {
	PinnedNavigation(context.Context) ([]string, error)
}
