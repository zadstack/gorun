package media

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

const generatedTestContent = "01M4QAQM4RP3T0YSGHN6N7HJWS"

func prepareGeneratedContent(t *testing.T, s *service) {
	t.Helper()
	if _, err := s.db.Exec(`CREATE TABLE content(id TEXT PRIMARY KEY,status TEXT); INSERT INTO content VALUES(?, 'draft')`, generatedTestContent); err != nil {
		t.Fatal(err)
	}
}
func TestGeneratedUploadOrganizesContentAndPreservesVariants(t *testing.T) {
	s, _ := testService(t, false)
	prepareGeneratedContent(t, s)
	m, err := s.Upload(context.Background(), "generated.jpg", bytes.NewReader(jpegBytes(t, 800, 400)), UploadMeta{GeneratedContentID: generatedTestContent, GeneratedKind: "image"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(m.StorageKey, "/")
	if len(parts) != 8 || parts[0] != "sites" || len(parts[1]) != 26 || parts[2] != "content" || parts[3] != strings.ToLower(generatedTestContent) || strings.Join(parts[4:7], "/") != "2026/10/10" {
		t.Fatalf("unexpected key: %s", m.StorageKey)
	}
	for _, v := range m.Variants {
		if !strings.HasPrefix(v.URL, "/media/"+strings.Join(parts[:7], "/")+"/") {
			t.Fatal("variant escaped content namespace")
		}
	}
	second, err := s.Upload(context.Background(), "second.jpg", bytes.NewReader(jpegBytes(t, 10, 10)), UploadMeta{GeneratedContentID: generatedTestContent, GeneratedKind: "image"})
	if err != nil || strings.Split(second.StorageKey, "/")[1] != parts[1] {
		t.Fatalf("unstable namespace: %v", err)
	}
	if _, err = s.storage.Open(context.Background(), m.StorageKey); err != nil {
		t.Fatal(err)
	}
}
func TestGeneratedUploadRejectsUnboundAndInvalidKinds(t *testing.T) {
	s, _ := testService(t, false)
	prepareGeneratedContent(t, s)
	for _, meta := range []UploadMeta{{GeneratedContentID: "../escape", GeneratedKind: "image"}, {GeneratedContentID: "01M4QAQM4RP3T0YSGHN6N7HJWX", GeneratedKind: "image"}, {GeneratedContentID: generatedTestContent}, {GeneratedKind: "image"}, {GeneratedContentID: generatedTestContent, GeneratedKind: "video"}} {
		if _, err := s.Upload(context.Background(), "image.jpg", bytes.NewReader(jpegBytes(t, 10, 10)), meta); err == nil {
			t.Fatalf("invalid metadata accepted: %+v", meta)
		}
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM media`).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid generated media persisted")
	}
}
