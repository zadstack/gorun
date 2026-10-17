package httpx

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestJSONAndErrorEnvelopes(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, 200, []int{1}, Meta{Total: 1, Page: 1, PerPage: 20})
	var ok struct {
		OK   bool  `json:"ok"`
		Data []int `json:"data"`
		Meta Meta  `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil || !ok.OK || ok.Meta.Total != 1 || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("%s %v", rec.Body, err)
	}
	rec = httptest.NewRecorder()
	JSON(rec, 200, map[string]int{"a": 1}, nil)
	if got := rec.Body.String(); got != "{\"ok\":true,\"data\":{\"a\":1}}\n" {
		t.Fatalf("meta must be omitted when nil: %s", got)
	}
	rec = httptest.NewRecorder()
	Error(rec, 422, "validation_failed", "bad", map[string]string{"email": "required"})
	var e struct {
		OK    bool `json:"ok"`
		Error struct {
			Code, Message string
			Fields        map[string]string
		} `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != 422 || e.OK || e.Error.Code != "validation_failed" || e.Error.Fields["email"] != "required" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
