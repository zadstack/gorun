package membership

import (
	"context"
	"net/http"
	"strings"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

type SubmissionInput struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	Body  string `json:"body"`
}
type Submission struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func (s *Service) Submit(ctx context.Context, native content.Service, q SubmissionInput) (Submission, error) {
	out := Submission{}
	id := actor(ctx)
	if id == "" || !auth.Can(ctx, "membership.submit") {
		return out, content.ErrForbidden
	}
	if strings.TrimSpace(q.Title) == "" || len(q.Title) > 500 || len(q.Body) > 128<<10 {
		return out, ErrInvalid
	}
	var n int
	if e := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM membership_policies WHERE content_type=?`, q.Type).Scan(&n); e != nil {
		return out, e
	}
	if n != 1 {
		return out, content.ErrForbidden
	}
	// This trusted entry point permits only a new pending submission owned by the
	// actor. No caller-supplied author, status, arbitrary fields or publish action.
	it, e := native.Create(ctx, content.Input{Type: q.Type, Title: q.Title, Body: q.Body, AuthorID: id, Status: "pending"})
	if e != nil {
		return out, e
	}
	return Submission{ID: it.ID, Type: it.Type, Title: it.Title, Body: it.Body, Status: string(it.Status), CreatedAt: it.CreatedAt}, nil
}
func (s *Service) Submissions(ctx context.Context) ([]Submission, error) {
	id := actor(ctx)
	if id == "" {
		return nil, content.ErrForbidden
	}
	if auth.ActorFromContext(ctx).Kind == "api_key" && !auth.Can(ctx, "membership.read") {
		return nil, content.ErrForbidden
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT c.id,c.type,c.title,c.body,c.status,c.created_at FROM content c JOIN membership_policies p ON p.content_type=c.type WHERE c.author_id=? AND c.status!='trash' ORDER BY c.created_at DESC,c.id LIMIT 100`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Submission{}
	for rows.Next() {
		var v Submission
		if e = rows.Scan(&v.ID, &v.Type, &v.Title, &v.Body, &v.Status, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func registerSubmissions(d *kernel.Deps, s *Service) {
	native, _ := kernel.Get[content.Service](d, kernel.ServiceContent)
	d.Perms.Declare("membership.submit", map[string]string{"en": "Submit own content within membership policy", "ar": "إرسال محتوى مملوك ضمن حصة العضوية"})
	d.API.Add(api.Op("GET", "/v1/membership/submissions", "Read own submissions", []string{"membership"}, []string{"membership.read"}, nil, []Submission{}, func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Submissions(r.Context())
		if e != nil {
			fail(w, r, e)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		api.WriteItem(w, v)
	}))
	d.API.Add(api.Op("POST", "/v1/membership/submissions", "Submit owned pending content within configured membership quota", []string{"membership"}, []string{"membership.submit"}, SubmissionInput{}, Submission{}, func(w http.ResponseWriter, r *http.Request) {
		var q SubmissionInput
		if e := api.Decode(w, r, &q); e != nil {
			api.Fail(w, r, e)
			return
		}
		v, e := s.Submit(r.Context(), native, q)
		if e != nil {
			fail(w, r, e)
			return
		}
		api.WriteItem(w, v)
	}))
	d.Router.Public.With(auth.CSRFMiddleware()).Post("/membership/submissions", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
		if e := r.ParseForm(); e != nil {
			fail(w, r, ErrInvalid)
			return
		}
		v, e := s.Submit(r.Context(), native, SubmissionInput{Type: r.Form.Get("type"), Title: r.Form.Get("title"), Body: r.Form.Get("body")})
		if e != nil {
			fail(w, r, e)
			return
		}
		http.Redirect(w, r, "/membership#submission-"+v.ID, 303)
	})
}
