package wpmigrate

import (
	"crypto/sha256"
	"encoding/binary"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Export attachment catalogs omit inline-only images and responsive variants.
// Synthetic negative IDs keep these source identities stable across re-exports
// without colliding with WordPress's positive attachment IDs.
func (s *wxrSource) inlineImages(catalog []WPMedia) []WPMedia {
	known := map[string]bool{}
	for _, attachment := range catalog {
		known[mediaExactKey(attachment.URL)] = true
	}
	base, err := url.Parse(firstNonEmpty(s.f.BaseBlogURL, s.f.Link, s.f.BaseSiteURL))
	if err != nil {
		return nil
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	var images []WPMedia
	for _, item := range s.f.Items {
		for _, ref := range inlineImageReferences(item.Content) {
			candidate, err := url.Parse(strings.TrimSpace(ref))
			if err != nil {
				continue
			}
			candidate = base.ResolveReference(candidate)
			if candidate.Host == "" || candidate.User != nil || (candidate.Scheme != "http" && candidate.Scheme != "https") {
				continue
			}
			candidate.Fragment, candidate.RawFragment = "", ""
			if catalogHasImage(known, candidate) {
				continue
			}
			key := mediaExactKey(candidate.String())
			digest := sha256.Sum256([]byte(key))
			id := -int(binary.BigEndian.Uint64(digest[:8])>>1) - 1
			images = append(images, WPMedia{ID: id, URL: candidate.String()})
			known[key] = true
		}
	}
	return images
}

func catalogHasImage(known map[string]bool, candidate *url.URL) bool {
	for _, variant := range mediaCandidates(candidate) {
		queryless := *variant
		queryless.RawQuery, queryless.ForceQuery = "", false
		if known[mediaExactKey(variant.String())] || known[mediaExactKey(queryless.String())] {
			return true
		}
	}
	return false
}

func inlineImageReferences(body string) []string {
	z := html.NewTokenizer(strings.NewReader(body))
	var refs []string
	for {
		switch z.Next() {
		case html.ErrorToken:
			return refs
		case html.StartTagToken, html.SelfClosingTagToken:
			tag := z.Token()
			if tag.Data != "img" && tag.Data != "source" {
				continue
			}
			for _, attr := range tag.Attr {
				switch attr.Key {
				case "src", "data-src":
					if tag.Data == "img" {
						refs = append(refs, attr.Val)
					}
				case "srcset":
					refs = append(refs, srcsetReferences(attr.Val)...)
				}
			}
		}
	}
}

func srcsetReferences(value string) []string {
	var refs []string
	for _, span := range srcsetURLSpans(value) {
		refs = append(refs, value[span[0]:span[1]])
	}
	return refs
}

// URL tokens may contain commas (notably data URLs); only trailing commas
// or commas after a descriptor separate candidates.
func srcsetURLSpans(value string) [][2]int {
	var spans [][2]int
	for pos := 0; pos < len(value); {
		for pos < len(value) && (srcsetSpace(value[pos]) || value[pos] == ',') {
			pos++
		}
		start := pos
		for pos < len(value) && !srcsetSpace(value[pos]) {
			pos++
		}
		end := pos
		for end > start && value[end-1] == ',' {
			end--
		}
		if end > start {
			spans = append(spans, [2]int{start, end})
		}
		if end < pos {
			continue
		}
		for pos < len(value) && value[pos] != ',' {
			pos++
		}
	}
	return spans
}

func srcsetSpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == '\f'
}
