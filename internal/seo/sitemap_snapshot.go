package seo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/content"
)

const sitemapMaxBytes = 50 * 1024 * 1024
const sitemapRefreshInterval = 15 * time.Minute

var ErrSitemapPartNotFound = errors.New("seo: sitemap part not found")
var ErrSitemapChanged = errors.New("seo: sitemap changed during regeneration")

type xmlURL struct {
	XMLName xml.Name `xml:"url"`
	Loc     string   `xml:"loc"`
	LastMod string   `xml:"lastmod,omitempty"`
}
type xmlURLSet struct {
	XMLName xml.Name `xml:"urlset"`
	NS      string   `xml:"xmlns,attr"`
	URLs    []xmlURL `xml:"url"`
}
type xmlSitemapPart struct {
	Loc string `xml:"loc"`
}
type xmlSitemapIndex struct {
	XMLName xml.Name         `xml:"sitemapindex"`
	NS      string           `xml:"xmlns,attr"`
	Parts   []xmlSitemapPart `xml:"sitemap"`
}

type sitemapStamp struct {
	revision int64
	hooks    uint64
	scope    string
}
type sitemapSnapshot struct {
	stamp    sitemapStamp
	root     []byte
	parts    map[string][]byte
	modified time.Time
}

func sitemapScope(ctx context.Context) (context.Context, string) {
	excluded := content.PublicExclusions(ctx)
	slices.Sort(excluded)
	return content.WithPublicExclusions(context.Background(), excluded), strings.Join(excluded, ",")
}

func (s *service) stamp(ctx context.Context) (sitemapStamp, error) {
	_, scope := sitemapScope(ctx)
	stamp := sitemapStamp{scope: scope}
	if s.hooks != nil {
		stamp.hooks = s.hooks.Revision()
	}
	if s.sitemapRevision != nil {
		revision, err := s.sitemapRevision(ctx)
		if err != nil {
			return stamp, err
		}
		stamp.revision = revision
	}
	return stamp, nil
}

// SitemapDocument never serves a snapshot invalidated by a committed write.
// Cold/invalidated callers wait for a shared background build and may cancel
// their wait without cancelling the build for other callers.
func (s *service) SitemapDocument(ctx context.Context, part string) (SitemapDocument, error) {
	snapshot, err := s.sitemapSnapshot(ctx)
	if err != nil {
		return SitemapDocument{}, err
	}
	body := snapshot.root
	if part != "" {
		var ok bool
		body, ok = snapshot.parts[part]
		if !ok {
			return SitemapDocument{}, ErrSitemapPartNotFound
		}
	}
	return SitemapDocument{XML: slices.Clone(body), ETag: fmt.Sprintf(`"%x"`, sha256.Sum256(body)), Modified: snapshot.modified}, nil
}

func (s *service) sitemapSnapshot(ctx context.Context) (*sitemapSnapshot, error) {
	stamp, err := s.stamp(ctx)
	if err != nil {
		return nil, err
	}
	s.sitemapMu.Lock()
	cached := s.snapshot
	s.sitemapMu.Unlock()
	if s.sitemapRevision != nil && cached != nil && cached.stamp == stamp && time.Since(cached.modified) < sitemapRefreshInterval {
		return cached, nil
	}
	buildCtx, _ := sitemapScope(ctx)
	if s.sitemapContext != nil {
		buildCtx = content.WithPublicExclusions(s.sitemapContext, content.PublicExclusions(ctx))
	}
	flight := s.sitemapFlight.DoChan("sitemap:"+stamp.scope, func() (any, error) {
		buildCtx, cancel := context.WithTimeout(buildCtx, 3*time.Minute)
		defer cancel()
		return s.regenerateSitemap(buildCtx, stamp)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-flight:
		if result.Err != nil {
			return nil, result.Err
		}
		built := result.Val.(*sitemapSnapshot)
		current, err := s.stamp(ctx)
		if err != nil {
			return nil, err
		}
		if built.stamp != current {
			return nil, ErrSitemapChanged
		}
		return built, nil
	}
}

func (s *service) regenerateSitemap(ctx context.Context, stamp sitemapStamp) (*sitemapSnapshot, error) {
	// Another waiter may have completed a build between the cache check and DoChan.
	s.sitemapMu.Lock()
	cached := s.snapshot
	s.sitemapMu.Unlock()
	if s.sitemapRevision != nil && cached != nil && cached.stamp == stamp && time.Since(cached.modified) < sitemapRefreshInterval {
		return cached, nil
	}
	started := time.Now()
	entries, err := s.Entries(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := encodeSitemap(s.base(ctx), entries)
	if err != nil {
		return nil, err
	}
	current, err := s.stamp(ctx)
	if err != nil {
		return nil, err
	}
	if current != stamp {
		return nil, ErrSitemapChanged
	}
	snapshot.stamp = stamp
	snapshot.modified = time.Now().UTC()
	s.sitemapMu.Lock()
	s.snapshot = snapshot
	s.sitemapMu.Unlock()
	s.log.Info("seo: sitemap regenerated", "entries", len(entries), "parts", max(1, len(snapshot.parts)), "duration_ms", time.Since(started).Milliseconds())
	return snapshot, nil
}

func encodeSitemap(base string, entries []SitemapEntry) (*sitemapSnapshot, error) {
	const open = xml.Header + `<urlset xmlns="` + sitemapNS + `">` + "\n"
	const close = "</urlset>\n"
	snapshot := &sitemapSnapshot{parts: map[string][]byte{}}
	var part bytes.Buffer
	part.WriteString(open)
	count := 0
	var docs [][]byte
	flush := func() {
		part.WriteString(close)
		docs = append(docs, bytes.Clone(part.Bytes()))
		part.Reset()
		part.WriteString(open)
		count = 0
	}
	var urlBuffer bytes.Buffer
	encoder := xml.NewEncoder(&urlBuffer)
	for _, entry := range entries {
		urlBuffer.Reset()
		if err := encoder.Encode(xmlURL{Loc: entry.Loc, LastMod: entry.LastMod}); err != nil {
			return nil, fmt.Errorf("seo: encode URL: %w", err)
		}
		encoded := urlBuffer.Bytes()
		if len(open)+len(encoded)+1+len(close) > sitemapMaxBytes {
			return nil, errors.New("seo: sitemap URL exceeds byte limit")
		}
		if count == sitemapMaxEntries || part.Len()+len(encoded)+1+len(close) > sitemapMaxBytes {
			flush()
		}
		part.Write(encoded)
		part.WriteByte('\n')
		count++
	}
	flush()
	if len(docs) == 1 {
		snapshot.root = docs[0]
		return snapshot, nil
	}
	if len(docs) > sitemapMaxEntries {
		return nil, errors.New("seo: sitemap index exceeds part limit")
	}
	index := xmlSitemapIndex{NS: sitemapNS}
	for i, body := range docs {
		name := fmt.Sprintf("%d.xml", i+1)
		snapshot.parts[name] = body
		index.Parts = append(index.Parts, xmlSitemapPart{Loc: strings.TrimRight(base, "/") + "/sitemaps/" + name})
	}
	root, err := xml.Marshal(index)
	if err != nil {
		return nil, err
	}
	snapshot.root = append([]byte(xml.Header), root...)
	if len(snapshot.root) > sitemapMaxBytes {
		return nil, errors.New("seo: sitemap index exceeds byte limit")
	}
	return snapshot, nil
}
