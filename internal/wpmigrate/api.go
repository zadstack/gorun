package wpmigrate

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// createMigrationRequest is the body of POST /v1/migrations.
type createMigrationRequest struct {
	SourceID string  `json:"source_id"`
	Options  Options `json:"options"`
}

// registerAPI mirrors the wizard under /v1/migrations (scope
// migrations.run).
func registerAPI(d *kernel.Deps, s *service) {
	tags, scopes := []string{"migrations"}, []string{Permission}
	id := func(r *http.Request) string { return chi.URLParam(r, "id") }
	add := func(method, path, summary string, req, resp any, h http.HandlerFunc) {
		d.API.Add(api.Op(method, path, summary, tags, scopes, req, resp, h))
	}
	add("POST", "/v1/migrations/sources", "Create a WordPress source (REST URL + optional application password)", SourceInput{}, SourceView{},
		func(w http.ResponseWriter, r *http.Request) {
			var in SourceInput
			if err := api.Decode(w, r, &in); err != nil {
				api.Fail(w, r, err)
				return
			}
			v, err := s.CreateSource(r.Context(), in)
			if err != nil {
				apiFail(w, r, err)
				return
			}
			httpx.JSON(w, http.StatusCreated, v, nil)
		})
	add("GET", "/v1/migrations/sources", "List WordPress sources", nil, []SourceView{}, func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Sources(r.Context())
		if err != nil {
			apiFail(w, r, err)
			return
		}
		api.WriteList(w, list, len(list), 1, len(list))
	})
	add("POST", "/v1/migrations/sources/{id}/discover", "Scan a source and return the Discovery Report", nil, Report{},
		func(w http.ResponseWriter, r *http.Request) {
			rep, err := s.Discover(r.Context(), id(r))
			if errors.Is(err, ErrAuthFailed) {
				httpx.Error(w, http.StatusUnprocessableEntity, "auth_failed", "WordPress rejected the application password", nil)
				return
			}
			if err != nil {
				apiFail(w, r, err)
				return
			}
			api.WriteItem(w, rep)
		})
	add("POST", "/v1/migrations", "Create a migration of a source", createMigrationRequest{}, MigrationView{},
		func(w http.ResponseWriter, r *http.Request) {
			var in createMigrationRequest
			if err := api.Decode(w, r, &in); err != nil {
				api.Fail(w, r, err)
				return
			}
			createdBy := ""
			if u := auth.UserFromContext(r.Context()); u != nil {
				createdBy = u.ID
			}
			v, err := s.CreateMigration(r.Context(), in.SourceID, in.Options, createdBy)
			if err != nil {
				apiFail(w, r, err)
				return
			}
			httpx.JSON(w, http.StatusCreated, v, nil)
		})
	add("GET", "/v1/migrations", "List migrations", nil, []MigrationView{}, func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Migrations(r.Context())
		if err != nil {
			apiFail(w, r, err)
			return
		}
		api.WriteList(w, list, len(list), 1, len(list))
	})
	add("GET", "/v1/migrations/{id}", "Migration status and progress counts", nil, Status{}, func(w http.ResponseWriter, r *http.Request) {
		st, err := s.Status(r.Context(), id(r))
		if err != nil {
			apiFail(w, r, err)
			return
		}
		api.WriteItem(w, st)
	})
	add("POST", "/v1/migrations/{id}/discover", "Scan the migration's source", nil, Report{}, func(w http.ResponseWriter, r *http.Request) {
		m, err := s.getMigration(r.Context(), id(r))
		if err != nil {
			apiFail(w, r, err)
			return
		}
		rep, err := s.Discover(r.Context(), m.SourceID)
		if errors.Is(err, ErrAuthFailed) {
			httpx.Error(w, http.StatusUnprocessableEntity, "auth_failed", "WordPress rejected the application password", nil)
			return
		}
		if err != nil {
			apiFail(w, r, err)
			return
		}
		api.WriteItem(w, rep)
	})
	add("GET", "/v1/migrations/{id}/mapping", "Current type/taxonomy/field mapping (defaults from the report)", nil, Mapping{},
		func(w http.ResponseWriter, r *http.Request) {
			m, err := s.getMigration(r.Context(), id(r))
			if err != nil {
				apiFail(w, r, err)
				return
			}
			api.WriteItem(w, m.Mapping.normalized(m.Discovery))
		})
	add("PUT", "/v1/migrations/{id}/mapping", "Save the mapping", Mapping{}, Mapping{}, func(w http.ResponseWriter, r *http.Request) {
		var mp Mapping
		if err := api.Decode(w, r, &mp); err != nil {
			api.Fail(w, r, err)
			return
		}
		if err := s.SaveMapping(r.Context(), id(r), mp); err != nil {
			apiFail(w, r, err)
			return
		}
		api.WriteItem(w, mp)
	})
	add("POST", "/v1/migrations/{id}/preview", "Dry run: state/date plan without content or media writes", nil, PreviewResult{},
		func(w http.ResponseWriter, r *http.Request) {
			res, err := s.Preview(r.Context(), id(r))
			if err != nil {
				apiFail(w, r, err)
				return
			}
			api.WriteItem(w, res)
		})
	for _, action := range []struct {
		name, summary string
		fn            func(*service) func(r *http.Request) error
	}{
		{"start", "Start (or re-run) the import", func(s *service) func(r *http.Request) error {
			return func(r *http.Request) error { return s.Start(r.Context(), id(r)) }
		}},
		{"pause", "Pause the import", func(s *service) func(r *http.Request) error {
			return func(r *http.Request) error { return s.Pause(r.Context(), id(r)) }
		}},
		{"resume", "Resume a paused import", func(s *service) func(r *http.Request) error {
			return func(r *http.Request) error { return s.Resume(r.Context(), id(r)) }
		}},
		{"retry", "Retry failed items", func(s *service) func(r *http.Request) error {
			return func(r *http.Request) error { return s.RetryFailed(r.Context(), id(r)) }
		}},
	} {
		run := action.fn(s)
		add("POST", "/v1/migrations/{id}/"+action.name, action.summary, nil, Status{}, func(w http.ResponseWriter, r *http.Request) {
			if err := run(r); err != nil {
				apiFail(w, r, err)
				return
			}
			st, err := s.Status(r.Context(), id(r))
			if err != nil {
				apiFail(w, r, err)
				return
			}
			api.WriteItem(w, st)
		})
	}
	logOp := api.Op("GET", "/v1/migrations/{id}/log", "Migration log (newest window, oldest first)", tags, scopes, nil, []LogEntry{},
		func(w http.ResponseWriter, r *http.Request) {
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			list, err := s.Log(r.Context(), id(r), limit)
			if err != nil {
				apiFail(w, r, err)
				return
			}
			api.WriteList(w, list, len(list), 1, len(list))
		})
	logOp.Params = append(logOp.Params, kernel.APIParam{Name: "limit", In: "query", Type: "integer", Description: "max entries (default 100, max 1000)"})
	d.API.Add(logOp)
	add("POST", "/v1/migrations/{id}/validate", "Run validation (counts, render checks, broken links, shortcodes)", nil, ValidateReport{},
		func(w http.ResponseWriter, r *http.Request) {
			rep, err := s.Validate(r.Context(), id(r))
			if err != nil {
				apiFail(w, r, err)
				return
			}
			api.WriteItem(w, rep)
		})
	add("GET", "/v1/migrations/{id}/report", "Last validation report (includes Markdown)", nil, ValidateReport{},
		func(w http.ResponseWriter, r *http.Request) {
			rep, err := s.report(r.Context(), id(r))
			if err != nil {
				apiFail(w, r, err)
				return
			}
			api.WriteItem(w, rep)
		})
}

// apiFail maps migrator errors to the envelope.
func apiFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		api.NotFound(w)
	case errors.Is(err, ErrBadState):
		httpx.Error(w, http.StatusConflict, "conflict", err.Error(), nil)
	case errors.Is(err, ErrInvalidSource):
		httpx.Error(w, http.StatusUnprocessableEntity, api.CodeValidation, err.Error(), nil)
	case errors.Is(err, ErrAuthFailed):
		httpx.Error(w, http.StatusUnprocessableEntity, "auth_failed", "WordPress rejected the application password", nil)
	case errors.Is(err, ErrAuthUnavailable):
		httpx.Error(w, http.StatusUnprocessableEntity, "auth_unavailable", "WordPress denied access to the authenticated profile; no public fallback was attempted", nil)
	case errors.Is(err, ErrUnreachable), errors.Is(err, ErrNotWordPress):
		httpx.Error(w, http.StatusBadGateway, "site_unreachable", "the WordPress site could not be read", nil)
	default:
		api.Fail(w, r, err)
	}
}
