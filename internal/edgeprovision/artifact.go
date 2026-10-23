package edgeprovision

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed runtime/*.js runtime/*.sql runtime/manifest.json
var runtimeFS embed.FS

type artifactManifest struct {
	SHA256            string   `json:"sha256"`
	CompatibilityDate string   `json:"compatibility_date"`
	MainModule        string   `json:"main_module"`
	Files             []string `json:"files"`
}

func loadArtifact() (artifactManifest, map[string][]byte, error) {
	var manifest artifactManifest
	data, err := runtimeFS.ReadFile("runtime/manifest.json")
	if err != nil {
		return manifest, nil, err
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		return manifest, nil, err
	}
	if manifest.MainModule != "index.js" || manifest.CompatibilityDate != "2026-10-01" {
		return manifest, nil, fmt.Errorf("edge artifact manifest invalid")
	}
	names := append([]string(nil), manifest.Files...)
	sort.Strings(names)
	hash := sha256.New()
	files := map[string][]byte{}
	for _, name := range names {
		if !fs.ValidPath(name) || strings.Contains(name, "/") || (!strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".sql")) {
			return manifest, nil, fmt.Errorf("edge artifact path invalid")
		}
		b, e := runtimeFS.ReadFile("runtime/" + name)
		if e != nil {
			return manifest, nil, e
		}
		if _, exists := files[name]; exists {
			return manifest, nil, fmt.Errorf("edge artifact duplicate")
		}
		files[name] = b
		hash.Write([]byte(name + "\x00"))
		hash.Write(b)
	}
	for _, required := range []string{"index.js", "admin.js", "admin-auth.js", "0001_readonly.sql", "0002_owner_admin.sql", "0003_managers.sql"} {
		if _, exists := files[required]; !exists {
			return manifest, nil, fmt.Errorf("edge artifact required file absent")
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != manifest.SHA256 {
		return manifest, nil, fmt.Errorf("edge artifact digest mismatch")
	}
	return manifest, files, nil
}

func ArtifactSHA() string {
	m, _, err := loadArtifact()
	if err != nil {
		return ""
	}
	return m.SHA256
}
func Available() bool { return ArtifactSHA() != "" }
