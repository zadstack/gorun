package wpmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	clientMaxBody = 64 << 20
	perPage       = 100
	maxPages      = 500
)

var errCountUnknown = errors.New("wpmigrate: collection count unknown")

func optionalRESTUnavailable(err error) bool {
	if errors.Is(err, ErrAuthFailed) || errors.Is(err, ErrAuthUnavailable) {
		return false
	}
	if errors.Is(err, errCountUnknown) {
		return true
	}
	var se *StatusError
	return errors.As(err, &se) && (se.Status == 403 || se.Status == 404 || se.PermissionDenied)
}

// Client reads a WordPress site's REST API: Basic auth with an application
// password, paging via X-WP-TotalPages, retries with backoff on 429/5xx and
// network errors. The URL is user-entered and must be http(s).
type Client struct {
	base       *url.URL // site root, no trailing slash
	user, pass string
	http       *http.Client
	retries    int
	backoff    time.Duration
	limit      func(context.Context) error // rate limit, waited before every request (retries too)
}

// Response is a fetched REST response.
type Response struct {
	Status     int
	Body       []byte
	Total      int
	TotalPages int
	Header     http.Header
}

// StatusError is a non-2xx REST response; Code is WordPress's error code
// (rest_forbidden_context, incorrect_password, …) when present.
type StatusError struct {
	Status           int
	Code             string
	Message          string
	Path             string
	PermissionDenied bool
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("wpmigrate: %s: HTTP %d %s", e.Path, e.Status, e.Code)
}

// authCodes are the WordPress error codes of a credentials problem.
var authCodes = map[string]bool{"rest_not_logged_in": true, "rest_forbidden": true, "incorrect_password": true,
	"invalid_username": true, "invalid_email": true, "rest_forbidden_context": true, "rest_cannot_view": true}

// IsAuthError reports a credentials problem: any 401, or a 403 whose
// WordPress code is an authentication/permission code. Other 403s (a
// security plugin hiding an endpoint) are not auth failures.
func IsAuthError(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	return se.Status == http.StatusUnauthorized || (se.Status == http.StatusForbidden && authCodes[se.Code])
}

// isForbidden reports any 401/403 (data the source will not give us).
func isForbidden(err error) bool {
	if errors.Is(err, ErrAuthFailed) || errors.Is(err, ErrAuthUnavailable) {
		return false
	}
	var se *StatusError
	return errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden)
}

// NewClient validates siteURL and returns a client using hc.
func NewClient(siteURL, user, pass string, hc *http.Client) (*Client, error) {
	u, err := parseSiteURL(siteURL)
	if err != nil {
		return nil, err
	}
	return &Client{base: u, user: user, pass: pass, http: hc, retries: 3, backoff: 300 * time.Millisecond}, nil
}

func parseSiteURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("%w: site URL must be http(s)://host", ErrInvalidSource)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}

// SiteURL returns the normalized site root.
func (c *Client) SiteURL() string { return c.base.String() }

// HasAuth reports whether credentials are configured.
func (c *Client) HasAuth() bool { return c.user != "" }

// Get fetches /wp-json<path>?q. Non-2xx responses return *StatusError
// together with the response.
func (c *Client) Get(ctx context.Context, path string, q url.Values) (*Response, error) {
	u := c.base.String() + "/wp-json" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return c.GetURL(ctx, u)
}

// GetURL fetches an absolute URL on the site with the client's auth.
func (c *Client) GetURL(ctx context.Context, rawURL string) (*Response, error) {
	var last error
	for attempt := 0; attempt < max(c.retries, 1); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.backoff * time.Duration(1<<(attempt-1))):
			}
		}
		resp, err := c.do(ctx, rawURL)
		var denied *StatusError
		if c.HasAuth() && errors.As(err, &denied) && denied.Status == http.StatusUnauthorized {
			return resp, errors.Join(ErrAuthFailed, err)
		}
		if c.HasAuth() && errors.As(err, &denied) && denied.Status == http.StatusForbidden && strings.HasSuffix(denied.Path, "/wp/v2/users/me") {
			return resp, errors.Join(ErrAuthUnavailable, err)
		}
		// WordPress may validate requested statuses before reporting expired
		// Application Password authentication. A fresh profile check tells a
		// revoked credential apart from a valid account's collection limits.
		if c.HasAuth() && errors.As(err, &denied) && (denied.Status == http.StatusForbidden || denied.PermissionDenied) && !strings.HasSuffix(denied.Path, "/wp/v2/users/me") {
			if _, probeErr := c.Get(ctx, "/wp/v2/users/me", url.Values{"context": {"edit"}}); probeErr != nil {
				return resp, probeErr
			}
		}
		if err == nil {
			return resp, nil
		}
		last = err
		var se *StatusError
		if errors.As(err, &se) && se.Status != http.StatusTooManyRequests && se.Status < 500 {
			return resp, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, last
}

func (c *Client) do(ctx context.Context, rawURL string) (*Response, error) {
	if c.limit != nil {
		if err := c.limit(ctx); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GoRun migrator")
	req.Header.Set("Accept", "application/json")
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	hr, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, redactErr(err))
	}
	defer hr.Body.Close()
	body, err := io.ReadAll(io.LimitReader(hr.Body, clientMaxBody))
	if err != nil {
		return nil, fmt.Errorf("%w: read: %v", ErrUnreachable, err)
	}
	resp := &Response{Status: hr.StatusCode, Body: body, Header: hr.Header}
	resp.Total, _ = strconv.Atoi(hr.Header.Get("X-WP-Total"))
	resp.TotalPages, _ = strconv.Atoi(hr.Header.Get("X-WP-TotalPages"))
	if hr.StatusCode < 200 || hr.StatusCode > 299 {
		se := &StatusError{Status: hr.StatusCode, Path: req.URL.Path}
		var e struct {
			Code, Message string
			Data          struct {
				Details map[string]struct{ Code string }
			}
		}
		if json.Unmarshal(body, &e) == nil {
			se.Code, se.Message = e.Code, e.Message
			for _, detail := range e.Data.Details {
				if detail.Code == "rest_forbidden_status" || detail.Code == "rest_forbidden_context" {
					se.PermissionDenied = true
				}
			}
		}
		return resp, se
	}
	return resp, nil
}

// redactErr drops credentials that url errors may echo.
func redactErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		if u, perr := url.Parse(ue.URL); perr == nil {
			return ue.Op + " " + u.Redacted() + ": " + ue.Err.Error()
		}
	}
	return err.Error()
}

// GetJSON fetches path and decodes the body into out.
func (c *Client) GetJSON(ctx context.Context, path string, q url.Values, out any) (*Response, error) {
	resp, err := c.Get(ctx, path, q)
	if err != nil {
		return resp, err
	}
	if err := json.Unmarshal(resp.Body, out); err != nil {
		return resp, fmt.Errorf("%w: %s: invalid JSON: %v", ErrNotWordPress, path, err)
	}
	return resp, nil
}

// Count returns X-WP-Total for a collection (per_page=1).
func (c *Client) Count(ctx context.Context, path string, q url.Values) (int, error) {
	qq := cloneValues(q)
	qq.Set("per_page", "1")
	resp, err := c.Get(ctx, path, qq)
	if err != nil {
		return 0, err
	}
	if _, ok := collectionHeader(resp.Header, "X-WP-Total"); !ok {
		return 0, fmt.Errorf("%w: %s", errCountUnknown, path)
	}
	return resp.Total, nil
}

// All walks every page of a collection, calling fn per element. limitPages
// <= 0 means no limit (bounded by maxPages).
func (c *Client) All(ctx context.Context, path string, q url.Values, limitPages int, fn func(json.RawMessage) error) (int, error) {
	read, err := c.ReadCollection(ctx, path, q, limitPages, fn)
	return read.ReportedTotal, err
}

// CollectionRead separates accessible rows from WordPress's pre-permission-filter bounds.
type CollectionRead struct {
	Visible       int  `json:"visible"`
	ReportedTotal int  `json:"reported_total"`
	ReportedPages int  `json:"reported_pages"`
	PagesRead     int  `json:"pages_read"`
	CountKnown    bool `json:"count_known"`
	PagesKnown    bool `json:"pages_known"`
	Limited       bool `json:"limited"`
}

func collectionHeader(h http.Header, key string) (int, bool) {
	n, err := strconv.Atoi(h.Get(key))
	return n, err == nil && n >= 0
}

func (c *Client) ReadCollection(ctx context.Context, path string, q url.Values, limitPages int, fn func(json.RawMessage) error) (CollectionRead, error) {
	read := CollectionRead{}
	for page := 1; page <= maxPages; page++ {
		qq := cloneValues(q)
		qq.Set("per_page", strconv.Itoa(perPage))
		qq.Set("page", strconv.Itoa(page))
		var arr []json.RawMessage
		resp, err := c.GetJSON(ctx, path, qq, &arr)
		if err != nil {
			return read, err
		}
		read.PagesRead = page
		n, known := collectionHeader(resp.Header, "X-WP-Total")
		pages, pagesKnown := collectionHeader(resp.Header, "X-WP-TotalPages")
		if page == 1 {
			read.ReportedTotal, read.CountKnown = n, known
			read.ReportedPages, read.PagesKnown = pages, pagesKnown
		} else if known != read.CountKnown || pagesKnown != read.PagesKnown || (known && n != read.ReportedTotal) || (pagesKnown && pages != read.ReportedPages) {
			return read, fmt.Errorf("wpmigrate: collection changed while paging: %s", path)
		}
		if pagesKnown && ((pages == 0 && len(arr) > 0) || (known && n > 0 && pages == 0)) {
			return read, fmt.Errorf("wpmigrate: inconsistent collection bounds: %s", path)
		}
		if pagesKnown && pages > maxPages && limitPages <= 0 {
			read.Limited = true
			return read, fmt.Errorf("wpmigrate: collection exceeds %d pages: %s", maxPages, path)
		}
		read.Visible += len(arr)
		for _, raw := range arr {
			if err := fn(raw); err != nil {
				return read, err
			}
		}
		if pagesKnown && page >= pages {
			return read, nil
		}
		if limitPages > 0 && page >= limitPages {
			read.Limited = !pagesKnown || page < pages
			return read, nil
		}
		if !pagesKnown {
			return read, fmt.Errorf("wpmigrate: collection pagination unknown: %s", path)
		}
	}
	read.Limited = true
	return read, fmt.Errorf("wpmigrate: collection exceeds %d pages: %s", maxPages, path)
}

func cloneValues(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}
