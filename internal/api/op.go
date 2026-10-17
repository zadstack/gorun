package api

import (
	"net/http"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
)

// Op builds a kernel.APIOperation. path may be given with or without the
// /v1 prefix ("/content/{id}" and "/v1/content/{id}" are equivalent; chi
// patterns, including {id:regex}, are allowed). req and resp are example
// values whose types become the OpenAPI request body and the envelope's
// data schema (nil for none; a slice for list operations). Every scope in
// scopes is enforced with auth.Can before h runs. Add query parameters to
// the returned value's Params (see PageParams).
func Op(method, path, summary string, tags, scopes []string, req, resp any, h http.HandlerFunc) kernel.APIOperation {
	return kernel.APIOperation{
		Method: strings.ToUpper(method), Path: normalizePath(path), Summary: summary,
		Tags: tags, Scopes: scopes, Request: req, Response: resp, Handler: h,
	}
}
