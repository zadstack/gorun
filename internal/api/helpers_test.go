package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
)

func TestPagination(t *testing.T) {
	cases := []struct {
		q         string
		page, per int
	}{
		{"", 1, 20},
		{"page=3&per_page=50", 3, 50},
		{"page=0&per_page=-1", 1, 20},
		{"page=x&per_page=y", 1, 20},
		{"per_page=1000", 1, 100},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/x?"+c.q, nil)
		if p, pp := Pagination(r); p != c.page || pp != c.per {
			t.Errorf("%q: got %d/%d want %d/%d", c.q, p, pp, c.page, c.per)
		}
	}
}

func TestSort(t *testing.T) {
	allowed := []string{"created_at", "title"}
	cases := []struct{ q, by, dir string }{
		{"", "created_at", "desc"},
		{"sort=title", "title", "asc"},
		{"sort=title&order=desc", "title", "desc"},
		{"sort=-title", "title", "desc"},
		{"sort=password&order=sideways", "created_at", "desc"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/x?"+c.q, nil)
		if by, dir := Sort(r, allowed); by != c.by || dir != c.dir {
			t.Errorf("%q: got %s %s want %s %s", c.q, by, dir, c.by, c.dir)
		}
	}
	if by, dir := Sort(httptest.NewRequest("GET", "/x?sort=a", nil), nil); by != "" || dir != "desc" {
		t.Errorf("no allowed: %q %q", by, dir)
	}
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %s", w.Body.String())
	}
	return m
}

func TestWriteListNeverNull(t *testing.T) {
	w := httptest.NewRecorder()
	var items []string
	WriteList(w, items, 0, 1, 20)
	if got := strings.TrimSpace(w.Body.String()); got != `{"ok":true,"data":[],"meta":{"total":0,"page":1,"per_page":20}}` {
		t.Fatalf("list: %s", got)
	}
	w = httptest.NewRecorder()
	WriteItem(w, map[string]int{"a": 1})
	if got := strings.TrimSpace(w.Body.String()); got != `{"ok":true,"data":{"a":1}}` {
		t.Fatalf("item: %s", got)
	}
	w = httptest.NewRecorder()
	NotFound(w)
	if w.Code != 404 || decodeBody(t, w)["error"].(map[string]any)["code"] != "not_found" {
		t.Fatalf("not found: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	WriteError(w, 422, "validation_failed", "bad", map[string]string{})
	if strings.Contains(w.Body.String(), "fields") {
		t.Fatalf("empty fields must be omitted: %s", w.Body)
	}
}

type in struct {
	Title string `json:"title"`
	N     int    `json:"n"`
}

func TestDecode(t *testing.T) {
	cases := []struct {
		body, field string
		status      int
	}{
		{`{"title":"x","n":1}`, "", 0},
		{`{"title":`, "", 400},
		{`{"title":"x","extra":1}`, "extra", 400},
		{`{"n":"seven"}`, "n", 400},
		{`{"title":"x"} {"title":"y"}`, "", 400},
		{`{"title":"` + strings.Repeat("a", maxBody) + `"}`, "", 413},
	}
	for _, c := range cases {
		var v in
		err := Decode(httptest.NewRecorder(), httptest.NewRequest("POST", "/x", strings.NewReader(c.body)), &v)
		status, fields := ParseValidation(err)
		if status != c.status {
			t.Errorf("%.30q: status %d want %d (%v)", c.body, status, c.status, err)
		}
		if c.field != "" && fields[c.field] == "" {
			t.Errorf("%.30q: want field hint for %s, got %v", c.body, c.field, fields)
		}
	}
}

// contentLikeErr has content.ValidationError's shape (its Error method is
// added by the content module in a parallel branch).
type contentLikeErr struct{ Fields content.FieldErrors }

func (e *contentLikeErr) Error() string { return "invalid" }

func TestDecodeEmptyBody(t *testing.T) {
	nilBody := httptest.NewRequest("POST", "/x", nil)
	nilBody.Body = nil
	for name, r := range map[string]*http.Request{
		"nil":   nilBody,
		"empty": httptest.NewRequest("POST", "/x", strings.NewReader("")),
	} {
		var v in
		err := Decode(httptest.NewRecorder(), r, &v)
		var de *DecodeError
		if !errors.As(err, &de) || de.Status != 400 || de.Code != "empty_body" {
			t.Errorf("%s body: %v", name, err)
		}
	}
}

type fieldErr struct{}

func (fieldErr) Error() string                  { return "bad" }
func (fieldErr) FieldErrors() map[string]string { return map[string]string{"title": "required"} }

func TestParseValidation(t *testing.T) {
	cases := []struct {
		err    error
		status int
		field  string
	}{
		{nil, 0, ""},
		{errors.New("boom"), 0, ""},
		{fieldErr{}, 422, "title"},
		{fmt.Errorf("wrap: %w", fieldErr{}), 422, "title"},
		{&contentLikeErr{Fields: content.FieldErrors{"slug": "taken"}}, 422, "slug"},
		{fmt.Errorf("x: %w", &auth.ValidationError{Fields: map[string]string{"email": "invalid"}}), 422, "email"},
	}
	for i, c := range cases {
		status, fields := ParseValidation(c.err)
		if status != c.status || (c.field != "" && fields[c.field] == "") {
			t.Errorf("case %d: %d %v", i, status, fields)
		}
	}
}

func TestFail(t *testing.T) {
	logger.Store(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { logger.Store(nil) })
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{fieldErr{}, 422, "validation_failed"},
		{fmt.Errorf("get: %w", content.ErrNotFound), 404, "not_found"},
		{auth.ErrNotFound, 404, "not_found"},
		{errors.New("secret db detail"), 500, "internal_error"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		Fail(w, httptest.NewRequest("GET", "/x", nil), c.err)
		body := decodeBody(t, w)
		if w.Code != c.status || body["error"].(map[string]any)["code"] != c.code || strings.Contains(w.Body.String(), "secret") {
			t.Errorf("%v: %d %s", c.err, w.Code, w.Body)
		}
	}
	_ = http.StatusOK
}
