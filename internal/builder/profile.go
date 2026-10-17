package builder

import (
	"context"
	"errors"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"net/mail"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type starterProfile struct {
	Description string   `json:"description,omitempty"`
	About       string   `json:"about,omitempty"`
	Services    []string `json:"services,omitempty"`
	Email       string   `json:"email,omitempty"`
	Phone       string   `json:"phone,omitempty"`
	Address     string   `json:"address,omitempty"`
	Logo        string   `json:"logo,omitempty"`
	HeroImage   string   `json:"hero_image,omitempty"`
	HeroURL     string   `json:"hero_url,omitempty"`
	HeroAlt     string   `json:"hero_alt,omitempty"`
}

var ErrProfile = errors.New("builder: invalid site profile")

type profileFieldError struct{ field string }

func (e profileFieldError) Error() string { return "builder: invalid profile field " + e.field }
func (e profileFieldError) Unwrap() error { return ErrProfile }

func captureProfile(ctx context.Context, d *kernel.Deps, f starterForm) (starterProfile, error) {
	p := starterProfile{Description: strings.TrimSpace(f.Description), About: strings.TrimSpace(f.About), Email: strings.TrimSpace(f.ContactEmail), Phone: strings.TrimSpace(f.ContactPhone), Address: strings.TrimSpace(f.ContactAddress), Logo: strings.TrimSpace(f.Logo), HeroImage: strings.TrimSpace(f.HeroImage)}
	for _, field := range []struct {
		name, value string
		limit       int
	}{{"description", p.Description, 1600}, {"about", p.About, 6000}, {"contact_address", p.Address, 800}} {
		if utf8.RuneCountInString(field.value) > field.limit || len(field.value) > 12000 || strings.ContainsFunc(field.value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) {
			return p, profileFieldError{field.name}
		}
	}
	if p.Email != "" {
		addr, err := mail.ParseAddress(p.Email)
		if err != nil || addr.Address != p.Email || addr.Name != "" || len(p.Email) > 254 || strings.ContainsAny(p.Email, "\r\n") {
			return p, profileFieldError{"contact_email"}
		}
	}
	digits := 0
	for _, r := range p.Phone {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	if p.Phone != "" && (digits < 6 || len(p.Phone) > 32 || !profilePhone.MatchString(p.Phone)) {
		return p, profileFieldError{"contact_phone"}
	}
	for _, line := range strings.Split(strings.ReplaceAll(f.Services, "\r\n", "\n"), "\n") {
		value := strings.TrimSpace(line)
		if value == "" {
			continue
		}
		if utf8.RuneCountInString(value) > 120 || strings.ContainsFunc(value, unicode.IsControl) {
			return p, profileFieldError{"services"}
		}
		p.Services = append(p.Services, value)
	}
	if len(p.Services) > 0 && len(p.Services) > 6 {
		return p, profileFieldError{"services"}
	}
	for _, selected := range []struct{ field, id string }{{"logo", p.Logo}, {"hero_image", p.HeroImage}} {
		if selected.id == "" {
			continue
		}
		item, err := profileImage(ctx, d, selected.field, selected.id)
		if err != nil {
			return p, err
		}
		if selected.field == "hero_image" {
			p.HeroURL = item.URL
			p.HeroAlt = item.Alt
			if strings.TrimSpace(p.HeroAlt) == "" {
				p.HeroAlt = strings.TrimSpace(f.Title)
			}
		}
	}
	return p, nil
}

var profilePhone = regexp.MustCompile(`^\+?[0-9][0-9 ().-]{5,31}$`)

func profileImage(ctx context.Context, d *kernel.Deps, field, id string) (*media.Media, error) {
	if len(id) != 26 {
		return nil, profileFieldError{field}
	}
	ms, ok := kernel.Get[media.Service](d, kernel.ServiceMedia)
	if !ok {
		return nil, profileFieldError{field}
	}
	item, err := ms.Get(ctx, id)
	if errors.Is(err, media.ErrNotFound) {
		return nil, profileFieldError{field}
	}
	if err != nil {
		return nil, err
	}
	if item == nil || item.URL == "" || !strings.HasPrefix(item.Mime, "image/") || safeURL(item.URL, true) != nil || len(item.Alt) > 12000 {
		return nil, profileFieldError{field}
	}
	return item, nil
}
func profileMessage(ctx context.Context, err error) string {
	var field profileFieldError
	if !errors.As(err, &field) {
		return ""
	}
	ar := i18n.Locale(ctx) == "ar"
	switch field.field {
	case "description":
		if ar {
			return "اكتب وصفًا لا يزيد عن 1600 حرف."
		}
		return "Keep the description within 1600 characters."
	case "about":
		if ar {
			return "اكتب نبذة لا تزيد عن 6000 حرف وحجمها لا يزيد عن 12 كيلوبايت."
		}
		return "Keep the about text within 6000 characters and 12 kilobytes."
	case "services":
		if ar {
			return "اكتب من خدمة أو موضوع واحد إلى 6 خدمات أو موضوعات، كل منها في سطر لا يزيد عن 120 حرفًا."
		}
		return "Enter 1 to 6 services or topics, one per line, up to 120 characters each."
	case "contact_email":
		if ar {
			return "اكتب عنوان بريد إلكتروني صحيحًا بدون اسم إضافي."
		}
		return "Enter a valid email address without a display name."
	case "contact_phone":
		if ar {
			return "اكتب رقم هاتف فيه 6 أرقام على الأقل وبحد أقصى 32 خانة، مع علامة + والمسافات والأقواس."
		}
		return "Enter a phone number with at least 6 digits and up to 32 characters, using +, spaces and parentheses."
	case "contact_address":
		if ar {
			return "اكتب عنوانًا لا يزيد عن 800 حرف."
		}
		return "Keep the address within 800 characters."
	default:
		if ar {
			return "اختر صورة موجودة من مكتبة الوسائط للشعار والصورة الرئيسية."
		}
		return "Choose an existing image from the media library for the logo and hero image."
	}
}
func profilePages(pages []starterPage, p starterProfile, kind, locale string) []starterPage {
	out := make([]starterPage, len(pages))
	copy(out, pages)
	for i, page := range out {
		sections := make([]Section, len(page.Document.Sections))
		copy(sections, page.Document.Sections)
		aboutApplied := false
		for j, section := range sections {
			if section.Kind == "hero" {
				if page.Slug == "home" && p.Description != "" {
					section.Text = p.Description
				}
				if page.Slug == "home" && p.HeroURL != "" {
					section.Image = p.HeroURL
					section.ImageAlt = p.HeroAlt
				}
				if page.Slug == "about" && p.Description != "" {
					section.Text = p.Description
				}
			}
			if p.About != "" && section.Kind == "text" && section.Feed == "" && (page.Slug == "about" || page.Slug == "home") && !aboutApplied {
				section.Text = p.About
				aboutApplied = true
			}
			if len(p.Services) > 0 && (section.Kind == "services" || (kind == "magazine" && section.Kind == "features")) {
				section.Items = make([]Entry, len(p.Services))
				for k, title := range p.Services {
					section.Items[k] = Entry{Title: title}
				}
				section.Text = "Explore what we offer."
				if locale == "ar" {
					section.Text = "تعرّف على ما نقدمه."
				}
			}
			if section.Kind == "contact" && (p.Email != "" || p.Phone != "" || p.Address != "") {
				contact := []string{}
				for _, v := range []string{p.Email, p.Phone, p.Address} {
					if v != "" {
						contact = append(contact, v)
					}
				}
				section.Text = strings.Join(contact, "\n")
				if p.Email != "" {
					section.Link = "mailto:" + p.Email
					section.LinkLabel = p.Email
				}
			}
			sections[j] = section
		}
		page.Document.Sections = sections
		out[i] = page
	}
	return out
}
