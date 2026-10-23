package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"net/url"
	"path"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

const (
	maxUploadBytes  = 10 << 20
	maxUploadBase64 = (maxUploadBytes + 2) / 3 * 4
)

type uploadMediaInput struct {
	Filename   string `json:"filename,omitempty" jsonschema:"required with data_base64, e.g. hero.png"`
	DataBase64 string `json:"data_base64,omitempty" jsonschema:"file bytes, standard base64, at most 10 MB decoded"`
	URL        string `json:"url,omitempty" jsonschema:"public http(s) URL to download instead of data_base64"`
	Alt        string `json:"alt,omitempty"`
	Caption    string `json:"caption,omitempty"`
	Title      string `json:"title,omitempty"`
}

type listMediaInput struct {
	Search  string `json:"search,omitempty"`
	Page    int    `json:"page,omitempty"`
	PerPage int    `json:"per_page,omitempty"`
}

type mediaIDInput struct {
	ID string `json:"id"`
}

type updateMediaInput struct {
	ID      string  `json:"id"`
	Alt     *string `json:"alt,omitempty"`
	Caption *string `json:"caption,omitempty"`
	Title   *string `json:"title,omitempty"`
	// Focal sets the focal point (x, y from 0 to 1); clear_focal removes it.
	Focal      *media.Focal `json:"focal,omitempty"`
	ClearFocal bool         `json:"clear_focal,omitempty"`
}

func mediaTools(svc media.Service, log *slog.Logger) []kernel.MCPTool {
	return []kernel.MCPTool{
		{Name: "get_media", Scopes: []string{scopeView}, Input: mediaIDInput{},
			Description: "Read a media item, including dimensions and generated responsive image variants.",
			Handler:     bind(func(ctx context.Context, in mediaIDInput) (any, error) { return svc.Get(ctx, in.ID) })},
		{Name: "update_media", Sensitive: true, Scopes: []string{scopeUpload}, Input: updateMediaInput{},
			Description: "Update alt text, caption, title or focal point (x, y from 0 to 1; clear_focal removes it). Omitted or null fields are preserved; an empty string clears a field. Requires approval because metadata may appear on public pages.",
			Handler: bind(func(ctx context.Context, in updateMediaInput) (any, error) {
				if in.Alt == nil && in.Caption == nil && in.Title == nil && in.Focal == nil && !in.ClearFocal {
					return nil, invalid("provide alt, caption, title, focal or clear_focal", nil)
				}
				if in.Focal != nil && !in.Focal.Valid() {
					return nil, invalid("focal x and y must be between 0 and 1", nil)
				}
				return svc.Update(ctx, in.ID, media.Patch{Alt: in.Alt, Caption: in.Caption, Title: in.Title, Focal: in.Focal, ClearFocal: in.ClearFocal})
			})},
		{Name: "upload_media", Scopes: []string{scopeUpload}, Input: uploadMediaInput{},
			Description: "Upload a file from base64 data (≤ 10 MB) or download it from a public URL (private addresses are refused).",
			Handler:     bind(func(ctx context.Context, in uploadMediaInput) (any, error) { return uploadMedia(ctx, svc, log, in) })},
		{Name: "list_media", Scopes: []string{scopeView}, Input: listMediaInput{},
			Description: "List the media library (newest first).",
			Handler: bind(func(ctx context.Context, in listMediaInput) (any, error) {
				page, per := paging(in.Page, in.PerPage)
				items, total, err := svc.List(ctx, media.ListQuery{Search: in.Search, Page: page, PerPage: per})
				if err != nil {
					return nil, err
				}
				return newPage(items, total, page, per), nil
			})},
	}
}

func uploadMedia(ctx context.Context, svc media.Service, log *slog.Logger, in uploadMediaInput) (any, error) {
	meta := media.UploadMeta{Alt: in.Alt, Caption: in.Caption, Title: in.Title, AuthorID: actingUserID(ctx)}
	switch {
	case in.DataBase64 != "" && in.URL != "":
		return nil, invalid("give data_base64 or url, not both", map[string]string{"url": "exclusive"})
	case in.URL != "":
		u, err := url.Parse(in.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, invalid("url must be an absolute http(s) URL", map[string]string{"url": "invalid"})
		}
		m, err := svc.FromURL(ctx, in.URL, meta)
		if err != nil {
			if _, known := toToolError(err); known {
				return nil, err
			}
			log.Info("mcp: upload_media url fetch failed", "err", err)
			return nil, &ToolError{Code: "fetch_failed",
				Message: "could not download the URL (unreachable, private address, refused redirect or non-200 response)"}
		}
		return m, nil
	case in.DataBase64 != "":
		if len(in.DataBase64) > maxUploadBase64 {
			return nil, invalid("file exceeds 10 MB", map[string]string{"data_base64": "too_large"})
		}
		name := path.Base(strings.ReplaceAll(strings.TrimSpace(in.Filename), "\\", "/"))
		if name == "" || name == "." || name == "/" {
			return nil, invalid("filename is required with data_base64", map[string]string{"filename": "required"})
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(in.DataBase64))
		if err != nil {
			return nil, invalid("data_base64 is not valid base64", map[string]string{"data_base64": "invalid"})
		}
		if len(data) > maxUploadBytes {
			return nil, invalid("file exceeds 10 MB", map[string]string{"data_base64": "too_large"})
		}
		return svc.Upload(ctx, name, bytes.NewReader(data), meta)
	}
	return nil, invalid("give data_base64 (with filename) or url", map[string]string{"data_base64": "required"})
}
