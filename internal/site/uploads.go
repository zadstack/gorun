package site

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// uploadsDir holds contact form attachments, one file per submission named
// by the submission id. It is outside media/ (never public) and is part of
// `gorun backup`.
const (
	uploadsDir      = "form-uploads"
	attachmentField = "attachment"
	maxFileNameLen  = 120
)

// upload is a validated attachment ready to store; the bytes stay in the
// parsed multipart form (memory or its temp file) until saveUpload copies them.
type upload struct {
	Name, Type string
	Size       int64
	file       *multipart.FileHeader
}

// sniffLen is how much of a file classify reads (DetectContentType uses 512
// bytes, looksLikeMarkup 4096).
const sniffLen = 4096

// officeTypes are the zip-based formats accepted by extension once the
// content sniffs as a zip archive.
var officeTypes = map[string]string{
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

// sniffedTypes maps http.DetectContentType results to the stored type and
// file extension. Anything else (HTML, SVG, scripts, executables) is refused.
var sniffedTypes = map[string][2]string{
	"application/pdf":           {"application/pdf", ".pdf"},
	"image/png":                 {"image/png", ".png"},
	"image/jpeg":                {"image/jpeg", ".jpg"},
	"image/gif":                 {"image/gif", ".gif"},
	"image/webp":                {"image/webp", ".webp"},
	"text/plain; charset=utf-8": {"text/plain; charset=utf-8", ".txt"},
}

var (
	errFileTooLarge = errors.New("site.form.file_too_large")
	errFileType     = errors.New("site.form.file_type")
)

// readUpload returns the attachment of a parsed multipart form, nil when
// none was sent, or errFileTooLarge / errFileType. It reads only the head of
// the file to classify it.
func readUpload(form *multipart.Form, maxBytes int64) (*upload, error) {
	if form == nil || len(form.File[attachmentField]) == 0 {
		return nil, nil
	}
	fh := form.File[attachmentField][0]
	if fh.Size == 0 {
		return nil, nil // an empty file input
	}
	if fh.Size > maxBytes {
		return nil, errFileTooLarge
	}
	f, err := fh.Open()
	if err != nil {
		return nil, fmt.Errorf("site: open upload: %w", err)
	}
	defer f.Close()
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("site: read upload: %w", err)
	}
	ctype, ext, ok := classify(head[:n], fh.Filename)
	if !ok {
		return nil, errFileType
	}
	return &upload{Name: displayName(fh.Filename, ext), Type: ctype, Size: fh.Size, file: fh}, nil
}

// classify sniffs data; the visitor's file name only picks between zip
// based office formats and keeps .csv for plain text.
func classify(data []byte, name string) (ctype, ext string, ok bool) {
	given := strings.ToLower(filepath.Ext(name))
	sniffed := http.DetectContentType(data)
	if sniffed == "application/zip" {
		if t, ok := officeTypes[given]; ok {
			return t, given, true
		}
		return "", "", false
	}
	t, ok := sniffedTypes[sniffed]
	if !ok || (t[1] == ".txt" && looksLikeMarkup(data)) {
		return "", "", false
	}
	if t[1] == ".txt" && given == ".csv" {
		return "text/csv; charset=utf-8", ".csv", true
	}
	return t[0], t[1], true
}

// looksLikeMarkup reports text that a browser could render as a document
// (HTML, SVG, XML), which DetectContentType files under text/plain when
// the tag is not one it knows.
func looksLikeMarkup(data []byte) bool {
	head := strings.ToLower(string(data[:min(len(data), sniffLen)]))
	for _, tag := range []string{"<svg", "<html", "<script", "<!doctype", "<?xml", "<iframe", "<body", "<head"} {
		if strings.Contains(head, tag) {
			return true
		}
	}
	return false
}

// displayName keeps the visitor's base name (control characters, path
// separators and quotes removed, at most 120 runes) with the extension of
// the detected type.
func displayName(name, ext string) string {
	base := filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	stem = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\"<>:|?*`, r) {
			return -1
		}
		return r
	}, stem)
	stem = strings.Trim(strings.TrimSpace(stem), ".")
	for utf8.RuneCountInString(stem) > maxFileNameLen {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = stem[:len(stem)-size]
	}
	if stem == "" {
		stem = "attachment"
	}
	return stem + ext
}

// uploadPath is where submission id's attachment lives.
func (h *handlers) uploadPath(id string) string {
	return filepath.Join(h.d.Config.DataDir, uploadsDir, filepath.Base(id))
}

// saveUpload copies up into the file for submission id (0600, via a temp
// file renamed into place).
func (h *handlers) saveUpload(id string, up *upload) error {
	src, err := up.file.Open()
	if err != nil {
		return fmt.Errorf("site: save upload: %w", err)
	}
	defer src.Close()
	dir := filepath.Join(h.d.Config.DataDir, uploadsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("site: uploads dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return fmt.Errorf("site: save upload: %w", err)
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	_, err = io.Copy(tmp, src)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), h.uploadPath(id))
	}
	if err != nil {
		return fmt.Errorf("site: save upload: %w", err)
	}
	return nil
}

// removeUpload deletes id's attachment; a missing file is not an error.
func (h *handlers) removeUpload(id string) error {
	if err := os.Remove(h.uploadPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("site: remove upload: %w", err)
	}
	return nil
}
