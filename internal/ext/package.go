package ext

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Package limits.
const (
	MaxZipSize       = 20 << 20 // upload cap
	maxUnpackedSize  = 50 << 20 // total uncompressed bytes extracted
	maxPackageFiles  = 500
	manifestFile     = "manifest.json"
	assetsDir        = "assets"
	stagingDirPrefix = ".staging-"
)

// errPackage marks an unusable package (bad zip, missing manifest,
// unsafe paths); it surfaces as a validation error on "package".
func errPackage(key string) error { return invalid("package", key) }

// wanted reports whether a package-relative path is extracted: only the
// manifest and files under assets/ are kept.
func wanted(rel string) bool {
	return rel == manifestFile || strings.HasPrefix(rel, assetsDir+"/")
}

// zipRoot returns the prefix to strip: "" when manifest.json is at the
// root, "<dir>/" when the archive wraps everything in one folder.
func zipRoot(files []*zip.File) (string, error) {
	for _, f := range files {
		if f.Name == manifestFile {
			return "", nil
		}
	}
	for _, f := range files {
		dir, name := path.Split(f.Name)
		if name == manifestFile && strings.Count(dir, "/") == 1 {
			return dir, nil
		}
	}
	return "", errPackage("ext.package.no_manifest")
}

// extractZip unpacks manifest.json and assets/** of the archive into dst
// (which must exist), refusing absolute paths, "..", symlinks and archives
// over the size and file-count limits.
func extractZip(r io.ReaderAt, size int64, dst string) error {
	if size > MaxZipSize {
		return errPackage("ext.package.too_large")
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return errPackage("ext.package.bad_zip")
	}
	if len(zr.File) > maxPackageFiles {
		return errPackage("ext.package.too_many_files")
	}
	root, err := zipRoot(zr.File)
	if err != nil {
		return err
	}
	var total int64
	seen := map[string]bool{}
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, root) {
			continue
		}
		rel := strings.TrimPrefix(f.Name, root)
		if rel == "" || strings.HasSuffix(rel, "/") || f.FileInfo().IsDir() {
			continue // directory entries (zip -r, Finder): files carry their own paths
		}
		if strings.Contains(rel, `\`) || !filepath.IsLocal(filepath.FromSlash(rel)) || path.Clean(rel) != strings.TrimSuffix(rel, "/") {
			return errPackage("ext.package.unsafe_path")
		}
		if f.Mode()&fs.ModeSymlink != 0 {
			return errPackage("ext.package.unsafe_path")
		}
		if !wanted(rel) {
			continue
		}
		if seen[rel] {
			return errPackage("ext.package.duplicate_entry")
		}
		seen[rel] = true
		total += int64(f.UncompressedSize64)
		if total > maxUnpackedSize {
			return errPackage("ext.package.too_large")
		}
		if err := extractFile(f, filepath.Join(dst, filepath.FromSlash(rel)), maxUnpackedSize); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(f *zip.File, target string, limit int64) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("ext: extract: %w", err)
	}
	rc, err := f.Open()
	if err != nil {
		return errPackage("ext.package.bad_zip")
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("ext: extract: %w", err)
	}
	n, err := io.Copy(out, io.LimitReader(rc, limit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return errPackage("ext.package.bad_zip")
	}
	if n > limit {
		return errPackage("ext.package.too_large")
	}
	return nil
}

// copyPackage copies manifest.json and assets/** from src into dst
// (regular files only; symlinks are skipped).
func copyPackage(src, dst string) error {
	var total int64
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." || rel == assetsDir || strings.HasPrefix(rel, assetsDir+"/") {
				return nil
			}
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() || !wanted(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if total += info.Size(); total > maxUnpackedSize {
			return errPackage("ext.package.too_large")
		}
		return copyFile(p, filepath.Join(dst, filepath.FromSlash(rel)))
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// readManifest reads and parses dir/manifest.json.
func readManifest(dir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errPackage("ext.package.no_manifest")
	}
	if err != nil {
		return nil, fmt.Errorf("ext: read manifest: %w", err)
	}
	m, err := ParseManifest(raw)
	if err != nil {
		return nil, &ValidationError{Fields: map[string]string{"package": "ext.package.bad_manifest"}, Detail: trimErr(err.Error())}
	}
	return m, nil
}
