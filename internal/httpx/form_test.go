package httpx

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/i18n"
)

type signup struct {
	Email    string   `form:"email" validate:"required,email"`
	Password string   `form:"password" validate:"required,min=10"`
	Age      int      `form:"age"`
	Admin    bool     `form:"admin"`
	Tags     []string `form:"tags"`
	Role     string   `form:"role" validate:"oneof=admin editor"`
}

func TestBindFormAndJSON(t *testing.T) {
	form := url.Values{"email": {"a@b.co"}, "password": {"x"}, "age": {"7"}, "admin": {"on"}, "tags": {"a", "b"}, "role": {"editor"}}
	r := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var s signup
	if err := Bind(r, &s); err != nil {
		t.Fatal(err)
	}
	if s.Email != "a@b.co" || s.Age != 7 || !s.Admin || len(s.Tags) != 2 || s.Role != "editor" {
		t.Fatalf("%+v", s)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader(`{"email":"j@x.io","age":3}`))
	r.Header.Set("Content-Type", "application/json")
	var j struct {
		Email string `json:"email"`
		Age   int    `json:"age"`
	}
	if err := Bind(r, &j); err != nil || j.Email != "j@x.io" || j.Age != 3 {
		t.Fatalf("%+v %v", j, err)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader(`age=notanumber`))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := Bind(r, &s); err == nil {
		t.Fatal("want error for bad int")
	}
}

func TestValidateTranslatesErrors(t *testing.T) {
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := i18n.WithLocale(context.Background(), "en")
	errs := Validate(ctx, b, &signup{Email: "nope", Password: "short", Role: "root"})
	if len(errs) != 3 || errs["email"] == "" || !strings.Contains(errs["password"], "10") || errs["role"] == "" {
		t.Fatalf("%v", errs)
	}
	if errs := Validate(ctx, b, &signup{Email: "a@b.co", Password: "0123456789", Role: "admin"}); len(errs) != 0 {
		t.Fatalf("%v", errs)
	}
	if errs := Validate(ctx, b, &signup{}); errs["email"] == "" || errs["password"] == "" {
		t.Fatalf("required: %v", errs)
	}
}
