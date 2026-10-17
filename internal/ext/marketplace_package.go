package ext

import (
	"errors"
	"io"
	"os"
)

// InspectDeclarativeZip reuses native package guards without installing in the catalog host.
func InspectDeclarativeZip(r io.ReaderAt, size int64) (*Manifest, error) {
	dir, err := os.MkdirTemp("", "gorun-package-inspect-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err = extractZip(r, size, dir); err != nil {
		return nil, err
	}
	m, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	if len(m.Permissions) > 0 || len(m.Hooks) > 0 || m.Endpoint.URL != "" || m.Endpoint.Auth != "" || len(m.AdminNav) > 0 {
		return nil, errors.New("marketplace supports declarative content types, settings and assets only; external services, hooks, API permissions and navigation require a separate trusted deployment contract")
	}
	if fields := m.validate(validateEnv{takenTypes: map[string]bool{"page": true, "post": true}}); len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	return m, nil
}
