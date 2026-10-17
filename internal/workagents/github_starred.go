package workagents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/httpx"
)

const starredPageSize = 100
const starredMaxPages = 3
const starredMaxPageBytes = 2 << 20

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

type starredRepository struct {
	ID          int64  `json:"id"`
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	Language    string `json:"language"`
	Stars       int64  `json:"stargazers_count"`
	HTMLURL     string `json:"html_url"`
}
type starredRecord struct {
	StarredAt string            `json:"starred_at"`
	Repo      starredRepository `json:"repo"`
}
type starredResult struct {
	Username  string          `json:"username"`
	Items     []starredRecord `json:"items"`
	Pages     int             `json:"pages"`
	Truncated bool            `json:"truncated"`
}

// githubStarred reads public stars made by a user, distinct from repository
// star counts. Input cannot supply a URL, token or next-page link. Every page
// stays on the fixed public API host and the request budget is finite.
func (s *service) githubStarred(ctx context.Context, input string) (string, error) {
	var in struct {
		Username string `json:"username"`
	}
	if e := toolInput(input, &in); e != nil {
		return "", e
	}
	if !usernamePattern.MatchString(in.Username) || strings.Contains(in.Username, "--") {
		return "", toolError("invalid_github_username")
	}
	sourceClient := s.githubClient
	if sourceClient == nil {
		sourceClient = httpx.NewSafeClient(false)
	}
	client := *sourceClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result := starredResult{Username: in.Username, Items: []starredRecord{}}
	seen := map[int64]bool{}
	for page := 1; page <= starredMaxPages; page++ {
		url := fmt.Sprintf("https://api.github.com/users/%s/starred?per_page=%d&page=%d&sort=created&direction=desc", in.Username, starredPageSize, page)
		req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
		if e != nil {
			return "", toolError("github_request_failed")
		}
		req.Header.Set("Accept", "application/vnd.github.star+json")
		req.Header.Set("User-Agent", "GoRun-workagents")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, e := client.Do(req)
		if e != nil {
			return "", toolError("github_upstream_failed")
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			if resp.StatusCode == 403 || resp.StatusCode == 429 {
				return "", toolError("github_rate_limited")
			}
			return "", toolError("github_upstream_failed")
		}
		body, e := io.ReadAll(io.LimitReader(resp.Body, starredMaxPageBytes+1))
		resp.Body.Close()
		if e != nil || len(body) > starredMaxPageBytes {
			return "", toolError("github_invalid_response")
		}
		var rows []starredRecord
		if json.Unmarshal(body, &rows) != nil || rows == nil || len(rows) > starredPageSize {
			return "", toolError("github_invalid_response")
		}
		for _, row := range rows {
			if row.Repo.ID <= 0 || !repositoryPattern.MatchString(row.Repo.FullName) || strings.Contains(row.Repo.FullName, "..") || row.Repo.Stars < 0 || len(row.Repo.Description) > 20000 || len(row.Repo.Language) > 100 {
				return "", toolError("github_invalid_response")
			}
			at, e := time.Parse(time.RFC3339, row.StarredAt)
			if e != nil || at.IsZero() {
				return "", toolError("github_invalid_response")
			}
			// Canonical links are derived from validated repository identifiers, never
			// forwarded from an untrusted API body into a rendered artifact.
			row.Repo.HTMLURL = "https://github.com/" + row.Repo.FullName
			if !seen[row.Repo.ID] {
				seen[row.Repo.ID] = true
				result.Items = append(result.Items, row)
			}
		}
		result.Pages = page
		if len(rows) < starredPageSize {
			break
		}
		if page == starredMaxPages {
			result.Truncated = true
		}
	}
	out, e := json.Marshal(result)
	if e != nil || len(out) > maxOutputBytes {
		return "", toolError("github_invalid_response")
	}
	return string(out), nil
}
