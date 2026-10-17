package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type connectionModel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type connectionModelError struct {
	Code   string
	Status int
}

func (e *connectionModelError) Error() string  { return e.Code }
func modelError(code string, status int) error { return &connectionModelError{code, status} }

type connectionModelPage struct {
	Data []struct {
		ID   string `json:"id"`
		Name string `json:"display_name"`
	} `json:"data"`
	More bool   `json:"has_more"`
	Last string `json:"last_id"`
}

func fetchConnectionModelPage(ctx context.Context, client *http.Client, c connectionRecord, s connectionSecrets, cursor string) (connectionModelPage, error) {
	var body connectionModelPage
	endpoint := strings.TrimRight(c.Endpoint, "/") + "/models"
	if cursor != "" {
		endpoint += "?after_id=" + url.QueryEscape(cursor)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if e != nil {
		return body, modelError("invalid", 422)
	}
	req.Header.Set("Accept", "application/json")
	if c.Provider == "anthropic" {
		req.Header.Set("x-api-key", s.Token)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, e := client.Do(req)
	if e != nil {
		return body, modelError("unreachable", 502)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		code := "provider_error"
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			code = "provider_auth"
		}
		if resp.StatusCode == 429 {
			code = "provider_rate"
		}
		return body, modelError(code, 502)
	}
	if decodeConnectionJSON(resp.Body, &body) != nil {
		return body, modelError("response_invalid", 502)
	}
	return body, nil
}

type connectionModelCatalog struct {
	Models        []connectionModel `json:"models"`
	ExcludedCount int               `json:"excluded_count"`
}

func discoverAIModels(ctx context.Context, client *http.Client, c connectionRecord, s connectionSecrets) ([]connectionModel, error) {
	catalog, err := discoverAIModelCatalog(ctx, client, c, s)
	return catalog.Models, err
}

// Discovery never saves a profile, generates content, or activates a provider.
func discoverAIModelCatalog(ctx context.Context, client *http.Client, c connectionRecord, s connectionSecrets) (connectionModelCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows := make([]connectionModel, 0)
	excluded := 0
	seen := map[string]bool{}
	cursors := map[string]bool{}
	cursor := ""
	total := 0
	for page := 0; page < 10; page++ {
		body, e := fetchConnectionModelPage(ctx, client, c, s, cursor)
		if e != nil {
			return connectionModelCatalog{}, e
		}
		total += len(body.Data)
		if total > 1000 {
			return connectionModelCatalog{}, modelError("too_many", 502)
		}
		if len(body.Data) == 0 {
			return connectionModelCatalog{}, modelError("empty", 502)
		}
		for _, m := range body.Data {
			label := strings.TrimSpace(m.Name)
			if label == "" {
				label = m.ID
			}
			if !utf8.ValidString(label) || len(label) > 400 || strings.IndexFunc(label, unicode.IsControl) >= 0 {
				return connectionModelCatalog{}, modelError("response_invalid", 502)
			}
			if s.Token != "" && (strings.Contains(m.ID, s.Token) || strings.Contains(label, s.Token)) {
				return connectionModelCatalog{}, modelError("response_invalid", 502)
			}
			if !aiModelRe.MatchString(m.ID) {
				excluded++
				continue
			}
			if !seen[m.ID] {
				rows = append(rows, connectionModel{m.ID, label})
				seen[m.ID] = true
			}
		}
		if !body.More {
			if len(rows) == 0 {
				return connectionModelCatalog{}, modelError("empty", 502)
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
			return connectionModelCatalog{Models: rows, ExcludedCount: excluded}, nil
		}
		if c.Provider != "anthropic" || !aiModelRe.MatchString(body.Last) || !seen[body.Last] || cursors[body.Last] {
			return connectionModelCatalog{}, modelError("incomplete", 502)
		}
		cursor = body.Last
		cursors[cursor] = true
	}
	return connectionModelCatalog{}, modelError("incomplete", 502)
}
func connectionModelFailure(err error) (string, int) {
	var e *connectionModelError
	if errors.As(err, &e) {
		return e.Code, e.Status
	}
	if errors.Is(err, errConnectionConflict) {
		return "conflict", 409
	}
	return "invalid", 422
}
