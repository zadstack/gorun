package admin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/observe"
)

// Cloudflare connection for the Integrations hub.
//
// The management token is stored encrypted with the site secret key and
// never echoed. It is used for read-only account, permission and inventory
// reads only; it is not an S3 credential (R2 buckets are bound with their
// own access keys) and not a GoRun Edge manager key (a Worker is confirmed
// as an Edge instance only by the manager identity contract). Every remote
// read runs as a bounded background observation fenced by the revision of
// the stored token ciphertext and the selected account.
const (
	settingCFToken       = "integrations.cloudflare.token"
	settingCFTokenHint   = "integrations.cloudflare.token_hint"
	settingCFAccount     = "integrations.cloudflare.account_id"
	settingCFAccountName = "integrations.cloudflare.account_name"
	cfTokenAAD           = "integrations.cloudflare.token"

	kindCFAccounts  = "integrations.cloudflare.accounts"
	kindCFScopes    = "integrations.cloudflare.scopes"
	kindCFInventory = "integrations.cloudflare.inventory"
	kindCFEdge      = "integrations.cloudflare.edge_identity"

	cfObservationKey = "cloudflare"

	cfAPIBaseDefault  = "https://api.cloudflare.com/client/v4"
	cfPerPage         = 100
	cfMaxResponse     = 2 << 20
	cfScopesFreshness = 12 * time.Hour
	cfInventoryFresh  = time.Hour
)

// cfMaxPages bounds inventory pagination; beyond it the inventory is
// reported incomplete instead of guessed. A variable so tests can lower it.
var cfMaxPages = 20

// cfAPIBase and cfNewClient are replaced by tests with an httptest fake.
// Production always talks to the fixed Cloudflare API host through the
// safe client (private networks blocked, no redirects).
var (
	cfAPIBase   = cfAPIBaseDefault
	cfNewClient = func() *http.Client {
		c := httpx.NewSafeClientOpts(httpx.SafeClientOptions{Timeout: 25 * time.Second})
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		return c
	}
	cfTokenPattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]{20,160}$`)
	cfAccountPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
	cfNamePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

	errCFConflict      = errors.New("integrations: cloudflare settings changed")
	errCFNotConfigured = errors.New("integrations: cloudflare token not configured")
	errCFNoAccount     = errors.New("integrations: cloudflare account not selected")
)

// cfInventoryKinds are the discovery keys of kindCFInventory.
var cfInventoryKinds = []string{"r2", "d1", "workers"}

// cfScopeDefs lists the bounded read-only checks and the minimum token
// permission each one needs. "token" verifies the token itself; the others
// prove one read permission each. A verified token says nothing about
// per-service permissions, and the API cannot report permissions a token
// has beyond these, so excess permissions are never claimed to be absent.
var cfScopeDefs = []struct{ ID, Permission string }{
	{"token", "a valid, active API token"},
	{"accounts", "Account Settings: Read"},
	{"r2", "Workers R2 Storage: Read"},
	{"d1", "D1: Read"},
	{"workers", "Workers Scripts: Read"},
}

// cfSnapshot is one settings read of the Cloudflare connection; raw keeps
// the exact stored JSON of the token and account for compare-and-swap.
type cfSnapshot struct {
	Cipher, Hint, AccountID, AccountName string
	raw                                  map[string]string
}

func (s cfSnapshot) configured() bool { return s.Cipher != "" }

// revision fences every observation on the token ciphertext and account.
func (s cfSnapshot) revision(withAccount bool) string {
	if s.Cipher == "" {
		return ""
	}
	if withAccount && s.AccountID == "" {
		return ""
	}
	parts := s.Cipher
	if withAccount {
		parts += "\x00" + s.AccountID
	}
	sum := sha256.Sum256([]byte(parts))
	return hex.EncodeToString(sum[:8])
}

func (h *handlers) cloudflareSnapshot(ctx context.Context) (cfSnapshot, error) {
	raw, err := h.d.Settings.All(ctx, "integrations.cloudflare.")
	if err != nil {
		return cfSnapshot{}, err
	}
	get := func(key string) (string, error) {
		encoded, ok := raw[key]
		if !ok {
			return "", nil
		}
		var v string
		if err := json.Unmarshal(encoded, &v); err != nil {
			return "", errors.New("integrations: invalid stored cloudflare settings")
		}
		return v, nil
	}
	var s cfSnapshot
	for key, dst := range map[string]*string{settingCFToken: &s.Cipher, settingCFTokenHint: &s.Hint, settingCFAccount: &s.AccountID, settingCFAccountName: &s.AccountName} {
		if *dst, err = get(key); err != nil {
			return cfSnapshot{}, err
		}
	}
	s.raw = map[string]string{}
	for _, key := range []string{settingCFToken, settingCFAccount} {
		if v, ok := raw[key]; ok {
			s.raw[key] = string(v)
		}
	}
	return s, nil
}

func (h *handlers) cloudflareToken(s cfSnapshot) (string, error) {
	if s.Cipher == "" {
		return "", errCFNotConfigured
	}
	plain, err := db.Decrypt(h.d.Config.SecretKey, s.Cipher, cfTokenAAD)
	if err != nil || !cfTokenPattern.MatchString(string(plain)) {
		return "", errors.New("token unreadable with the current site secret key")
	}
	return string(plain), nil
}

// cloudflareInput is the connection form / API body. A blank token keeps
// the stored one; Clear removes the token and the account.
type cloudflareInput struct {
	Token     string `form:"token" json:"token"`
	AccountID string `form:"account_id" json:"account_id"`
	Clear     bool   `form:"clear" json:"clear"`
}

// cloudflareValues validates in against the snapshot and returns the
// settings to write. A replaced token or a changed account resets the
// account name (the observations are revision-fenced, so they expire by
// themselves). Field errors are i18n keys.
func (h *handlers) cloudflareValues(ctx context.Context, in cloudflareInput, snapshot cfSnapshot) (map[string]any, map[string]string, bool) {
	fields := map[string]string{}
	vals := map[string]any{}
	if in.Clear {
		for _, k := range []string{settingCFToken, settingCFTokenHint, settingCFAccount, settingCFAccountName} {
			vals[k] = ""
		}
		return vals, fields, true
	}
	token := strings.TrimSpace(in.Token)
	replaced := false
	switch {
	case token == "" && snapshot.Cipher == "":
		fields["token"] = "admin.integrations.cloudflare.error.token"
	case token != "":
		if !cfTokenPattern.MatchString(token) {
			fields["token"] = "admin.integrations.cloudflare.error.token"
			break
		}
		enc, err := db.Encrypt(h.d.Config.SecretKey, []byte(token), cfTokenAAD)
		if err != nil {
			fields["token"] = "admin.settings.error_encrypt"
			break
		}
		vals[settingCFToken] = enc
		vals[settingCFTokenHint] = token[len(token)-4:]
		replaced = true
	}
	account := strings.ToLower(strings.TrimSpace(in.AccountID))
	if account != "" && !cfAccountPattern.MatchString(account) {
		fields["account_id"] = "admin.integrations.cloudflare.error.account"
	}
	if account != "" && account != snapshot.AccountID {
		vals[settingCFAccount] = account
		vals[settingCFAccountName] = h.cloudflareAccountName(ctx, account)
	}
	if replaced && account == "" && snapshot.AccountID != "" {
		// A new token may not see the previously selected account; keep the
		// selection (the scope check reports it) but drop the stale label.
		vals[settingCFAccountName] = ""
	}
	return vals, fields, replaced
}

// cloudflareAccountName looks the label up in the saved accounts
// observation; an account the token has not listed keeps an empty label.
func (h *handlers) cloudflareAccountName(ctx context.Context, id string) string {
	o, err := h.observe.Get(ctx, kindCFAccounts, cfObservationKey)
	if err != nil || !currentCFObservation(o, cfScopesFreshness) {
		return ""
	}
	var data cfAccountsResult
	if json.Unmarshal(o.Saved.Data, &data) != nil {
		return ""
	}
	for _, a := range data.Accounts {
		if a.ID == id {
			return a.Name
		}
	}
	return ""
}

// saveCloudflare writes vals only while the token and account are still
// the ones the caller validated against (compare-and-swap in one
// transaction), so a concurrent save is never silently overwritten.
func (h *handlers) saveCloudflare(ctx context.Context, snapshot cfSnapshot, vals map[string]any) error {
	keys := slices.Sorted(maps.Keys(vals))
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT key, value_json FROM settings WHERE key IN (?, ?)`, settingCFToken, settingCFAccount)
		if err != nil {
			return err
		}
		defer rows.Close()
		current := map[string]string{}
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				return err
			}
			current[k] = v
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if !maps.Equal(snapshot.raw, current) {
			return errCFConflict
		}
		for _, k := range keys {
			if err := h.d.Settings.SetTx(ctx, tx, k, vals[k]); err != nil {
				return err
			}
		}
		return nil
	})
}

// queueCloudflareChecks admits the background reads that make sense for
// the stored configuration; deduplicated refreshes are not errors.
func (h *handlers) queueCloudflareChecks(ctx context.Context) []observe.Ref {
	var refs []observe.Ref
	for _, kind := range []string{kindCFAccounts, kindCFScopes} {
		if ref, err := h.observe.Refresh(ctx, kind, cfObservationKey); err == nil {
			refs = append(refs, ref)
		}
	}
	return refs
}

// --- Cloudflare API client (bounded, read-only) --------------------------------

type cfResponse struct {
	Success    bool            `json:"success"`
	Errors     []cfAPIError    `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo *cfResultInfo   `json:"result_info"`
}

type cfAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfResultInfo struct {
	Page       int    `json:"page"`
	PerPage    int    `json:"per_page"`
	Count      int    `json:"count"`
	TotalCount int    `json:"total_count"`
	TotalPages int    `json:"total_pages"`
	Cursor     string `json:"cursor"`
}

// cfStatusError carries the HTTP status so callers can classify a missing
// permission (401/403) apart from provider failures.
type cfStatusError struct {
	Status int
	Code   int
	Msg    string
}

func (e *cfStatusError) Error() string {
	msg := fmt.Sprintf("HTTP %d", e.Status)
	if e.Code != 0 {
		msg += fmt.Sprintf(" (code %d)", e.Code)
	}
	if e.Msg != "" {
		msg += ": " + e.Msg
	}
	return msg
}

// cfGet performs one GET on the Cloudflare API. Errors never contain the
// token: provider messages are truncated and scrubbed of it.
func cfGet(ctx context.Context, client *http.Client, token, path string, query url.Values) (cfResponse, error) {
	u := strings.TrimRight(cfAPIBase, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return cfResponse{}, errors.New("invalid request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return cfResponse{}, context.DeadlineExceeded
		}
		return cfResponse{}, errors.New("cloudflare api could not be reached safely")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, cfMaxResponse+1))
	if err != nil {
		return cfResponse{}, errors.New("response unreadable")
	}
	if len(raw) > cfMaxResponse {
		return cfResponse{}, errors.New("response too large")
	}
	var body cfResponse
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			if resp.StatusCode/100 != 2 {
				return cfResponse{}, &cfStatusError{Status: resp.StatusCode}
			}
			return cfResponse{}, errors.New("invalid JSON response")
		}
	}
	if resp.StatusCode/100 != 2 || !body.Success {
		e := &cfStatusError{Status: resp.StatusCode}
		if len(body.Errors) > 0 {
			e.Code = body.Errors[0].Code
			e.Msg = cfScrub(body.Errors[0].Message, token)
		}
		return cfResponse{}, e
	}
	return body, nil
}

func cfScrub(msg, token string) string {
	msg = strings.ReplaceAll(msg, token, "[redacted]")
	msg = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\x00' {
			return ' '
		}
		return r
	}, msg)
	if len(msg) > 120 {
		msg = msg[:120] + "…"
	}
	return msg
}

// cfClassify maps a check error to a lamp: a 401/403 or a Cloudflare
// authorization code means the token lacks the permission (incomplete);
// anything else is a provider or network failure (could_not_verify).
func cfClassify(err error) string {
	var se *cfStatusError
	if errors.As(err, &se) {
		switch {
		case se.Status == http.StatusUnauthorized, se.Status == http.StatusForbidden:
			return "incomplete"
		case se.Code == 10000, se.Code == 9109, se.Code == 1000, se.Code == 7003:
			return "incomplete"
		}
	}
	return "could_not_verify"
}

// --- accounts ------------------------------------------------------------------

type cfAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfAccountsResult struct {
	Accounts []cfAccount `json:"accounts"`
	Complete bool        `json:"complete"`
}

func (h *handlers) runCloudflareAccounts(ctx context.Context, _, _ string) (observe.Result, error) {
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		return observe.Result{}, err
	}
	token, err := h.cloudflareToken(snapshot)
	if err != nil {
		return observe.Result{}, err
	}
	var out cfAccountsResult
	seen := map[string]bool{}
	complete, err := cfPaged(ctx, cfNewClient(), token, "/accounts", nil, func(raw json.RawMessage) (int, error) {
		var list []cfAccount
		if err := json.Unmarshal(raw, &list); err != nil {
			return 0, errors.New("invalid accounts list")
		}
		added := 0
		for _, a := range list {
			if cfAccountPattern.MatchString(a.ID) && !seen[a.ID] {
				seen[a.ID] = true
				out.Accounts = append(out.Accounts, cfAccount{ID: a.ID, Name: cfLabel(a.Name)})
				added++
			}
		}
		return added, nil
	})
	if err != nil {
		return observe.Result{}, err
	}
	out.Complete = complete
	if out.Accounts == nil {
		out.Accounts = []cfAccount{}
	}
	return observe.Result{Scope: "discovery", Data: out}, nil
}

// cfLabel keeps provider labels printable: control characters are
// dropped and the length bounded. Markup is left alone; html/template and
// encoding/json escape it where it is rendered.
func cfLabel(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// cfPaged walks a page-numbered or cursor-based listing. page receives
// each page's result and returns how many items it added that were not
// seen before (callers deduplicate by id), so an endpoint that ignores
// paging and repeats the same full page stops after the first repeat
// instead of being re-read until the cap. It returns whether the listing
// was read in full: a page cap or a total_count beyond what was read
// makes the inventory incomplete rather than guessed.
func cfPaged(ctx context.Context, client *http.Client, token, path string, base url.Values, page func(json.RawMessage) (int, error)) (bool, error) {
	cursor := ""
	read := 0
	for n := 1; n <= cfMaxPages; n++ {
		q := url.Values{}
		for k, v := range base {
			q[k] = v
		}
		q.Set("per_page", strconv.Itoa(cfPerPage))
		if cursor != "" {
			q.Set("cursor", cursor)
		} else {
			q.Set("page", strconv.Itoa(n))
		}
		resp, err := cfGet(ctx, client, token, path, q)
		if err != nil {
			return false, err
		}
		count, err := page(resp.Result)
		if err != nil {
			return false, err
		}
		read += count
		info := resp.ResultInfo
		if count == 0 {
			// Nothing new: either the end, or an endpoint repeating its page.
			return true, nil
		}
		switch {
		case info != nil && info.Cursor != "":
			cursor = info.Cursor
			continue
		case info != nil && info.TotalCount > 0 && read < info.TotalCount:
			cursor = ""
			continue
		case info != nil && info.TotalPages > n:
			cursor = ""
			continue
		case info == nil && count == cfPerPage:
			// No paging metadata: a full page may hide more; a repeat stops above.
			cursor = ""
			continue
		}
		return true, nil
	}
	return false, nil
}

// --- scopes ----------------------------------------------------------------------

// cfScopeState is one lamp. Status: passed, incomplete (the token lacks
// the permission), could_not_verify (provider/network failure) or
// not_checked (no account selected).
type cfScopeState struct {
	ID        string `json:"id"`
	Status    string `json:"status" enum:"passed,incomplete,could_not_verify,not_checked"`
	Evidence  string `json:"evidence"`
	Error     string `json:"error"`
	CheckedAt string `json:"checked_at"`
}

type cfScopesResult struct {
	Scopes []cfScopeState `json:"scopes"`
}

func (h *handlers) runCloudflareScopes(ctx context.Context, _, _ string) (observe.Result, error) {
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		return observe.Result{}, err
	}
	token, err := h.cloudflareToken(snapshot)
	if err != nil {
		return observe.Result{}, err
	}
	client := cfNewClient()
	now := time.Now().UTC().Format(time.RFC3339)
	var out cfScopesResult
	lamp := func(id, status, evidence string, err error) {
		s := cfScopeState{ID: id, Status: status, Evidence: evidence, CheckedAt: now}
		if err != nil {
			s.Error = err.Error()
		}
		out.Scopes = append(out.Scopes, s)
	}
	// Token verification is the only check whose failure fails the whole
	// observation: without a usable token nothing below is meaningful.
	resp, err := cfGet(ctx, client, token, "/user/tokens/verify", nil)
	if err != nil {
		return observe.Result{}, errors.New("token verification failed: " + err.Error())
	}
	var verify struct {
		Status    string `json:"status"`
		ExpiresOn string `json:"expires_on"`
	}
	_ = json.Unmarshal(resp.Result, &verify)
	if verify.Status != "active" {
		return observe.Result{}, errors.New("token is not active: " + cfScrub(verify.Status, token))
	}
	evidence := "status active"
	if verify.ExpiresOn != "" {
		evidence += ", expires " + cfScrub(verify.ExpiresOn, token)
	}
	lamp("token", "passed", evidence, nil)

	one := url.Values{"per_page": {"1"}, "page": {"1"}}
	if resp, err := cfGet(ctx, client, token, "/accounts", one); err != nil {
		lamp("accounts", cfClassify(err), "", err)
	} else {
		lamp("accounts", "passed", "accounts listed "+cfCountEvidence(resp), nil)
	}
	if snapshot.AccountID == "" {
		for _, id := range []string{"r2", "d1", "workers"} {
			lamp(id, "not_checked", "", nil)
		}
		return observe.Result{Scope: "connectivity", Data: out}, nil
	}
	acct := "/accounts/" + snapshot.AccountID
	for _, c := range []struct{ id, path string }{{"r2", acct + "/r2/buckets"}, {"d1", acct + "/d1/database"}, {"workers", acct + "/workers/scripts"}} {
		if resp, err := cfGet(ctx, client, token, c.path, one); err != nil {
			lamp(c.id, cfClassify(err), "", err)
		} else {
			lamp(c.id, "passed", "read permission confirmed "+cfCountEvidence(resp), nil)
		}
	}
	return observe.Result{Scope: "connectivity", Data: out}, nil
}

func cfCountEvidence(resp cfResponse) string {
	if resp.ResultInfo != nil && resp.ResultInfo.TotalCount > 0 {
		return fmt.Sprintf("(total %d)", resp.ResultInfo.TotalCount)
	}
	return "(first page)"
}

// --- inventory --------------------------------------------------------------------

// cfResource is one discovered R2 bucket, D1 database or Worker. Only
// metadata the list endpoints return is kept: no objects, rows or code.
type cfResource struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  string `json:"created_at,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
	Location   string `json:"location,omitempty"`
	Class      string `json:"class,omitempty"`
	Version    string `json:"version,omitempty"`
	Tables     int    `json:"tables,omitempty"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
}

type cfInventoryResult struct {
	Kind     string       `json:"kind" enum:"r2,d1,workers"`
	Items    []cfResource `json:"items"`
	Count    int          `json:"count"`
	Complete bool         `json:"complete" doc:"False when a page cap or provider total means not everything was read"`
}

func (h *handlers) cloudflareInventoryRevision(ctx context.Context, key string) (string, error) {
	if !slices.Contains(cfInventoryKinds, key) {
		return "", nil
	}
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		return "", err
	}
	return snapshot.revision(true), nil
}

func (h *handlers) runCloudflareInventory(ctx context.Context, key, _ string) (observe.Result, error) {
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		return observe.Result{}, err
	}
	token, err := h.cloudflareToken(snapshot)
	if err != nil {
		return observe.Result{}, err
	}
	if snapshot.AccountID == "" {
		return observe.Result{}, errCFNoAccount
	}
	acct := "/accounts/" + snapshot.AccountID
	out := cfInventoryResult{Kind: key, Items: []cfResource{}}
	seen := map[string]bool{}
	add := func(r cfResource) int {
		if r.ID == "" || seen[r.ID] {
			return 0
		}
		seen[r.ID] = true
		out.Items = append(out.Items, r)
		return 1
	}
	var path string
	var parse func(json.RawMessage) (int, error)
	switch key {
	case "r2":
		path = acct + "/r2/buckets"
		parse = func(raw json.RawMessage) (int, error) {
			var page struct {
				Buckets []struct {
					Name         string `json:"name"`
					CreationDate string `json:"creation_date"`
					Location     string `json:"location"`
					StorageClass string `json:"storage_class"`
				} `json:"buckets"`
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				return 0, errors.New("invalid bucket list")
			}
			added := 0
			for _, b := range page.Buckets {
				if cfNamePattern.MatchString(b.Name) {
					added += add(cfResource{ID: b.Name, Name: b.Name, CreatedAt: cfLabel(b.CreationDate), Location: cfLabel(b.Location), Class: cfLabel(b.StorageClass)})
				}
			}
			return added, nil
		}
	case "d1":
		path = acct + "/d1/database"
		parse = func(raw json.RawMessage) (int, error) {
			var list []struct {
				UUID      string  `json:"uuid"`
				Name      string  `json:"name"`
				CreatedAt string  `json:"created_at"`
				Version   string  `json:"version"`
				NumTables int     `json:"num_tables"`
				FileSize  float64 `json:"file_size"`
			}
			if err := json.Unmarshal(raw, &list); err != nil {
				return 0, errors.New("invalid database list")
			}
			added := 0
			for _, d := range list {
				added += add(cfResource{ID: cfLabel(d.UUID), Name: cfLabel(d.Name), CreatedAt: cfLabel(d.CreatedAt), Version: cfLabel(d.Version), Tables: d.NumTables, SizeBytes: int64(d.FileSize)})
			}
			return added, nil
		}
	case "workers":
		path = acct + "/workers/scripts"
		parse = func(raw json.RawMessage) (int, error) {
			var list []struct {
				ID         string `json:"id"`
				CreatedOn  string `json:"created_on"`
				ModifiedOn string `json:"modified_on"`
			}
			if err := json.Unmarshal(raw, &list); err != nil {
				return 0, errors.New("invalid worker list")
			}
			added := 0
			for _, w := range list {
				if cfNamePattern.MatchString(w.ID) {
					added += add(cfResource{ID: w.ID, Name: w.ID, CreatedAt: cfLabel(w.CreatedOn), ModifiedAt: cfLabel(w.ModifiedOn)})
				}
			}
			return added, nil
		}
	default:
		return observe.Result{}, errors.New("unknown inventory kind")
	}
	complete, err := cfPaged(ctx, cfNewClient(), token, path, nil, parse)
	if err != nil {
		return observe.Result{}, err
	}
	out.Complete, out.Count = complete, len(out.Items)
	slices.SortFunc(out.Items, func(a, b cfResource) int { return strings.Compare(a.Name, b.Name) })
	return observe.Result{Scope: "discovery", Data: out}, nil
}

// --- Worker identity ----------------------------------------------------------

// cfEdgeIdentity is the saved outcome of the trusted identity check of one
// Worker: the manager contract answered with the primary manager of a
// site. Names never make a Worker a GoRun Edge instance.
type cfEdgeIdentity struct {
	Worker    string `json:"worker"`
	Origin    string `json:"origin"`
	SiteID    string `json:"site_id"`
	ManagerID string `json:"manager_id"`
	Label     string `json:"label"`
	// Association says how the origin was tied to the Worker name. The
	// manager contract proves the origin is a GoRun Edge instance; it
	// cannot prove which Cloudflare script serves that origin. A
	// workers.dev host carrying the script name is "workers_dev_host";
	// anything else (a custom domain) is "operator_asserted".
	Association string `json:"association" enum:"workers_dev_host,operator_asserted"`
}

// workerAssociation classifies how origin relates to the Worker name.
func workerAssociation(worker, origin string) string {
	host := ""
	if u, err := url.Parse(origin); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	if strings.HasPrefix(host, strings.ToLower(worker)+".") && strings.HasSuffix(host, ".workers.dev") {
		return "workers_dev_host"
	}
	return "operator_asserted"
}

// workerDiscovered reports whether name is in the saved Workers inventory
// of the selected account; identity is only recorded for discovered Workers.
func (h *handlers) workerDiscovered(ctx context.Context, name string) bool {
	return h.cloudflareResourceDiscovered(ctx, "workers", name)
}

func (h *handlers) cloudflareResourceDiscovered(ctx context.Context, kind, name string) bool {
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		return false
	}
	return h.cloudflareResourceDiscoveredFor(ctx, snapshot, kind, name)
}

func (h *handlers) cloudflareResourceDiscoveredFor(ctx context.Context, snapshot cfSnapshot, kind, name string) bool {
	o, err := h.observe.Get(ctx, kindCFInventory, kind)
	if err != nil || !currentCFObservation(o, cfInventoryFresh) || o.Saved.Revision != snapshot.revision(true) {
		return false
	}
	var data cfInventoryResult
	if json.Unmarshal(o.Saved.Data, &data) != nil {
		return false
	}
	for _, item := range data.Items {
		if item.Name == name {
			return true
		}
	}
	return false
}

// cfEdgeRevision binds identity evidence to the account the Worker was
// discovered in; the manager key is pasted for the check and never stored.
func (h *handlers) cfEdgeRevision(ctx context.Context, key string) (string, error) {
	if !cfNamePattern.MatchString(key) {
		return "", nil
	}
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		return "", err
	}
	return cfEdgeRevisionFor(snapshot, key), nil
}

func cfEdgeRevisionFor(snapshot cfSnapshot, key string) string {
	if !cfNamePattern.MatchString(key) {
		return ""
	}
	rev := snapshot.revision(true)
	if rev == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(rev + "\x00" + key))
	return hex.EncodeToString(sum[:8])
}

// identifyWorker runs the manager identity contract against origin with
// the pasted key and records the outcome under the Worker's name. It is
// an explicit operator action, bounded by the Edge client's timeouts.
func (h *handlers) identifyWorker(ctx context.Context, client *http.Client, worker, origin, key string) (cfEdgeIdentity, error) {
	if !cfNamePattern.MatchString(worker) {
		return cfEdgeIdentity{}, errConnectionInvalid
	}
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		return cfEdgeIdentity{}, err
	}
	rev := cfEdgeRevisionFor(snapshot, worker)
	if rev == "" {
		return cfEdgeIdentity{}, errCFNoAccount
	}
	if !h.cloudflareResourceDiscoveredFor(ctx, snapshot, "workers", worker) {
		return cfEdgeIdentity{}, errConnectionInvalid
	}
	normalized, err := validateEdgeOrigin(origin)
	if err != nil {
		return cfEdgeIdentity{}, errEdgeOrigin
	}
	key = strings.TrimSpace(key)
	if !edgeKeyPattern.MatchString(key) {
		return cfEdgeIdentity{}, errEdgeCheck
	}
	started := time.Now()
	info, err := edgeWhoami(ctx, client, normalized, key)
	identity := cfEdgeIdentity{Worker: worker, Origin: normalized, SiteID: info.SiteID, ManagerID: info.ManagerID, Label: info.Label, Association: workerAssociation(worker, normalized)}
	var runErr error
	if err != nil {
		runErr = errors.New("manager identity check failed: the origin did not answer the manager contract for this key")
	}
	current, cerr := h.cloudflareSnapshot(ctx)
	if cerr != nil {
		return cfEdgeIdentity{}, cerr
	}
	if current.revision(true) != snapshot.revision(true) {
		return cfEdgeIdentity{}, errConnectionConflict
	}
	if rerr := h.observe.Record(ctx, kindCFEdge, worker, rev, observe.Result{Scope: "identity", Data: identity}, runErr, time.Since(started)); rerr != nil {
		return cfEdgeIdentity{}, rerr
	}
	h.d.Audit.Record(ctx, "integrations.worker_identify", "integrations", worker, map[string]any{"origin": normalized, "ok": err == nil, "association": identity.Association})
	if err != nil {
		return cfEdgeIdentity{}, errEdgeCheck
	}
	return identity, nil
}

// registerIntegrationObservations declares the Cloudflare observation
// kinds. Accounts and scopes are scheduled (cheap, read-only); inventory
// refreshes hourly per kind with a cap of one in flight; identity runs on
// request only.
func (h *handlers) registerIntegrationObservations() {
	enabled := func(ctx context.Context) bool {
		s, err := h.cloudflareSnapshot(ctx)
		return err == nil && s.configured()
	}
	single := func(ctx context.Context) ([]string, error) { return []string{cfObservationKey}, nil }
	h.observe.Register(observe.Spec{Kind: kindCFAccounts, Freshness: cfScopesFreshness, Timeout: 25 * time.Second, MaxAttempts: 2, MaxInFlight: 1,
		Keys: single, Enabled: enabled,
		Revision: func(ctx context.Context, key string) (string, error) {
			if key != cfObservationKey {
				return "", nil
			}
			s, err := h.cloudflareSnapshot(ctx)
			if err != nil {
				return "", err
			}
			return s.revision(false), nil
		},
		Run: h.runCloudflareAccounts, Remediation: "Confirm the token is active and includes Account Settings: Read, then refresh."})
	h.observe.Register(observe.Spec{Kind: kindCFScopes, Freshness: cfScopesFreshness, Timeout: 40 * time.Second, MaxAttempts: 2, MaxInFlight: 1,
		Keys: single, Enabled: enabled,
		Revision: func(ctx context.Context, key string) (string, error) {
			if key != cfObservationKey {
				return "", nil
			}
			s, err := h.cloudflareSnapshot(ctx)
			if err != nil {
				return "", err
			}
			if s.AccountID != "" {
				return s.revision(true), nil
			}
			return s.revision(false), nil
		},
		Run: h.runCloudflareScopes, Remediation: "Create a dedicated API token with the minimum read permissions listed on the Integrations page, save it, then run the check again."})
	h.observe.Register(observe.Spec{Kind: kindCFInventory, Freshness: cfInventoryFresh, Timeout: 60 * time.Second, MaxAttempts: 2, MaxInFlight: 1,
		Keys: func(context.Context) ([]string, error) { return slices.Clone(cfInventoryKinds), nil }, Enabled: enabled,
		Revision: h.cloudflareInventoryRevision, Run: h.runCloudflareInventory,
		Remediation: "Grant the token the read permission for this service on the selected account, then refresh the inventory."})
	// Identity evidence is recorded only by the explicit check with a
	// pasted key (Manual): no refresh can fail it, and it counts for as
	// long as other capability evidence.
	h.observe.Register(observe.Spec{Kind: kindCFEdge, Manual: true, Freshness: evidenceTTL, Timeout: 15 * time.Second, MaxAttempts: 1, Enabled: enabled,
		Revision: h.cfEdgeRevision,
		Run: func(context.Context, string, string) (observe.Result, error) {
			return observe.Result{}, observe.ErrManual
		},
		Remediation: "Paste the GoRun Edge manager key of this Worker and run the identity check again."})
}
