package builder

import (
	"bytes"
	"html/template"
	"strings"
)

var renderer = template.Must(template.New("builder").Funcs(template.FuncMap{"hasText": func(value string) bool { return strings.TrimSpace(value) != "" }}).Parse(`<div class="gp-builder">{{$hasH1 := .HasHeading}}{{range .Sections}}{{if not .Hidden}}{{$section := .}}<section id="gp-{{.ID}}" class="gp-section gp-{{.Kind}} gp-{{if .Variant}}{{.Variant}}{{else}}default{{end}}{{if eq .Feed "posts"}} gp-feed-posts{{end}}"><div class="gp-inner">{{if .HeroScene}}<div class="gp-hero-scene gp-scene-{{.HeroScene}}" aria-hidden="true"><div class="gp-scene-fallback"><span></span><span></span><span></span></div></div>{{end}}<div class="gp-heading">{{if .Image}}<img class="gp-image" src="{{.Image}}" alt="{{.ImageAlt}}" loading="lazy">{{end}}{{if hasText .Title}}{{if not $hasH1}}<h1>{{.Title}}</h1>{{$hasH1 = true}}{{else}}<h2>{{.Title}}</h2>{{end}}{{end}}{{if .Text}}<p class="gp-text">{{.Text}}</p>{{end}}{{if .Link}}<a class="gp-button" href="{{.Link}}">{{.LinkLabel}}</a>{{end}}</div>{{if .Items}}<div class="gp-grid">{{range .Items}}{{if eq $section.Kind "faq"}}<details class="gp-entry"><summary>{{.Title}}</summary><p class="gp-text">{{.Text}}</p></details>{{else}}<article class="gp-entry">{{if .Image}}<img class="gp-image" src="{{.Image}}" alt="{{.ImageAlt}}" loading="lazy">{{end}}{{if .Title}}<h3>{{.Title}}</h3>{{end}}{{if .Price}}<p class="gp-price">{{.Price}}</p>{{end}}{{if .Text}}<p class="gp-text">{{.Text}}</p>{{end}}{{if .Link}}<a href="{{.Link}}">{{.Title}}</a>{{end}}</article>{{end}}{{end}}</div>{{end}}</div></section>{{end}}{{end}}</div>`))

type renderData struct {
	Sections   []Section
	HasHeading bool
}

func Render(doc Document) (string, error) {
	if err := Validate(doc); err != nil {
		return "", err
	}
	for _, s := range doc.Sections {
		if s.Library == SectionLibrary {
			return renderMixed(doc)
		}
	}
	var b bytes.Buffer
	if err := renderer.Execute(&b, renderData{Sections: doc.Sections}); err != nil {
		return "", err
	}
	return b.String(), nil
}
