// Command wp-record records a WordPress site's public REST responses into a
// directory for local migration rehearsals (spec §7 "recorded fixture
// server"). The committed wpmigrate fixture is fictional: replace a
// recording's content with invented data before committing any of it. It only performs GET requests. Credentials, when given through
// WP_RECORD_USER / WP_RECORD_PASSWORD, are sent but never written: only
// response bodies, status codes and the X-WP-Total / X-WP-TotalPages /
// Content-Type headers are stored.
//
//	go run ./tools/wp-record -site https://example.com -out wp-recording
//
// Output: one file per response plus routes.json mapping the canonical
// request key (path + sorted query without per_page, _embed, _fields and
// page=1) to
// {file, status, headers}.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// endpoints are recorded in order; paged ones follow X-WP-TotalPages.
var endpoints = []struct {
	path  string
	paged bool
}{
	{"/wp-json/", false},
	{"/wp-json/wp/v2/types", false},
	{"/wp-json/wp/v2/taxonomies", false},
	{"/wp-json/wp/v2/posts?per_page=100&_embed", true},
	{"/wp-json/wp/v2/pages?per_page=100", true},
	{"/wp-json/wp/v2/media?per_page=100", true},
	{"/wp-json/wp/v2/categories?per_page=100", true},
	{"/wp-json/wp/v2/tags?per_page=100", true},
	{"/wp-json/wp/v2/users?per_page=100", false},
	{"/wp-json/wp/v2/users/me", false},
	{"/wp-json/wp/v2/menus", false},
	{"/wp-json/wp/v2/pages?per_page=100&context=edit", false},
	{"/wp-json/wc/store/v1/products?per_page=100", true},
	{"/feed/", false},
}

type route struct {
	File    string            `json:"file"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
}

func main() {
	site := flag.String("site", "", "WordPress site URL, e.g. https://example.com")
	out := flag.String("out", "wp-recording", "output directory")
	flag.Parse()
	if *site == "" {
		log.Fatal("wp-record: -site is required")
	}
	base, err := url.Parse(strings.TrimRight(*site, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") {
		log.Fatalf("wp-record: invalid -site %q", *site)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	r := &recorder{base: base, out: *out, routes: map[string]route{},
		client: &http.Client{Timeout: 60 * time.Second},
		user:   os.Getenv("WP_RECORD_USER"), pass: os.Getenv("WP_RECORD_PASSWORD")}
	for _, ep := range endpoints {
		if err := r.record(ep.path, ep.paged); err != nil {
			log.Fatalf("wp-record: %s: %v", ep.path, err)
		}
	}
	if err := r.writeRoutes(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("recorded %d responses into %s\n", len(r.routes), *out)
}

type recorder struct {
	base       *url.URL
	out        string
	client     *http.Client
	routes     map[string]route
	user, pass string
}

func (r *recorder) record(path string, paged bool) error {
	for page := 1; ; page++ {
		p := path
		if page > 1 {
			p += sep(p) + "page=" + strconv.Itoa(page)
		}
		pages, err := r.fetch(p)
		if err != nil {
			return err
		}
		if !paged || page >= pages {
			return nil
		}
	}
}

func sep(p string) string {
	if strings.Contains(p, "?") {
		return "&"
	}
	return "?"
}

// fetch GETs p, stores the body and returns X-WP-TotalPages (1 if absent).
func (r *recorder) fetch(p string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, r.base.String()+p, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "GoRun wp-record")
	if r.user != "" {
		req.SetBasicAuth(r.user, r.pass)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return 0, err
	}
	if p == "/wp-json/" && resp.StatusCode == http.StatusOK {
		if body, err = trimIndex(body); err != nil {
			return 0, err
		}
	}
	key := CanonicalKey(p)
	file := fileName(p, resp.Header.Get("Content-Type"))
	if err := os.WriteFile(filepath.Join(r.out, file), body, 0o644); err != nil {
		return 0, err
	}
	rt := route{File: file, Status: resp.StatusCode, Headers: map[string]string{}}
	for _, h := range []string{"X-WP-Total", "X-WP-TotalPages", "Content-Type"} {
		if v := resp.Header.Get(h); v != "" {
			rt.Headers[h] = v
		}
	}
	r.routes[key] = rt
	pages, _ := strconv.Atoi(resp.Header.Get("X-WP-TotalPages"))
	return max(pages, 1), nil
}

// trimIndex keeps the REST index small: route definitions are replaced by
// empty objects (the keys, i.e. which routes exist, are kept).
func trimIndex(body []byte) ([]byte, error) {
	var idx map[string]json.RawMessage
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}
	var routes map[string]json.RawMessage
	if err := json.Unmarshal(idx["routes"], &routes); err == nil {
		for k := range routes {
			routes[k] = json.RawMessage(`{}`)
		}
		b, _ := json.Marshal(routes)
		idx["routes"] = b
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(idx); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fileName derives a stable file name from the request path.
func fileName(p, contentType string) string {
	u, _ := url.Parse(p)
	name := strings.Trim(strings.ReplaceAll(u.Path, "/", "-"), "-")
	if name == "" {
		name = "root"
	}
	q := u.Query()
	if c := q.Get("context"); c != "" {
		name += "-" + c
	}
	if pg := q.Get("page"); pg != "" && pg != "1" {
		name += "-p" + pg
	}
	ext := ".json"
	if strings.Contains(contentType, "xml") {
		ext = ".xml"
	}
	return name + ext
}

// CanonicalKey is the fixture lookup key: path plus the sorted query without
// per_page, _embed, _fields and page=1 (the test fixture server uses the
// same rule).
func CanonicalKey(p string) string {
	u, err := url.Parse(p)
	if err != nil {
		return p
	}
	q := u.Query()
	q.Del("per_page")
	q.Del("_embed")
	q.Del("_fields")
	if q.Get("page") == "1" {
		q.Del("page")
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	if len(parts) == 0 {
		return u.Path
	}
	return u.Path + "?" + strings.Join(parts, "&")
}

func (r *recorder) writeRoutes() error {
	b, err := json.MarshalIndent(r.routes, "", "  ")
	if err != nil {
		return err
	}
	if len(r.routes) == 0 {
		return errors.New("no routes recorded")
	}
	return os.WriteFile(filepath.Join(r.out, "routes.json"), append(b, '\n'), 0o644)
}
