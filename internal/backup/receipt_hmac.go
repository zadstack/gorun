package backup

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func uploadReceiptHMAC(receipt UploadReceipt, key []byte) string {
	receipt.HMAC = ""
	canonical, _ := json.Marshal(receipt) // Struct fields contain only JSON scalar values and slices.
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("gorun.backup.upload.receipt.v1\x00"))
	mac.Write(canonical)
	return hex.EncodeToString(mac.Sum(nil))
}

func validUploadReceipt(receipt UploadReceipt, manifest *Manifest, key []byte) bool {
	if len(key) != 32 || receipt.Stamp != manifest.Stamp || receipt.ManifestHMAC != manifest.HMAC || receipt.HMAC == "" {
		return false
	}
	valid, err := CheckManifestHMAC(manifest, key)
	if err != nil || !valid {
		return false
	}
	expected, err := hex.DecodeString(receipt.HMAC)
	if err != nil {
		return false
	}
	actual, _ := hex.DecodeString(uploadReceiptHMAC(receipt, key))
	return hmac.Equal(actual, expected)
}
