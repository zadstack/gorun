package services

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRegistrySetAuditsFieldNamesOnly(t *testing.T) {
	d := testDeps(t)
	r := NewRegistry(d, fakeService{d})
	st, fields, err := r.Set(context.Background(), "fake", map[string]string{"value": "very-private-value"}, nil, "cli")
	if err != nil || len(fields) > 0 || st.State != "configured" {
		t.Fatalf("%+v %v %v", st, fields, err)
	}
	var meta string
	if err := d.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'service.update'`).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(meta, `"keys":["value"]`) || !strings.Contains(meta, `"via":"cli"`) || !strings.Contains(meta, `"service":"fake"`) || strings.Contains(meta, "very-private-value") {
		t.Fatalf("audit meta: %s", meta)
	}
}

func TestRegistrySetUnknownService(t *testing.T) {
	d := testDeps(t)
	_, _, err := NewRegistry(d, fakeService{d}).Set(context.Background(), "nope", nil, nil, "cli")
	if !errors.Is(err, ErrUnknownService) {
		t.Fatalf("%v", err)
	}
}

func TestRegistrySetRollsBackOnFieldErrors(t *testing.T) {
	d := testDeps(t)
	r := NewRegistry(d, fakeService{d})
	_, fields, err := r.Set(context.Background(), "fake", map[string]string{"value": "bad"}, nil, "cli")
	if err != nil || fields["value"] == "" {
		t.Fatalf("%v %v", fields, err)
	}
	if v := d.Settings.GetString(context.Background(), "fake.value", ""); v != "" {
		t.Fatalf("a refused set was kept: %q", v)
	}
	var n int
	d.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'service.update'`).Scan(&n)
	if n != 0 {
		t.Fatal("a refused set was audited")
	}
}

func TestRegistryListAndOff(t *testing.T) {
	d := testDeps(t)
	r := NewRegistry(d, fakeService{d})
	ctx := context.Background()
	if _, _, err := r.Set(ctx, "fake", map[string]string{"value": "x"}, nil, "cli"); err != nil {
		t.Fatal(err)
	}
	if err := r.Off(ctx, "fake", "cli"); err != nil {
		t.Fatal(err)
	}
	list, err := r.List(ctx)
	if err != nil || len(list) != 1 || list[0].State != "off" {
		t.Fatalf("%+v %v", list, err)
	}
}
