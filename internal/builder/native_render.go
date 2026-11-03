package builder

import (
	"bytes"
	"html/template"
	"strings"
	"unicode"
)

type nativeRenderData struct {
	Section
	Options     NativeOptions
	MainHeading bool
}

// Native templates preserve sanitizer-safe markers; dynamic controls are added
// by the theme only after the published body has been sanitized.
var nativeRenderer = template.Must(template.New("native").Funcs(template.FuncMap{
	"comparisonLabel": func(title string) string {
		title = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, title)
		label := []rune(strings.Join(strings.Fields(title), " "))
		if len(label) > 300 {
			label = label[:300]
		}
		return string(label)
	},
	"featurePrimary": func(entries []Entry) int {
		for i, entry := range entries {
			if entry.Featured {
				return i
			}
		}
		return 0
	},
	"searchParam": func(options *NativeSearchOptions) string {
		if options == nil || options.Param == "" {
			return "q"
		}
		return options.Param
	},
	"nativeBackground": func(value string) string {
		if value == "" {
			return ""
		}
		if value == "inherit" {
			return " gp-s-bg"
		}
		return " gp-s-bg gp-s-bg-" + value
	},
}).Parse(`{{define "heading"}}{{if .Title}}{{if .MainHeading}}<h1>{{.Title}}</h1>{{else}}<h2>{{.Title}}</h2>{{end}}{{end}}{{end}}
{{define "actions"}}{{if .Link}}<p class="gp-s-actions"><a class="gp-s-btn" href="{{.Link}}">{{.LinkLabel}}</a>{{if .Options.SecondaryLink}}<a class="gp-s-btn gp-s-btn-ghost" href="{{.Options.SecondaryLink}}">{{.Options.SecondaryLabel}}</a>{{end}}</p>{{else}}{{if .Options.SecondaryLink}}<p class="gp-s-actions"><a class="gp-s-btn gp-s-btn-ghost" href="{{.Options.SecondaryLink}}">{{.Options.SecondaryLabel}}</a></p>{{end}}{{end}}{{end}}
{{define "media"}}{{if or .Image .Options.Illustration}}<div class="gp-s-media"><figure class="gp-s-preview">{{if .Image}}<img src="{{.Image}}" alt="{{.ImageAlt}}" loading="lazy">{{else}}<div class="gp-s-preview-ui"><span class="gp-s-ui-side"></span><span class="gp-s-ui-stat"></span><span class="gp-s-ui-stat"></span><span class="gp-s-ui-chart"></span><span class="gp-s-ui-rows"></span></div>{{end}}{{if .Options.Caption}}<figcaption>{{.Options.Caption}}</figcaption>{{end}}</figure></div>{{end}}{{end}}
{{define "hero"}}<div class="gp-s-copy">{{if .Options.Eyebrow}}<span class="gp-s-eyebrow">{{.Options.Eyebrow}}</span>{{end}}{{template "heading" .}}{{if .Text}}<p class="gp-s-lead">{{.Text}}</p>{{end}}{{template "actions" .}}{{if .Options.Facts}}<dl class="gp-s-facts">{{range .Options.Facts}}<div><dt>{{.Label}}</dt><dd>{{.Value}}</dd></div>{{end}}</dl>{{end}}</div>{{template "media" .}}{{end}}
{{define "features"}}<div class="gp-s-grid">{{range .Items}}<article class="gp-s-card{{if .Featured}} gp-s-card-featured{{end}}">{{if .Icon}}<span class="gp-s-icon gp-s-icon-{{.Icon}}"></span>{{end}}{{if .Image}}<img src="{{.Image}}" alt="{{.ImageAlt}}" loading="lazy">{{end}}{{if .Title}}<h3>{{.Title}}</h3>{{end}}{{if .Text}}<p>{{.Text}}</p>{{end}}{{if .Link}}<a href="{{.Link}}">{{.Title}}</a>{{end}}</article>{{end}}</div>{{end}}

{{define "featureEntry"}}<article class="gp-feature-entry">{{if .Image}}<figure class="gp-feature-media"><img src="{{.Image}}" alt="{{.ImageAlt}}" loading="lazy"></figure>{{end}}<div class="gp-feature-copy">{{if .Icon}}<span class="gp-s-icon gp-s-icon-{{.Icon}}"></span>{{end}}{{if .Title}}<h3>{{if .Link}}<a href="{{.Link}}">{{.Title}}</a>{{else}}{{.Title}}{{end}}</h3>{{end}}{{if .Text}}<p>{{.Text}}</p>{{end}}</div></article>{{end}}
{{define "featureLayouts"}}<div class="gp-native-ui gp-feature-{{if eq .Variant "compact_rows"}}compact-rows{{else if eq .Variant "image_led"}}image-led{{else}}featured-secondary{{end}}">{{if .Items}}{{if eq .Variant "featured_secondary"}}{{$primary := featurePrimary .Items}}<div class="gp-feature-primary">{{range $i,$entry := .Items}}{{if eq $i $primary}}{{template "featureEntry" $entry}}{{end}}{{end}}</div>{{if gt (len .Items) 1}}<div class="gp-feature-secondary">{{range $i,$entry := .Items}}{{if ne $i $primary}}{{template "featureEntry" $entry}}{{end}}{{end}}</div>{{end}}{{else}}{{range .Items}}{{template "featureEntry" .}}{{end}}{{end}}{{else}}{{if .Options.EmptyText}}<p class="gp-native-empty">{{.Options.EmptyText}}</p>{{end}}{{end}}</div>{{end}}
{{define "taskEntry"}}<div class="gp-native-ui gp-task-entry gp-task-{{if eq .Variant "search_first"}}search-first{{else if eq .Variant "service_first"}}service-first{{else}}choice-first{{end}}">{{if .Options.Search}}<div class="gp-task-search gp-task-param-{{searchParam .Options.Search}}"><a class="gp-task-search-target" href="{{.Options.Search.Action}}">{{.Options.Search.Label}}</a>{{if .Options.Search.Placeholder}}<span class="gp-task-search-placeholder">{{.Options.Search.Placeholder}}</span>{{end}}</div>{{end}}{{if .Items}}<ul class="gp-task-choices">{{range .Items}}<li>{{if .Icon}}<span class="gp-s-icon gp-s-icon-{{.Icon}}"></span>{{end}}<a href="{{.Link}}">{{.Title}}</a>{{if .Text}}<p>{{.Text}}</p>{{end}}</li>{{end}}</ul>{{end}}</div>{{end}}
{{define "entryImage"}}{{if .Image}}<img src="{{.Image}}" alt="{{.ImageAlt}}" loading="lazy">{{end}}{{end}}
{{define "steps"}}<ol class="gp-s-steps">{{range .Items}}<li>{{if .Image}}<figure class="gp-s-step-media">{{template "entryImage" .}}</figure>{{end}}{{if .Title}}<h3>{{.Title}}</h3>{{end}}{{if .Text}}<p>{{.Text}}</p>{{end}}</li>{{end}}</ol>{{end}}
{{define "media_text"}}<div class="gp-s-rows{{if eq .Variant "image_start"}} gp-s-rows-image-start{{else if eq .Variant "image_end"}} gp-s-rows-image-end{{end}}">{{range .Items}}<div class="gp-s-row{{if not .Image}} gp-s-row-text{{end}}">{{if .Image}}<figure class="gp-s-row-media">{{template "entryImage" .}}</figure>{{end}}<div class="gp-s-row-copy">{{if .Title}}<h3>{{.Title}}</h3>{{end}}{{if .Text}}<p>{{.Text}}</p>{{end}}</div></div>{{end}}</div>{{end}}
{{define "switcher"}}{{$group := print "gp-switch-" .ID}}<div class="gp-s-switcher">{{range $i, $e := .Items}}<details class="gp-s-switch" name="{{$group}}"{{if eq $i 0}} open{{end}}><summary>{{$e.Title}}</summary><div class="gp-s-switch-panel">{{template "entryImage" $e}}{{if $e.Text}}<p>{{$e.Text}}</p>{{end}}</div></details>{{end}}</div>{{end}}
{{define "comparison"}}<div class="gp-s-compare-wrap" tabindex="0" role="region" aria-label="{{comparisonLabel .Title}}"><table class="gp-s-compare"><thead><tr>{{range .Options.Columns}}<th scope="col">{{.}}</th>{{end}}</tr></thead><tbody>{{range .Options.Rows}}<tr><th scope="row">{{.Label}}</th>{{range .Cells}}<td{{if eq .Status "yes"}} class="gp-s-yes"{{else}}{{if eq .Status "no"}} class="gp-s-no"{{end}}{{end}}>{{.Text}}</td>{{end}}</tr>{{end}}</tbody></table></div>{{end}}
{{define "faq"}}{{range .Items}}<details><summary>{{.Title}}</summary><p>{{.Text}}</p></details>{{end}}{{end}}
<section id="gp-{{.ID}}" class="{{if eq .Kind "hero"}}gp-s-hero gp-s-hero-{{if eq .Variant "split"}}split{{else}}centered{{end}}{{else}}gp-s-band{{if eq .Kind "faq"}} gp-s-faq{{if eq .Variant "split"}} gp-s-faq-cols{{end}}{{end}}{{end}}{{nativeBackground .Options.Background}}">
{{if eq .Kind "hero"}}{{template "hero" .}}{{else}}{{template "heading" .}}{{if .Text}}<p>{{.Text}}</p>{{end}}{{template "actions" .}}{{if eq .Kind "features"}}{{if or (eq .Variant "compact_rows") (eq .Variant "image_led") (eq .Variant "featured_secondary")}}{{template "featureLayouts" .}}{{else}}{{template "features" .}}{{end}}{{end}}{{if eq .Kind "task_entry"}}{{template "taskEntry" .}}{{end}}{{if eq .Kind "steps"}}{{template "steps" .}}{{end}}{{if eq .Kind "comparison"}}{{template "comparison" .}}{{end}}{{if eq .Kind "faq"}}{{template "faq" .}}{{end}}{{if eq .Kind "media_text"}}{{template "media_text" .}}{{end}}{{if eq .Kind "directory"}}<div class="gp-s-directory gp-s-dir-{{.Options.ListType}}{{if eq .Variant "compact"}} gp-s-directory-compact{{end}}"></div>{{end}}{{if eq .Kind "switcher"}}{{template "switcher" .}}{{end}}{{if eq .Kind "product_preview"}}{{template "media" .}}{{end}}{{end}}</section>`))

func renderMixed(doc Document) (string, error) {
	var out bytes.Buffer
	out.WriteString("<div>")
	hasHeading := false
	for i := 0; i < len(doc.Sections); {
		section := doc.Sections[i]
		if section.Library == SectionLibrary {
			i++
			if section.Hidden {
				continue
			}
			data := nativeRenderData{Section: section, MainHeading: !hasHeading && strings.TrimSpace(section.Title) != ""}
			if section.Native != nil {
				data.Options = *section.Native
			}
			// Match legacy behavior: whitespace-only titles do not take the page h1.
			if strings.TrimSpace(data.Title) == "" {
				data.Title = ""
			}
			if err := nativeRenderer.Execute(&out, data); err != nil {
				return "", err
			}
			if data.MainHeading {
				hasHeading = true
			}
			continue
		}
		start := i
		for i < len(doc.Sections) && doc.Sections[i].Library != SectionLibrary {
			i++
		}
		group := doc.Sections[start:i]
		if err := renderer.Execute(&out, renderData{Sections: group, HasHeading: hasHeading}); err != nil {
			return "", err
		}
		for _, s := range group {
			if !s.Hidden && strings.TrimSpace(s.Title) != "" {
				hasHeading = true
			}
		}
	}
	out.WriteString("</div>")
	return out.String(), nil
}
