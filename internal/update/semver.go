package update

import (
	"regexp"
	"strconv"
	"strings"
)

var semverPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

// gitDescribePattern recognises `git describe` output ("3-gabc1234", with
// an optional -dirty suffix), which is a build identity, not a release.
var gitDescribePattern = regexp.MustCompile(`(^|[.-])\d+-g[0-9a-f]{7,}(-dirty)?$|(^|-)dirty$`)

type version struct {
	major, minor, patch int
	pre                 string
}

// parseVersion reads a release tag ("0.15.0", "v0.15.0", "0.15.0-rc.1").
// Build strings such as "dev", "66a2646" or "v0.14.1-3-gabc-dirty" are not
// releases and cannot be compared.
func parseVersion(s string) (version, bool) {
	m := semverPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return version{}, false
	}
	var v version
	v.major, _ = strconv.Atoi(m[1])
	v.minor, _ = strconv.Atoi(m[2])
	v.patch, _ = strconv.Atoi(m[3])
	v.pre = m[4]
	if gitDescribePattern.MatchString(v.pre) {
		return version{}, false
	}
	return v, true
}

// compare returns -1, 0 or 1. A pre-release sorts before its release.
func (v version) compare(o version) int {
	for _, d := range []int{v.major - o.major, v.minor - o.minor, v.patch - o.patch} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	switch {
	case v.pre == o.pre:
		return 0
	case v.pre == "":
		return 1
	case o.pre == "":
		return -1
	}
	return strings.Compare(v.pre, o.pre)
}

// normalizeVersion strips a leading v so "v0.15.0" and "0.15.0" match.
func normalizeVersion(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "v") }

// IsRelease reports whether s is a comparable release tag.
func IsRelease(s string) bool {
	_, ok := parseVersion(s)
	return ok
}

// Newer reports whether candidate is a newer release than current. Either
// side not being a release tag yields false.
func Newer(candidate, current string) bool {
	c, ok1 := parseVersion(candidate)
	r, ok2 := parseVersion(current)
	return ok1 && ok2 && c.compare(r) > 0
}
