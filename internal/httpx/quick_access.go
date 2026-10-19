package httpx

// QuickAccessItem contains only a currently authorized registry item. Categories
// share the main navigation's semantic colors; no saved label/URL is trusted.
type QuickAccessItem struct {
	NavItem
	Category string
	Active   bool
}

func quickAccessItems(items []NavItem, path string) []QuickAccessItem {
	bestID, bestLength := "", 0
	for _, item := range items {
		if navPathMatches(path, item.Href) && len(item.Href) > bestLength {
			bestID, bestLength = item.ID, len(item.Href)
		}
	}
	out := make([]QuickAccessItem, 0, len(items))
	for _, item := range items {
		category, _ := navigationBucket(item)
		out = append(out, QuickAccessItem{NavItem: item, Category: category, Active: item.ID == bestID && bestID != ""})
	}
	return out
}
