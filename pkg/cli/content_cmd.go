package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/seo"
)

type contentRow struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Slug      string `json:"slug"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
	URL       string `json:"url"` // public link of a published, indexable item; "" otherwise
}

type contentList struct {
	Types []contentType `json:"types"`
	Items []contentRow  `json:"items"`
}

type contentType struct {
	Slug string            `json:"slug"`
	Name map[string]string `json:"name"`
}

// contentCmd implements `gorun content list [--type T] [--limit N]`: the
// site's items (all statuses but trash), newest first per type, with the
// public link of published ones (as the sitemap and theme permalinks give).
func contentCmd(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "list" {
		return errors.New("usage: gorun content list [--type T] [--limit N] [--data dir]")
	}
	typ, args, err := stringFlag(args[1:], "type")
	if err != nil {
		return err
	}
	limitStr, args, err := stringFlag(args, "limit")
	if err != nil {
		return err
	}
	limit := 100
	if limitStr != "" {
		if limit, err = strconv.Atoi(limitStr); err != nil || limit < 1 || limit > 500 {
			return errors.New("--limit is 1 to 500")
		}
	}
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	app, err := kernel.BootWithOptions(ctx, cfg, Modules(cfg), kernel.BootOptions{StartJobs: false})
	if err != nil {
		return err
	}
	defer app.Close()
	svc, ok := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	if !ok {
		return errors.New("content: service missing")
	}
	links := map[string]string{}
	if s, ok := kernel.Get[seo.Service](app.Deps, kernel.ServiceSEO); ok {
		if entries, err := s.Entries(ctx); err == nil {
			for _, e := range entries {
				if e.Item != nil {
					links[e.Item.ID] = e.Loc
				}
			}
		}
	}
	types, err := svc.Types(ctx)
	if err != nil {
		return err
	}
	res := contentList{Types: []contentType{}, Items: []contentRow{}}
	for _, t := range types {
		if !t.Public || (typ != "" && t.Slug != typ) {
			continue
		}
		res.Types = append(res.Types, contentType{t.Slug, t.Plural})
		q, err := svc.Query(ctx, content.Query{Type: t.Slug, Page: 1, PerPage: limit, Order: []content.Order{{By: "updated_at", Dir: "desc"}}})
		if err != nil {
			return err
		}
		for _, it := range q.Items {
			if it.Status == "trash" {
				continue
			}
			res.Items = append(res.Items, contentRow{it.ID, it.Type, it.Title, it.Slug, string(it.Status), it.UpdatedAt, links[it.ID]})
		}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}
