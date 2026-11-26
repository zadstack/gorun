package services

import (
	"context"
	"testing"
)

func TestAnalyticsToggle(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Analytics(d))

	st, fields, err := r.Set(ctx, "analytics", map[string]string{"enabled": "false"}, nil, "cli")
	if err != nil || len(fields) > 0 {
		t.Fatalf("set enabled=false: %v %v", err, fields)
	}
	if st.State != StateOff {
		t.Fatalf("state = %q, want off", st.State)
	}
	var on bool
	if err := d.Settings.Get(ctx, "analytics.enabled", &on); err != nil || on {
		t.Fatalf("analytics.enabled = %v (%v), want false", on, err)
	}

	st, fields, err = r.Set(ctx, "analytics", map[string]string{"enabled": "true", "exclude": "/private/, /drafts"}, nil, "cli")
	if err != nil || len(fields) > 0 {
		t.Fatalf("set enabled=true: %v %v", err, fields)
	}
	if st.State != StateConfigured {
		t.Fatalf("state = %q, want configured", st.State)
	}
	if got := d.Settings.GetString(ctx, "analytics.exclude", ""); got != "/private/\n/drafts" {
		t.Fatalf("analytics.exclude = %q", got)
	}

	// A partial set keeps the exclusions.
	if _, fields, err = r.Set(ctx, "analytics", map[string]string{"enabled": "false"}, nil, "cli"); err != nil || len(fields) > 0 {
		t.Fatalf("partial set: %v %v", err, fields)
	}
	if got := d.Settings.GetString(ctx, "analytics.exclude", ""); got != "/private/\n/drafts" {
		t.Fatalf("partial set lost exclude: %q", got)
	}

	if _, fields, _ = r.Set(ctx, "analytics", map[string]string{"exclude": "nope"}, nil, "cli"); fields["exclude"] == "" {
		t.Fatalf("exclude without a leading slash accepted: %v", fields)
	}
	if _, fields, _ = r.Set(ctx, "analytics", map[string]string{"enabled": "maybe"}, nil, "cli"); fields["enabled"] == "" {
		t.Fatalf("enabled=maybe accepted: %v", fields)
	}

	if err := r.Off(ctx, "analytics", "cli"); err != nil {
		t.Fatal(err)
	}
	st, _ = Analytics(d).Status(ctx)
	if st.State != StateOff {
		t.Fatalf("after off state = %q", st.State)
	}
}

func TestBackupsRejectsUnknownDestination(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Backups(d))

	_, fields, err := r.Set(ctx, "backups", map[string]string{"enabled": "true", "every_hours": "24", "destination": "0123456789abcdef0123456789abcdef"}, nil, "cli")
	if err != nil {
		t.Fatal(err)
	}
	if fields["destination"] == "" {
		t.Fatalf("unknown destination accepted: %v", fields)
	}
	if got := d.Settings.GetString(ctx, "backup.schedule.remote_destination", ""); got != "" {
		t.Fatalf("remote_destination written: %q", got)
	}
	var hours int
	if err := d.Settings.Get(ctx, "backup.schedule_hours", &hours); err == nil && hours != 0 {
		t.Fatalf("schedule_hours written despite field error: %d", hours)
	}

	for _, bad := range []map[string]string{
		{"every_hours": "0"}, {"every_hours": "169"}, {"retain": "101"}, {"retain": "-1"},
	} {
		if _, fields, _ := r.Set(ctx, "backups", bad, nil, "cli"); len(fields) == 0 {
			t.Fatalf("%v accepted", bad)
		}
	}

	st, fields, err := r.Set(ctx, "backups", map[string]string{"enabled": "true", "every_hours": "12", "retain": "7", "destination": "local"}, nil, "cli")
	if err != nil || len(fields) > 0 {
		t.Fatalf("local schedule: %v %v", err, fields)
	}
	if st.State != StateConfigured || st.Fields["every_hours"] != 12 || st.Fields["retain"] != 7 || st.Fields["destination"] != "local" {
		t.Fatalf("status = %+v", st)
	}

	if err := r.Off(ctx, "backups", "cli"); err != nil {
		t.Fatal(err)
	}
	st, _ = Backups(d).Status(ctx)
	if st.State != StateOff || st.Fields["retain"] != 7 {
		t.Fatalf("after off: %+v", st)
	}
}

func TestBackupsStatusHasLastSuccess(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	if err := d.Settings.Set(ctx, "backup.local.last_success", map[string]any{"at": "2026-11-25T03:00:00Z", "stamp": "20261125-030000", "bytes": 42}); err != nil {
		t.Fatal(err)
	}
	st, err := Backups(d).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Fields["last_success"] != "2026-11-25T03:00:00Z" {
		t.Fatalf("last_success = %v", st.Fields["last_success"])
	}
	if st.SecretSet || Backups(d).Secret("") != "" || Analytics(d).Secret("") != "" {
		t.Fatal("analytics and backups have no secret")
	}
}
