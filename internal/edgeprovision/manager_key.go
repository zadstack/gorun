package edgeprovision

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashManagerKey follows the independent Worker's site-scoped credential protocol.
func HashManagerKey(siteID, key string) string {
	sum := sha256.Sum256([]byte("gorun-edge-manager:v1:" + siteID + ":" + key))
	return hex.EncodeToString(sum[:])
}
