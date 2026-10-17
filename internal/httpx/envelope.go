// Package httpx holds shared HTTP plumbing: middleware, the API response
// envelope, form binding/validation, the SSRF-safe client and the admin
// template renderer.
package httpx

import (
	"encoding/json"
	"net/http"
)

// Meta is the pagination block of a list response.
type Meta struct {
	Total   int `json:"total"`
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
}

type okEnvelope struct {
	OK   bool `json:"ok"`
	Data any  `json:"data"`
	Meta any  `json:"meta,omitempty"`
}

type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fields  any    `json:"fields,omitempty"`
}

type errEnvelope struct {
	OK    bool    `json:"ok"`
	Error errBody `json:"error"`
}

// JSON writes {"ok":true,"data":data,"meta":meta}; meta is omitted when nil.
func JSON(w http.ResponseWriter, status int, data, meta any) {
	writeJSON(w, status, okEnvelope{OK: true, Data: data, Meta: meta})
}

// Error writes {"ok":false,"error":{"code","message","fields"}}; fields is
// omitted when nil.
func Error(w http.ResponseWriter, status int, code, msg string, fields any) {
	writeJSON(w, status, errEnvelope{Error: errBody{Code: code, Message: msg, Fields: fields}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
