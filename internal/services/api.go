package services

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/kernel"
)

// permTest is the permission of a service's safe test (the admin's
// capability tests use the same one).
const permTest = "system.capability_test"

// permBackup is the permission of a service's run action (backups).
const permBackup = "system.backup"

// patchRequest is the body of PATCH /site/services/{name}.
type patchRequest struct {
	Values map[string]any `json:"values" doc:"Field name → new value (string, number or boolean). Only the fields given change; a secret is refused here"`
}

var nameParam = []kernel.APIParam{{Name: "name", In: "path", Type: "string", Required: true, Description: "mail, storage, chat, analytics, backups or metricool"}}

var serviceErrors = []kernel.APIErrorResponse{
	{Status: 404, Code: api.CodeNotFound, Description: "No service has this name"},
	{Status: 422, Code: api.CodeValidation, Description: "A field is invalid, or a secret was sent as a value; nothing was saved"},
}

// registerAPI adds the REST operations over reg: the same registry and
// rules as gorun service and the MCP toolset, audited via api.
func registerAPI(d *kernel.Deps, reg *Registry) {
	h := apiHandlers{reg: reg}
	tags := []string{"services"}
	manage := []string{permManage}
	list := api.Op("GET", "/site/services", "Every service (mail, storage, chat, analytics, backups, metricool) and its status; never a secret, only secret_set", tags, manage, nil, []Status{}, h.list)
	get := api.Op("GET", "/site/services/{name}", "One service's status, without its secret", tags, manage, nil, Status{}, h.get)
	patch := api.Op("PATCH", "/site/services/{name}", "Change only the given fields of a service; a secret is never accepted here: when one is missing the answer carries needs_secret (a one-time admin link and the CLI line)", tags, manage, patchRequest{}, SetResult{}, h.patch)
	off := api.Op("DELETE", "/site/services/{name}", "Turn a service off", tags, manage, nil, Status{}, h.off)
	test := api.Op("POST", "/site/services/{name}/test", "Run the service's safe test and record it as last_test: mail sends one test message to the caller's own address, storage does a put, read and delete round trip on each bound bucket, metricool reads the brand list", tags, []string{permTest}, nil, TestResult{}, h.test)
	run := api.Op("POST", "/site/services/{name}/run", "Take the service's action now: backups takes a verified backup, uploads it to the configured destination and keeps retain pairs. A failed upload answers ok false (the local backup stays)", tags, []string{permBackup}, nil, RunResult{}, h.run)
	for _, op := range []*kernel.APIOperation{&get, &patch, &off, &test, &run} {
		op.Params = nameParam
		op.Contract = &kernel.APIContract{Errors: serviceErrors}
	}
	get.Contract = &kernel.APIContract{Errors: serviceErrors[:1]}
	off.Contract = &kernel.APIContract{Errors: serviceErrors[:1]}
	run.Contract = &kernel.APIContract{Errors: []kernel.APIErrorResponse{serviceErrors[0],
		{Status: 409, Code: "operation_in_progress", Description: "The data directory is busy (an update, a restore or another backup)"},
		{Status: 422, Code: api.CodeValidation, Description: "The service has no run action"}}}
	for _, op := range []kernel.APIOperation{list, get, patch, off, test, run} {
		d.API.Add(op)
	}
}

type apiHandlers struct{ reg *Registry }

// service is the {name} service, or a 404 written.
func (h apiHandlers) service(w http.ResponseWriter, r *http.Request) (Service, bool) {
	s, ok := h.reg.Get(chi.URLParam(r, "name"))
	if !ok {
		api.NotFound(w)
	}
	return s, ok
}

func (h apiHandlers) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.reg.List(r.Context())
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, list)
}

func (h apiHandlers) get(w http.ResponseWriter, r *http.Request) {
	s, ok := h.service(w, r)
	if !ok {
		return
	}
	st, err := h.reg.Status(r.Context(), s.Name())
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, st)
}

func (h apiHandlers) patch(w http.ResponseWriter, r *http.Request) {
	s, ok := h.service(w, r)
	if !ok {
		return
	}
	var in patchRequest
	if err := api.Decode(w, r, &in); err != nil {
		api.Fail(w, r, err)
		return
	}
	values, bad := StringValues(in.Values)
	if len(bad) > 0 {
		api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, "invalid values; nothing was saved", bad)
		return
	}
	res, fields, err := h.reg.SetRemote(r.Context(), s.Name(), values, "api")
	switch {
	case err != nil:
		api.Fail(w, r, err)
	case len(fields) > 0:
		api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, "invalid values; nothing was saved", fields)
	default:
		api.WriteItem(w, res)
	}
}

func (h apiHandlers) off(w http.ResponseWriter, r *http.Request) {
	s, ok := h.service(w, r)
	if !ok {
		return
	}
	if err := h.reg.Off(r.Context(), s.Name(), "api"); err != nil {
		api.Fail(w, r, err)
		return
	}
	h.get(w, r)
}

func (h apiHandlers) test(w http.ResponseWriter, r *http.Request) {
	s, ok := h.service(w, r)
	if !ok {
		return
	}
	res, err := h.reg.TestFor(r.Context(), s.Name())
	var te *TestError
	switch {
	case errors.As(ClassifyTestError(err), &te):
		api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, te.Error(), map[string]string{te.Field: te.Key})
	case err != nil:
		api.Fail(w, r, err)
	default:
		api.WriteItem(w, res)
	}
}

func (h apiHandlers) run(w http.ResponseWriter, r *http.Request) {
	s, ok := h.service(w, r)
	if !ok {
		return
	}
	res, err := h.reg.Run(r.Context(), s.Name(), "api")
	switch {
	case errors.Is(err, ErrNoRun):
		api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, err.Error(), map[string]string{"name": errNoRunKey})
	case errors.Is(err, ErrBackupBusy):
		api.WriteError(w, http.StatusConflict, "operation_in_progress", err.Error(), nil)
	case err != nil:
		api.Fail(w, r, err)
	default:
		api.WriteItem(w, res)
	}
}
