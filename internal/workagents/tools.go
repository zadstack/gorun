package workagents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

type toolError string

func (e toolError) Error() string { return string(e) }

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

func toolInput(input string, out any) error {
	dec := json.NewDecoder(strings.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(out) != nil || dec.Decode(&struct{}{}) != io.EOF {
		return toolError("invalid_tool_input")
	}
	return nil
}

// githubStars reads only a bounded public GitHub repository response. The
// endpoint is fixed; callers cannot provide URLs, credentials or write actions.
func (s *service) githubStars(ctx context.Context, input string) (string, error) {
	var in struct {
		Repository string `json:"repository"`
	}
	if e := toolInput(input, &in); e != nil {
		return "", e
	}
	if !repositoryPattern.MatchString(in.Repository) || strings.Contains(in.Repository, "..") {
		return "", toolError("invalid_repository")
	}
	client := s.githubClient
	if client == nil {
		client = httpx.NewSafeClient(false)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+in.Repository, nil)
	if e != nil {
		return "", toolError("github_request_failed")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "GoRun-workagents")
	resp, e := client.Do(req)
	if e != nil {
		return "", toolError("github_upstream_failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		return "", toolError("github_rate_limited")
	}
	if resp.StatusCode != 200 {
		return "", toolError("github_upstream_failed")
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil || len(body) > 1<<20 {
		return "", toolError("github_invalid_response")
	}
	var repository struct {
		FullName string `json:"full_name"`
		Stars    *int   `json:"stargazers_count"`
		Forks    *int   `json:"forks_count"`
		Issues   *int   `json:"open_issues_count"`
		Archived bool   `json:"archived"`
	}
	if json.Unmarshal(body, &repository) != nil || !strings.EqualFold(repository.FullName, in.Repository) || repository.Stars == nil || repository.Forks == nil || repository.Issues == nil || *repository.Stars < 0 || *repository.Forks < 0 || *repository.Issues < 0 {
		return "", toolError("github_invalid_response")
	}
	out, _ := json.Marshal(repository)
	return string(out), nil
}

// contentDraft writes a new sanitized draft only. Published pages, existing
// customer content, arbitrary metadata and author/role selection are excluded.
func (s *service) contentDraft(ctx context.Context, runID, input string) (string, error) {
	if !auth.Can(ctx, "content.create") {
		return "", toolError("content_permission_denied")
	}
	var in struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Locale string `json:"locale"`
	}
	if e := toolInput(input, &in); e != nil {
		return "", e
	}
	if (in.Type != "post" && in.Type != "page") || strings.TrimSpace(in.Title) == "" || len(in.Title) > 300 || len(in.Body) > maxInputBytes || (in.Locale != "" && in.Locale != "ar" && in.Locale != "en") {
		return "", toolError("invalid_tool_input")
	}
	if in.Locale == "" {
		in.Locale = "ar"
	}
	svc, ok := kernel.Get[content.Service](s.d, kernel.ServiceContent)
	if !ok {
		return "", toolError("content_service_unavailable")
	}
	slug := "agent-draft-" + strings.ToLower(runID)
	existing, e := svc.GetBySlug(ctx, in.Type, slug, in.Locale)
	if e != nil && e != content.ErrNotFound {
		return "", toolError("content_read_failed")
	}
	if existing == nil {
		existing, e = svc.Create(ctx, content.Input{Type: in.Type, Slug: slug, Title: in.Title, Body: content.Sanitize(in.Body), AuthorID: owner(ctx), Status: content.Status("draft"), Locale: in.Locale})
		if e != nil {
			return "", toolError("content_draft_failed")
		}
	} else if existing.AuthorID != owner(ctx) || existing.Status != content.Status("draft") {
		return "", toolError("content_draft_conflict")
	}
	out, _ := json.Marshal(map[string]string{"content_id": existing.ID, "type": existing.Type, "status": string(existing.Status), "editor_url": "/admin/content/" + existing.Type + "/" + existing.ID})
	return string(out), nil
}
