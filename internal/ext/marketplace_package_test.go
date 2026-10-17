package ext

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestMarketplaceRejectsExternalCapabilitiesAndUnsafePackage(t *testing.T) {
	for _, manifest := range []string{`{"slug":"test-addon","name":{"en":"Test"},"version":"1.0.0","permissions":["content.view"]}`, `{"slug":"test-addon","name":{"en":"Test"},"version":"1.0.0","endpoint":{"url":"https://example.com","auth":"hmac"}}`} {
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		f, _ := z.Create("manifest.json")
		f.Write([]byte(manifest))
		z.Close()
		if _, err := InspectDeclarativeZip(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err == nil {
			t.Fatal("external package accepted")
		}
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	f, _ := z.Create("../manifest.json")
	f.Write([]byte(`{}`))
	z.Close()
	if _, err := InspectDeclarativeZip(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err == nil {
		t.Fatal("unsafe path accepted")
	}
}
func TestMarketplaceAcceptsNativeDeclarativePackage(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	f, _ := z.Create("manifest.json")
	f.Write([]byte(`{"slug":"test-addon","name":{"en":"Test"},"version":"1.0.0","settings":[{"key":"label","type":"text","label":{"en":"Label"}}]}`))
	z.Close()
	m, err := InspectDeclarativeZip(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil || m.Slug != "test-addon" {
		t.Fatal(m, err)
	}
}
