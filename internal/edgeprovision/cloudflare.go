package edgeprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const apiOrigin = "https://api.cloudflare.com/client/v4"
const responseLimit = 8 << 20

var accountPattern = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

// Workers.dev uses DNS labels; R2 additionally requires at least three characters.
var hostLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var databasePattern = regexp.MustCompile(`^[a-fA-F0-9-]{32,36}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ProviderError intentionally excludes upstream text, request URLs and credentials.
type ProviderError struct {
	Stage  string
	Status int
	Code   int
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("cloudflare stage=%s status=%d code=%d", e.Stage, e.Status, e.Code)
}

type concreteCloudflareAdapter struct {
	client *http.Client
	base   string
}

func NewAdapter() Adapter {
	return &concreteCloudflareAdapter{client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base: apiOrigin}
}
func fail(stage string) error { return &ProviderError{Stage: stage} }
func validate(token string, q Request, r Resources) error {
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") || len(token) > 4096 || !accountPattern.MatchString(q.AccountID) || !hostLabelPattern.MatchString(q.Worker) || q.Worker != r.Worker || !namePattern.MatchString(r.D1Name) || !namePattern.MatchString(r.R2Bucket) || !idPattern.MatchString(q.SiteID) || (q.Locale != "ar" && q.Locale != "en") || len(q.Title) < 1 || utf8.RuneCountInString(q.Title) > 200 {
		return fail("validation")
	}
	return nil
}

type envelope struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Errors  []struct {
		Code int `json:"code"`
	} `json:"errors"`
	Info struct {
		Page       int    `json:"page"`
		TotalPages int    `json:"total_pages"`
		Count      *int   `json:"count"`
		TotalCount *int   `json:"total_count"`
		PerPage    int    `json:"per_page"`
		Cursor     string `json:"cursor"`
	} `json:"result_info"`
}

func (a *concreteCloudflareAdapter) call(ctx context.Context, token, stage, method, path, kind string, body io.Reader) (envelope, error) {
	var e envelope
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, method, a.base+path, body)
	if err != nil {
		return e, fail(stage)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if kind != "" {
		req.Header.Set("Content-Type", kind)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return e, fail(stage)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if err != nil || len(data) > responseLimit {
		return e, fail(stage)
	}
	if json.Unmarshal(data, &e) != nil {
		return e, &ProviderError{Stage: stage, Status: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !e.Success {
		code := 0
		if len(e.Errors) > 0 {
			code = e.Errors[0].Code
		}
		return e, &ProviderError{Stage: stage, Status: resp.StatusCode, Code: code}
	}
	return e, nil
}
func (a *concreteCloudflareAdapter) json(ctx context.Context, t, s, m, p string, v any) (envelope, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return envelope{}, fail(s)
	}
	return a.call(ctx, t, s, m, p, "application/json", bytes.NewReader(b))
}
func prefix(q Request) string { return "/accounts/" + q.AccountID }
func (a *concreteCloudflareAdapter) inspect(ctx context.Context, t string, q Request, r Resources) (Inspection, error) {
	out := Inspection{AccountID: q.AccountID, RequiredPermissions: []string{"Account Settings Read", "Workers Scripts Write", "Workers R2 Storage Write", "D1 Write"}}
	if err := validate(t, q, r); err != nil {
		return out, err
	}
	if _, err := a.call(ctx, t, "account", http.MethodGet, prefix(q), "", nil); err != nil {
		return out, err
	}
	sub, err := a.call(ctx, t, "subdomain", http.MethodGet, prefix(q)+"/workers/subdomain", "", nil)
	if err != nil {
		return out, err
	}
	var sd struct {
		Subdomain string `json:"subdomain"`
	}
	if json.Unmarshal(sub.Result, &sd) != nil || !hostLabelPattern.MatchString(sd.Subdomain) {
		return out, fail("subdomain")
	}
	out.WorkersSubdomain = sd.Subdomain
	workers, err := a.call(ctx, t, "workers", http.MethodGet, prefix(q)+"/workers/scripts", "", nil)
	if err != nil {
		return out, err
	}
	if workers.Info.TotalPages > 1 {
		return out, fail("workers_incomplete")
	}
	var ws []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(workers.Result, &ws) != nil || ws == nil {
		return out, fail("workers")
	}
	out.WorkerAvailable = true
	for _, w := range ws {
		if w.ID == r.Worker {
			out.WorkerAvailable = false
		}
	}
	out.D1Available = true
	complete := false
	readDatabases, expectedTotal := 0, -1
	seenDatabases := map[string]bool{}
	for page := 1; page <= 20; page++ {
		e, err := a.call(ctx, t, "databases", http.MethodGet, fmt.Sprintf("%s/d1/database?page=%d&per_page=100", prefix(q), page), "", nil)
		if err != nil {
			return out, err
		}
		var rows []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(e.Result, &rows) != nil || rows == nil {
			return out, fail("databases")
		}
		if e.Info.Page < 0 || (e.Info.Page > 0 && e.Info.Page != page) || e.Info.TotalPages < 0 || e.Info.PerPage < 0 || (e.Info.Count != nil && *e.Info.Count != len(rows)) {
			return out, fail("databases_metadata")
		}
		if e.Info.TotalCount != nil {
			if *e.Info.TotalCount < 0 || (expectedTotal >= 0 && expectedTotal != *e.Info.TotalCount) {
				return out, fail("databases_metadata")
			}
			expectedTotal = *e.Info.TotalCount
		}
		for _, row := range rows {
			if row.Name == "" || seenDatabases[row.Name] {
				return out, fail("databases_incomplete")
			}
			seenDatabases[row.Name] = true
			if row.Name == r.D1Name {
				out.D1Available = false
			}
		}
		readDatabases += len(rows)
		if expectedTotal >= 0 {
			if readDatabases > expectedTotal {
				return out, fail("databases_metadata")
			}
			if readDatabases == expectedTotal {
				if e.Info.TotalPages > page {
					return out, fail("databases_metadata")
				}
				complete = true
				break
			}
			if len(rows) == 0 || (e.Info.TotalPages > 0 && page >= e.Info.TotalPages) {
				return out, fail("databases_incomplete")
			}
			continue
		}
		if e.Info.TotalPages > 0 {
			if page >= e.Info.TotalPages {
				complete = true
				break
			}
		} else {
			pageSize := 100
			if e.Info.PerPage > 0 {
				pageSize = e.Info.PerPage
			}
			if len(rows) > pageSize {
				return out, fail("databases_metadata")
			}
			if len(rows) < pageSize {
				complete = true
				break
			}
		}
	}
	if !complete {
		return out, fail("databases_incomplete")
	}
	out.BucketAvailable = true
	cursor := ""
	seen := map[string]bool{}
	complete = false
	for page := 0; page < 20; page++ {
		path := prefix(q) + "/r2/buckets?per_page=100"
		if cursor != "" {
			if len(cursor) > 2048 || strings.ContainsAny(cursor, "\r\n\x00") {
				return out, fail("buckets_cursor")
			}
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		e, err := a.call(ctx, t, "buckets", http.MethodGet, path, "", nil)
		if err != nil {
			return out, err
		}
		var result struct {
			Buckets []struct {
				Name string `json:"name"`
			} `json:"buckets"`
			Cursor string `json:"cursor"`
		}
		if json.Unmarshal(e.Result, &result) != nil || result.Buckets == nil {
			return out, fail("buckets")
		}
		for _, row := range result.Buckets {
			if row.Name == r.R2Bucket {
				out.BucketAvailable = false
			}
		}
		next := result.Cursor
		if next == "" {
			next = e.Info.Cursor
		}
		if next == "" {
			complete = true
			break
		}
		if seen[next] {
			return out, fail("buckets_cursor_loop")
		}
		seen[next] = true
		cursor = next
	}
	if !complete {
		return out, fail("buckets_incomplete")
	}
	return out, nil
}
func (a *concreteCloudflareAdapter) Inspect(c context.Context, t string, q Request, r Resources) (Inspection, error) {
	return a.inspect(c, t, q, r)
}
func (a *concreteCloudflareAdapter) CreateDB(c context.Context, t string, q Request, r Resources) (string, error) {
	i, e := a.inspect(c, t, q, r)
	if e != nil {
		return "", e
	}
	if !i.D1Available || !i.WorkerAvailable || !i.BucketAvailable {
		return "", fail("collision")
	}
	v, e := a.json(c, t, "create_database", http.MethodPost, prefix(q)+"/d1/database", map[string]string{"name": r.D1Name})
	if e != nil {
		return "", e
	}
	var db struct {
		UUID string `json:"uuid"`
	}
	if json.Unmarshal(v.Result, &db) != nil || !databasePattern.MatchString(db.UUID) {
		return "", fail("create_database_result")
	}
	return db.UUID, nil
}
func (a *concreteCloudflareAdapter) CreateBucket(c context.Context, t string, q Request, r Resources) (string, error) {
	i, e := a.inspect(c, t, q, r)
	if e != nil {
		return "", e
	}
	if !i.BucketAvailable || !i.WorkerAvailable {
		return "", fail("collision")
	}
	v, e := a.json(c, t, "create_bucket", http.MethodPost, prefix(q)+"/r2/buckets", map[string]string{"name": r.R2Bucket})
	if e != nil {
		return "", e
	}
	var b struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(v.Result, &b) != nil || b.Name != r.R2Bucket {
		return "", fail("create_bucket_result")
	}
	return b.Name, nil
}
func (a *concreteCloudflareAdapter) ApplySchema(c context.Context, t string, q Request, r Resources, s Seed) error {
	if e := validate(t, q, r); e != nil {
		return e
	}
	if !databasePattern.MatchString(r.D1ID) || !idPattern.MatchString(s.ManagerID) || !hashPattern.MatchString(s.ManagerKeyHash) || len(s.ManagerKeyHint) != 4 || strings.ContainsAny(s.ManagerKeyHint, "\r\n") {
		return fail("schema_validation")
	}
	_, files, e := loadArtifact()
	if e != nil {
		return fail("artifact")
	}
	names := []string{}
	for n := range files {
		if strings.HasSuffix(n, ".sql") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	query := func(sql string, params []any) error {
		v, e := a.json(c, t, "schema", http.MethodPost, prefix(q)+"/d1/database/"+r.D1ID+"/query", map[string]any{"sql": sql, "params": params})
		if e != nil {
			return e
		}
		var results []struct {
			Success bool `json:"success"`
		}
		if json.Unmarshal(v.Result, &results) != nil || len(results) == 0 {
			return fail("schema_result")
		}
		for _, r := range results {
			if !r.Success {
				return fail("schema_result")
			}
		}
		return nil
	}
	for _, n := range names {
		if e = query(string(files[n]), []any{}); e != nil {
			return e
		}
	}
	if e = query("INSERT INTO edge_sites(id,title,default_locale) VALUES(?,?,?)", []any{q.SiteID, q.Title, q.Locale}); e != nil {
		return e
	}
	return query("INSERT INTO edge_managers(site_id,id,label,key_hash,key_hint,primary_manager,created_at) VALUES(?,?,?,?,?,1,?)", []any{q.SiteID, s.ManagerID, "GoRun manager", s.ManagerKeyHash, s.ManagerKeyHint, time.Now().UTC().Format(time.RFC3339)})
}
func (a *concreteCloudflareAdapter) UploadWorker(c context.Context, t string, q Request, r Resources, digest string) error {
	m, files, e := loadArtifact()
	if e != nil || digest == "" || digest != m.SHA256 {
		return fail("artifact")
	}
	if e = validate(t, q, r); e != nil {
		return e
	}
	if !databasePattern.MatchString(r.D1ID) {
		return fail("database_binding")
	}
	i, e := a.inspect(c, t, q, r)
	if e != nil {
		return e
	}
	if !i.WorkerAvailable {
		return fail("collision")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadata := map[string]any{"main_module": m.MainModule, "compatibility_date": m.CompatibilityDate, "bindings": []any{map[string]string{"type": "d1", "name": "DB", "id": r.D1ID}, map[string]string{"type": "r2_bucket", "name": "MEDIA", "bucket_name": r.R2Bucket}, map[string]string{"type": "plain_text", "name": "SITE_ID", "text": q.SiteID}}}
	part, e := writer.CreateFormField("metadata")
	if e != nil {
		return fail("multipart")
	}
	if e = json.NewEncoder(part).Encode(metadata); e != nil {
		return fail("multipart")
	}
	names := []string{}
	for n := range files {
		if strings.HasSuffix(n, ".js") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, n, n))
		h.Set("Content-Type", "application/javascript+module")
		p, e := writer.CreatePart(h)
		if e != nil {
			return fail("multipart")
		}
		if _, e = p.Write(files[n]); e != nil {
			return fail("multipart")
		}
	}
	if e = writer.Close(); e != nil {
		return fail("multipart")
	}
	_, e = a.call(c, t, "upload_worker", http.MethodPut, prefix(q)+"/workers/scripts/"+r.Worker, writer.FormDataContentType(), &body)
	return e
}
func (a *concreteCloudflareAdapter) EnableWorker(c context.Context, t string, q Request, r Resources) (string, error) {
	if e := validate(t, q, r); e != nil {
		return "", e
	}
	v, e := a.call(c, t, "subdomain", http.MethodGet, prefix(q)+"/workers/subdomain", "", nil)
	if e != nil {
		return "", e
	}
	var sd struct {
		Subdomain string `json:"subdomain"`
	}
	if json.Unmarshal(v.Result, &sd) != nil || !hostLabelPattern.MatchString(sd.Subdomain) {
		return "", fail("subdomain")
	}
	_, e = a.json(c, t, "enable_worker", http.MethodPost, prefix(q)+"/workers/scripts/"+r.Worker+"/subdomain", map[string]bool{"enabled": true})
	if e != nil {
		return "", e
	}
	return "https://" + r.Worker + "." + sd.Subdomain + ".workers.dev", nil
}
