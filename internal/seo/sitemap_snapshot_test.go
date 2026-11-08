package seo

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/zadstack/gorun/internal/content"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSitemapPartsPreserveEveryURL(t *testing.T) {
	entries := make([]SitemapEntry, 50001)
	for i := range entries {
		entries[i].Loc = fmt.Sprintf("https://example.test/post/%d", i)
	}
	snapshot, err := encodeSitemap("https://example.test", entries)
	if err != nil {
		t.Fatal(err)
	}
	var index struct {
		Parts []struct {
			Loc string `xml:"loc"`
		} `xml:"sitemap"`
	}
	if err := xml.Unmarshal(snapshot.root, &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Parts) != 2 {
		t.Fatalf("parts: %d", len(index.Parts))
	}
	count := 0
	for _, part := range snapshot.parts {
		var set xmlURLSet
		if err := xml.Unmarshal(part, &set); err != nil {
			t.Fatal(err)
		}
		if len(set.URLs) > 50000 {
			t.Fatalf("oversized part: %d", len(set.URLs))
		}
		count += len(set.URLs)
	}
	if count != len(entries) {
		t.Fatalf("lost URLs: %d of %d", count, len(entries))
	}
}

func TestSitemapSnapshotReusesAndInvalidatesGeneration(t *testing.T) {
	s := testService(t)
	revision := int64(1)
	s.sitemapRevision = func(context.Context) (int64, error) { return revision, nil }
	fc := fakeContent{types: []content.Type{{Slug: "post", Public: true}}, items: []content.Item{{ID: "1", Type: "post", Slug: "one", Status: "published"}}}
	s.content = func() (content.Service, bool) { return fc, true }
	calls := 0
	s.hooks.Filter(FilterSitemapEntry, 0, func(_ context.Context, v any) (any, error) { calls++; return v, nil })
	first, err := s.Sitemap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Sitemap(context.Background())
	if err != nil || string(first) != string(second) || calls != 1 {
		t.Fatalf("unexpected cached response: calls=%d err=%v", calls, err)
	}
	set(t, s, SettingSiteBaseURL, "https://changed.example")
	revision++
	third, err := s.Sitemap(context.Background())
	if err != nil || !strings.Contains(string(third), "https://changed.example/") {
		t.Fatalf("stale snapshot: %s %v", third, err)
	}
	s.hooks.Filter(FilterSitemapEntry, 20, func(_ context.Context, v any) (any, error) {
		v.(*SitemapEntry).Loc = "/new-permalink"
		return v, nil
	})
	fourth, err := s.Sitemap(context.Background())
	if err != nil || !strings.Contains(string(fourth), "/new-permalink") {
		t.Fatalf("old filter chain retained: %s %v", fourth, err)
	}
}

func TestSitemapCancelledWaiterDoesNotCancelSharedGeneration(t *testing.T) {
	s := testService(t)
	s.sitemapRevision = func(context.Context) (int64, error) { return 1, nil }
	fc := fakeContent{types: []content.Type{{Slug: "post", Public: true}}, items: []content.Item{{ID: "1", Type: "post", Slug: "shared", Status: "published"}}}
	s.content = func() (content.Service, bool) { return fc, true }
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.hooks.Filter(FilterSitemapEntry, 0, func(_ context.Context, v any) (any, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return v, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := s.Sitemap(ctx); first <- err }()
	<-started
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		body, err := s.Sitemap(context.Background())
		if err == nil && !strings.Contains(string(body), "/shared") {
			err = fmt.Errorf("missing shared URL")
		}
		done <- err
	}()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate generation: %d", calls.Load())
	}
}

func TestSitemapRejectsGenerationChangedDuringBuild(t *testing.T) {
	s := testService(t)
	var revision atomic.Int64
	s.sitemapRevision = func(context.Context) (int64, error) { return revision.Load(), nil }
	fc := fakeContent{types: []content.Type{{Slug: "post", Public: true}}, items: []content.Item{{ID: "1", Type: "post", Slug: "changed", Status: "published"}}}
	s.content = func() (content.Service, bool) { return fc, true }
	token := s.hooks.Filter(FilterSitemapEntry, 0, func(_ context.Context, v any) (any, error) { revision.Add(1); return v, nil })
	if _, err := s.Sitemap(context.Background()); !errors.Is(err, ErrSitemapChanged) {
		t.Fatalf("published inconsistent generation: %v", err)
	}
	s.hooks.Remove(token)
	if _, err := s.Sitemap(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkSitemapSnapshots(b *testing.B) {
	for _, cached := range []bool{false, true} {
		name := "cold"
		if cached {
			name = "cached"
		}
		b.Run(name, func(b *testing.B) {
			s := testService(b)
			var revision atomic.Int64
			s.sitemapRevision = func(context.Context) (int64, error) { return revision.Load(), nil }
			fc := benchmarkSitemapContent{}
			for i := range 8000 {
				fc.items = append(fc.items, content.Item{ID: fmt.Sprint(i), Type: "post", Slug: fmt.Sprintf("benchmark-entry-%06d", i), Status: "published"})
			}
			s.content = func() (content.Service, bool) { return fc, true }
			body, err := s.Sitemap(context.Background())
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if !cached {
					revision.Add(1)
				}
				if _, err := s.Sitemap(context.Background()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type benchmarkSitemapContent struct {
	content.Service
	items []content.Item
}

func (f benchmarkSitemapContent) Types(context.Context) ([]content.Type, error) {
	return []content.Type{{Slug: "post", Public: true}}, nil
}
func (f benchmarkSitemapContent) Query(_ context.Context, q content.Query) (content.Page[content.Item], error) {
	start := min((q.Page-1)*q.PerPage, len(f.items))
	end := min(start+q.PerPage, len(f.items))
	return content.Page[content.Item]{Items: f.items[start:end], Total: len(f.items)}, nil
}

func TestSitemapFailedFilterCannotPublishFallbackSnapshot(t *testing.T) {
	s := testService(t)
	s.sitemapRevision = func(context.Context) (int64, error) { return 1, nil }
	fc := fakeContent{types: []content.Type{{Slug: "post", Public: true}}, items: []content.Item{{ID: "1", Type: "post", Slug: "public", Status: "published"}}}
	s.content = func() (content.Service, bool) { return fc, true }
	if _, err := s.Sitemap(context.Background()); err != nil {
		t.Fatal(err)
	}
	unavailable := errors.New("permalink provider unavailable")
	token := s.hooks.Filter(FilterSitemapEntry, 20, func(context.Context, any) (any, error) { return nil, unavailable })
	if _, err := s.Sitemap(context.Background()); !errors.Is(err, unavailable) {
		t.Fatalf("fallback published after filter failure: %v", err)
	}
	s.hooks.Remove(token)
	if _, err := s.Sitemap(context.Background()); err != nil {
		t.Fatal(err)
	}
}
