package media

// PublicPath uses the native media handler, including its SVG sandbox and managed-storage routing.
func PublicPath(key string) string {
	if !keyPattern.MatchString(key) {
		return ""
	}
	return URLPrefix + key
}
