package commerce

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// uploadAs posts a multipart body to path with the admin CSRF header and a
// JSON Accept header, returning status and decoded envelope.
func (e *env) uploadAs(path, contentType string, body io.Reader) (int, map[string]any) {
	e.t.Helper()
	_, page := e.do("GET", "/admin/dashboard", nil)
	m := csrfMetaRe.FindStringSubmatch(page)
	if m == nil {
		e.t.Fatal("no csrf meta")
	}
	req, _ := http.NewRequest("POST", e.srv.URL+path, body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", m[1])
	res, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func multipartFile(t *testing.T, field, name string, content []byte) (string, *bytes.Buffer) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if field != "" {
		f, err := w.CreateFormFile(field, name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write(content)
	} else {
		_ = w.WriteField("note", "no file here")
	}
	_ = w.Close()
	return w.FormDataContentType(), &body
}

func storedDigitalFiles(t *testing.T, e *env) []string {
	t.Helper()
	entries, err := os.ReadDir(e.svc.digitalDir())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	return names
}

func TestDigitalUploadRejections(t *testing.T) {
	e := boot(t)
	e.signInAdmin("uploader@example.test", "admin")

	ct, body := multipartFile(t, "file", "a.zip", []byte("data"))
	if st, _ := e.uploadAs("/admin/commerce/products/missing/digital-file", ct, body); st != 404 {
		t.Fatalf("unknown product upload: %d", st)
	}
	ct, body = multipartFile(t, "", "", nil)
	if st, out := e.uploadAs("/admin/commerce/digital-files", ct, body); st != 422 || out["error"] == nil {
		t.Fatalf("no file field: %d %v", st, out)
	}
	ct, body = multipartFile(t, "file", "empty.zip", nil)
	if st, _ := e.uploadAs("/admin/commerce/digital-files", ct, body); st != 422 {
		t.Fatalf("empty file: %d", st)
	}
	if st, _ := e.uploadAs("/admin/commerce/digital-files", "multipart/form-data; boundary=xyz", strings.NewReader("garbage without boundary")); st != 422 {
		t.Fatalf("malformed multipart: %d", st)
	}
	if st, _ := e.uploadAs("/admin/commerce/digital-files", "text/plain", strings.NewReader("plain")); st != 422 {
		t.Fatalf("non-multipart: %d", st)
	}
	if got := storedDigitalFiles(t, e); len(got) != 0 {
		t.Fatalf("rejected uploads left files: %v", got)
	}

	// An extension that is not a safe key character set falls back to .bin.
	ct, body = multipartFile(t, "file", "report.<x>", []byte("payload"))
	st, out := e.uploadAs("/admin/commerce/digital-files", ct, body)
	data, _ := out["data"].(map[string]any)
	key, _ := data["digital_file"].(string)
	if st != 200 || !strings.HasSuffix(key, ".bin") || data["bytes"] != float64(7) || !e.svc.digitalFileExists(key) {
		t.Fatalf("unsafe extension upload: %d %v", st, out)
	}
	if auditCount(e, "commerce.digital.upload") != 1 {
		t.Fatal("upload not audited")
	}
}

func TestDigitalUploadTooLarge(t *testing.T) {
	e := boot(t)
	e.signInAdmin("big@example.test", "admin")
	const maxBytes = 20 << 20
	// Just over the file cap but within the request allowance: copied then removed.
	ct, body := multipartFile(t, "file", "big.zip", bytes.Repeat([]byte("a"), maxBytes+1))
	if st, _ := e.uploadAs("/admin/commerce/digital-files", ct, body); st != 413 {
		t.Fatalf("file over cap: %d", st)
	}
	// Far over the request allowance: rejected while parsing.
	ct, body = multipartFile(t, "file", "huge.zip", bytes.Repeat([]byte("a"), maxBytes+200000))
	if st, _ := e.uploadAs("/admin/commerce/digital-files", ct, body); st != 413 {
		t.Fatalf("request over cap: %d", st)
	}
	if got := storedDigitalFiles(t, e); len(got) != 0 {
		t.Fatalf("oversized uploads left files: %v", got)
	}
}

// paidDigitalOrder returns a paid digital order, its item download path and
// the buyer's user id.
func paidDigitalOrder(t *testing.T, e *env, key string) (orderID, path, userID string) {
	t.Helper()
	ctx := context.Background()
	if err := os.MkdirAll(e.svc.digitalDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.svc.digitalDir(), key), []byte("asset-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.SaveProduct(ctx, Product{Kind: ProductDigital, Name: "Ebook", Slug: "ebook", Active: true, PriceMinor: 4900, Currency: "AED", DigitalFile: key, DownloadDays: 3})
	if err != nil {
		t.Fatal(err)
	}
	orderID = e.checkout(p, "reader@example.test")
	e.pay(orderID)
	o, err := e.svc.Order(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := e.svc.orderItems(ctx, o)
	if err != nil || len(items) != 1 || items[0].DownloadURL == "" {
		t.Fatalf("items = %+v %v", items, err)
	}
	userID = e.str(`SELECT user_id FROM customers WHERE id = ?`, o.CustomerID)
	return orderID, items[0].DownloadURL, userID
}

func TestDigitalDownloadMissingArtifactAndWrongItem(t *testing.T) {
	e := boot(t)
	orderID, path, uid := paidDigitalOrder(t, e, "book.pdf")
	e.signInCustomer(uid)
	if resp, body := e.do("GET", path, nil); resp.StatusCode != 200 || body != "asset-bytes" {
		t.Fatalf("baseline download: %d %q", resp.StatusCode, body)
	}
	if resp, _ := e.do("GET", "/account/download/"+orderID+"/not-an-item", nil); resp.StatusCode != 404 {
		t.Fatalf("unknown item: %d", resp.StatusCode)
	}
	file := filepath.Join(e.svc.digitalDir(), "book.pdf")
	// The artifact replaced by a directory is not served.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.do("GET", path, nil); resp.StatusCode != 404 {
		t.Fatalf("directory artifact: %d", resp.StatusCode)
	}
	// The artifact deleted.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.do("GET", path, nil); resp.StatusCode != 404 {
		t.Fatalf("missing artifact: %d", resp.StatusCode)
	}
	// The whole private directory gone.
	if err := os.RemoveAll(e.svc.digitalDir()); err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.do("GET", path, nil); resp.StatusCode != 404 {
		t.Fatalf("missing directory: %d", resp.StatusCode)
	}
	if n := auditCount(e, "commerce.digital.download"); n != 1 {
		t.Fatalf("download audits = %d, want 1", n)
	}
}

func TestDigitalDownloadByStoreManager(t *testing.T) {
	e := boot(t)
	_, path, _ := paidDigitalOrder(t, e, "manual.zip")
	e.signInAdmin("support@example.test", "admin")
	resp, body := e.do("GET", path, nil)
	if resp.StatusCode != 200 || body != "asset-bytes" {
		t.Fatalf("manager download: %d %q", resp.StatusCode, body)
	}
	if resp.Header.Get("Cache-Control") != "private, no-store" || resp.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), `filename="download.zip"`) {
		t.Fatalf("headers = %v", resp.Header)
	}
}
