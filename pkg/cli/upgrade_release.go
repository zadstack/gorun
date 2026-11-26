package cli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const upgradeRepository = "https://github.com/zadstack/gorun"

var upgradeTag = regexp.MustCompile(`^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// HTTP requests go through this interface so release tests use only a local server.
type releaseHTTP interface {
	Do(*http.Request) (*http.Response, error)
}

type binaryReleases struct {
	repository string
	http       releaseHTTP
}

func officialBinaryReleases() binaryReleases {
	return binaryReleases{upgradeRepository, &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || len(via) >= 10 {
				return errors.New("unsafe release redirect")
			}
			return nil
		},
	}}
}

func (r binaryReleases) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.repository+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s returned HTTP %d", path, resp.StatusCode)
	}
	return resp, nil
}

func (r binaryReleases) latest(ctx context.Context) (string, error) {
	resp, err := r.get(ctx, "/releases/latest")
	if err != nil {
		return "", fmt.Errorf("resolve latest release: %w", err)
	}
	defer resp.Body.Close()
	tag, ok := strings.CutPrefix(resp.Request.URL.String(), r.repository+"/releases/tag/")
	if !ok || !upgradeTag.MatchString(tag) {
		return "", errors.New("unexpected latest release redirect; expected vX.Y.Z")
	}
	return tag, nil
}

func (r binaryReleases) stage(ctx context.Context, dir, themeStage, tag, platform string) (string, []string, error) {
	v := strings.TrimPrefix(tag, "v")
	bundle := "gorun-" + v + "-" + platform
	name := bundle + ".tar.gz"
	base := "/releases/download/" + tag + "/"
	const maxArchive = 512 << 20
	sum, err := r.checksum(ctx, base+"SHA256SUMS-"+v+".txt", name)
	if err != nil {
		return "", nil, err
	}
	resp, err := r.get(ctx, base+name)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	var memory bytes.Buffer
	var archive io.ReadWriter = &memory
	var rewind func() error
	if dir != "" {
		file, err := os.CreateTemp(dir, ".gorun-archive-*")
		if err != nil {
			return "", nil, err
		}
		defer os.Remove(file.Name())
		defer file.Close()
		archive = file
		rewind = func() error {
			_, err := file.Seek(0, io.SeekStart)
			return err
		}
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(resp.Body, maxArchive+1))
	if err != nil {
		return "", nil, err
	}
	if n > maxArchive {
		return "", nil, errors.New("release archive exceeds size limit")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), sum) {
		return "", nil, errors.New("release archive checksum mismatch")
	}
	if rewind != nil {
		if err := rewind(); err != nil {
			return "", nil, err
		}
	}
	return stageReleaseContents(dir, themeStage, archive, bundle)
}

func (r binaryReleases) checksum(ctx context.Context, path, archive string) (string, error) {
	resp, err := r.get(ctx, path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	const maxChecksums = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksums+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxChecksums {
		return "", errors.New("release checksums exceed size limit")
	}
	var sum string
	lines := bufio.NewScanner(strings.NewReader(string(data)))
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		if len(fields) < 2 || fields[1] != archive {
			continue
		}
		decoded, err := hex.DecodeString(fields[0])
		if sum != "" || err != nil || len(decoded) != sha256.Size {
			return "", errors.New("invalid or duplicate release archive checksum")
		}
		sum = fields[0]
	}
	if err := lines.Err(); err != nil {
		return "", err
	}
	if sum == "" {
		return "", errors.New("release archive checksum is missing")
	}
	return sum, nil
}

func stageReleaseContents(dir, themeStage string, archive io.Reader, bundle string) (string, []string, error) {
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return "", nil, err
	}
	defer gz.Close()
	const maxContents = 1 << 30
	contents := &io.LimitedReader{R: gz, N: maxContents}
	tr := tar.NewReader(contents)
	var staged string
	found, success := false, false
	defer func() {
		if staged != "" && !success {
			os.Remove(staged)
		}
	}()
	slugs := make(map[string]bool)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !filepath.IsLocal(name) || strings.Contains(name, "\\") || hasParentComponent(name) ||
			(h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg) {
			return "", nil, fmt.Errorf("unsafe release archive member %q", h.Name)
		}
		switch {
		case name == bundle+"/gorun":
			if found || h.Typeflag != tar.TypeReg || h.Size <= 0 || h.Name != name {
				return "", nil, errors.New("invalid or duplicate release executable")
			}
			found = true
			if dir != "" {
				staged, err = stageUpgradeFile(dir, 0o755, func(w io.Writer) error {
					_, err := io.Copy(w, tr)
					return err
				})
			}
		case name == bundle+"/themes":
			if h.Typeflag != tar.TypeDir {
				return "", nil, fmt.Errorf("invalid themes directory member %q", h.Name)
			}
		case strings.HasPrefix(name, bundle+"/themes/"):
			relative := strings.TrimPrefix(name, bundle+"/themes/")
			slug, _, nested := strings.Cut(relative, "/")
			// Archives up to 0.24.0 also carry themes/_base (the source of the
			// tokens default/assets serves) and Go files at the themes root.
			// Neither is a theme: skip them, the safety checks above still ran.
			if !upgradeThemeSlug.MatchString(slug) {
				continue
			}
			if filepath.Clean(relative) != relative ||
				(!nested && h.Typeflag != tar.TypeDir) || (h.Typeflag == tar.TypeReg && h.Name != name) {
				return "", nil, fmt.Errorf("invalid theme member %q", h.Name)
			}
			slugs[slug] = true
			if themeStage != "" {
				err = extractUpgradeTheme(filepath.Join(themeStage, relative), h.Typeflag, tr)
			}
		}
		if err != nil {
			return "", nil, err
		}
	}
	if !found || contents.N == 0 {
		return "", nil, errors.New("release executable is missing or archive contents exceed size limit")
	}
	names := make([]string, 0, len(slugs))
	for slug := range slugs {
		names = append(names, slug)
	}
	sort.Strings(names)
	success = true
	return staged, names, nil
}

func hasParentComponent(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func extractUpgradeTheme(path string, kind byte, content io.Reader) error {
	if kind == tar.TypeDir {
		return os.MkdirAll(path, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, content)
	return errors.Join(err, f.Close())
}

func stageUpgradeFile(dir string, mode os.FileMode, write func(io.Writer) error) (string, error) {
	f, err := os.CreateTemp(dir, ".gorun-upgrade-*")
	if err != nil {
		return "", err
	}
	err = write(f)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if err := errors.Join(err, f.Close()); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func installedBinary() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}
