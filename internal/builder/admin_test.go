package builder

import (
	"errors"
	"github.com/zadstack/gorun/internal/kernel"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuilderDecodeRejectsUnknownTrailingAndOversizedJSON(t *testing.T) {
	cases := []string{`{"document":{"version":1,"sections":[]},"base_updated_at":"a"}`, `{"document":{"version":1,"sections":[]},"unknown":true}`, `{} {}`, `{"document":`, strings.Repeat(" ", 1<<20) + `{}`}
	for i, body := range cases {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request saveRequest
			if decodeBuilder(w, r, &request) != nil {
				w.WriteHeader(400)
				return
			}
			builderJSON(w, 200, request)
		})
		server := httptest.NewServer(handler)
		response, err := http.Post(server.URL, "application/json", strings.NewReader(body))
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		expected := 400
		if i == 0 {
			expected = 200
		}
		if response.StatusCode != expected {
			t.Errorf("case %d: got %d want %d", i, response.StatusCode, expected)
		}
		response.Body.Close()
		server.Close()
	}
}

func TestBuilderErrorsExposeConflictAndOwnershipWithoutInternalDetails(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{{ErrConflict, 409}, {ErrForbidden, 403}, {ErrNotFound, 404}, {errors.New("builder: invalid URL secret-internal-marker"), 422}} {
		h := &adminHandlers{d: &kernel.Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
		recorder := httptest.NewRecorder()
		h.fail(recorder, httptest.NewRequest("POST", "/admin/builder/test/save", nil), test.err)
		if recorder.Code != test.status {
			t.Errorf("%v: got %d want %d", test.err, recorder.Code, test.status)
		}
		if strings.Contains(recorder.Body.String(), "secret-internal-marker") {
			t.Fatal("internal details exposed")
		}
	}
}
