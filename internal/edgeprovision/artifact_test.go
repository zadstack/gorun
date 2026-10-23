package edgeprovision

import (
	"strings"
	"testing"
)

func TestEmbeddedArtifactIsPinnedAndManagerOnly(t *testing.T) {
	m, files, e := loadArtifact()
	if e != nil {
		t.Fatal(e)
	}
	if len(m.SHA256) != 64 || m.SHA256 != ArtifactSHA() || !Available() {
		t.Fatal("artifact not verified")
	}
	js, sql := 0, 0
	for n, b := range files {
		if len(b) == 0 {
			t.Fatal("empty artifact")
		}
		if strings.HasSuffix(n, ".js") {
			js++
		}
		if strings.HasSuffix(n, ".sql") {
			sql++
		}
	}
	if js != 3 || sql != 3 {
		t.Fatalf("unexpected artifact %d/%d", js, sql)
	}
}
