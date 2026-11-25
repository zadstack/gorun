package media_test

import (
	"strings"
	"testing"
)

// The library says which types it accepts, and a rejected upload says what
// would work: HEIC photos get the export-as-JPEG step, other files the list.
func TestAdminUploadRejectionsSayWhatWorks(t *testing.T) {
	s := boot(t, "admin")
	_, page := s.do("GET", "/admin/media", nil, "")
	accepted := "JPG, PNG, GIF, WEBP, SVG, PDF, ZIP, MP4, MP3"
	if !strings.Contains(page, accepted) {
		t.Fatal("library does not list the accepted types")
	}
	heic := []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic")
	body, ct := multipartBody(t, nil, map[string][]byte{"IMG_0001.HEIC": heic, "notes.txt": []byte("hello")})
	_, page = s.do("POST", "/admin/media", body, ct, "HX-Request", "true")
	alerts := strings.Split(page, `role="alert"`)
	var heicMsg, txtMsg string
	for _, a := range alerts[1:] {
		a = a[:strings.Index(a, "</div>")]
		switch {
		case strings.Contains(a, "IMG_0001.HEIC"):
			heicMsg = a
		case strings.Contains(a, "notes.txt"):
			txtMsg = a
		}
	}
	// Test admins render in Arabic, the default dashboard language.
	if !strings.Contains(heicMsg, "HEIC") || !strings.Contains(heicMsg, "JPEG") || strings.Contains(heicMsg, accepted) {
		t.Fatalf("HEIC rejection: %q", heicMsg)
	}
	if !strings.Contains(txtMsg, accepted) {
		t.Fatalf("type rejection does not list accepted types: %q", txtMsg)
	}
	// The picker shows the same guidance.
	body, ct = multipartBody(t, map[string]string{"view": "picker"}, map[string][]byte{"notes.txt": []byte("hello")})
	if _, page = s.do("POST", "/admin/media", body, ct, "HX-Request", "true"); !strings.Contains(page, accepted) {
		t.Fatal("picker rejection does not list accepted types")
	}
}
