package backup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// stampName matches a backup UTC stamp (same layout as Create).
var stampName = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

// ValidStamp reports whether s is a backup timestamp name.
func ValidStamp(s string) bool {
	return stampName.MatchString(s)
}

// ManifestFile returns the path of <stamp>.manifest.json under the backups
// directory when it exists as a regular file.
func ManifestFile(dataDir, stamp string) (string, error) {
	if !ValidStamp(stamp) {
		return "", fs.ErrNotExist
	}
	return File(dataDir, stamp+".manifest.json")
}

// RestoreDestination is the confined path for an admin restore of stamp:
// <dataDir>/restores/<stamp>. The directory must not exist before Restore.
func RestoreDestination(dataDir, stamp string) (string, error) {
	if !ValidStamp(stamp) || strings.ContainsAny(stamp, `/\`) {
		return "", errors.New("backup: invalid restore stamp")
	}
	return filepath.Join(dataDir, "restores", stamp), nil
}

// Restored reports whether a previous restore for stamp is present under
// <dataDir>/restores/<stamp> (directory with gorun.db).
func Restored(dataDir, stamp string) (string, bool) {
	dest, err := RestoreDestination(dataDir, stamp)
	if err != nil {
		return "", false
	}
	st, err := os.Stat(filepath.Join(dest, "gorun.db"))
	if err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	return dest, true
}
