package wpmigrate

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProgressRemainsRunningUntilValidationFinishes(t *testing.T) {
	for _, pause := range []bool{false, true} {
		name := "finishes"
		if pause {
			name = "pause_during_validation"
		}
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			id := e.wxrMigration(Options{})
			e.run(id)
			if _, err := e.svc.db.Exec(`UPDATE migrations SET status='running',current_step='validate',finished_at=NULL WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
			migration, err := e.svc.getMigration(e.ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var arrived, unblocked sync.Once
			unblock := func() { unblocked.Do(func() { close(release) }) }
			defer unblock()
			e.svc.check = func(context.Context, string) int {
				arrived.Do(func() { close(entered) })
				<-release
				return http.StatusOK
			}
			done := make(chan error, 1)
			go func() { done <- e.svc.finish(e.ctx(), migration) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("validation did not start")
			}
			status, err := e.svc.Status(e.ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			if status.Status != StatusRunning {
				t.Errorf("validation still in flight but status=%s; browser will stop polling", status.Status)
			}
			b := e.browser()
			resp, html := b.do("GET", "/admin/x/migrations/"+id+"/progress", nil, "", "HX-Request", "true")
			if resp.StatusCode != 200 || !strings.Contains(html, `data-status="running"`) || !strings.Contains(html, `hx-trigger="every 2s"`) {
				t.Error("progress stopped polling before validation finished")
			}
			if pause {
				if err := e.svc.Pause(e.ctx(), id); err != nil {
					t.Error("cannot pause active validation", err)
				}
			}
			unblock()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			status, err = e.svc.Status(e.ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			expected := StatusValidated
			if pause {
				expected = StatusPaused
			}
			if status.Status != expected {
				t.Fatalf("final status=%s, want %s", status.Status, expected)
			}
			_, html = b.do("GET", "/admin/x/migrations/"+id+"/progress", nil, "", "HX-Request", "true")
			if strings.Contains(html, `hx-trigger="every 2s"`) {
				t.Fatal("terminal or paused progress still polls")
			}
			if !pause {
				if _, err := e.svc.report(e.ctx(), id); err != nil {
					t.Fatal("terminal validation lacks stored report", err)
				}
			}
		})
	}
}
