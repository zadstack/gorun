package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

const subprocessFixtureMarker = "jobs-owned-subprocess-fixture"

type subprocessPayload struct {
	Key string
}

func subprocessQueue(t *testing.T, root string) *Queue {
	t.Helper()
	d, err := db.Open(filepath.Join(root, "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.Migrate(context.Background(), d, "jobs", Migrations()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS audit_invocations (key TEXT NOT NULL, mode TEXT NOT NULL, pid INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS audit_effects (key TEXT PRIMARY KEY, pid INTEGER NOT NULL, at_utc TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS audit_ticks (n INTEGER PRIMARY KEY, at_utc TEXT NOT NULL)`,
	} {
		if _, err := d.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	q := New(d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.pollInterval, q.tickInterval = 5*time.Millisecond, 10*time.Millisecond
	// Timestamps have second precision, so staleAfter must exceed the
	// heartbeat by more than a second for live jobs to never look stale.
	q.heartbeat, q.staleAfter = 20*time.Millisecond, 1100*time.Millisecond
	return q
}

func ownedSubprocessFixture(t *testing.T) (string, *Queue) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".owned"), []byte(subprocessFixtureMarker), 0600); err != nil {
		t.Fatal(err)
	}
	return root, subprocessQueue(t, root)
}

func startJobsSubprocess(t *testing.T, root, mode string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestJobsSubprocessHelper$", "-test.v", "-test.timeout=10s")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.Getenv("TMPDIR"), "TZ=UTC", "AUDIT_JOBS_PROCESS_MODE=" + mode, "AUDIT_JOBS_PROCESS_ROOT=" + root}
	output := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
			if err := cmd.Wait(); err != nil {
				t.Log("bounded helper cleanup:", err)
			}
		}
		cancel()
	})
	return cmd, output
}

func TestQueueRecoversAfterOSProcessInterruption(t *testing.T) {
	root, q := ownedSubprocessFixture(t)
	ctx := context.Background()
	first, err := q.Enqueue(ctx, "subprocess_fixture", subprocessPayload{Key: "interrupted"}, Idempotency("interrupted"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := q.Enqueue(ctx, "subprocess_fixture", subprocessPayload{Key: "queued"}, Idempotency("queued"), RunAt(time.Now().UTC().Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	crashed, crashOutput := startJobsSubprocess(t, root, "interrupt")
	ready := filepath.Join(root, "running.ready")
	deadline := time.Now().Add(4 * time.Second)
	for {
		_, err := os.Stat(ready)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not reach the running handler")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if j := getJob(t, q, first); j.Status != "running" || j.LockedBy == "" || j.Attempts != 0 {
		t.Fatalf("job was not actually running: %+v", j)
	}
	if j := getJob(t, q, second); j.Status != "queued" {
		t.Fatalf("queued sibling changed before interruption: %+v", j)
	}
	if err := crashed.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := crashed.Wait(); err == nil {
		t.Fatal("interrupted process exited normally")
	}
	if getJob(t, q, first).Status != "running" {
		t.Fatal("process exited gracefully instead of leaving a stale running job")
	}
	restarted, output := startJobsSubprocess(t, root, "recover")
	if err := restarted.Wait(); err != nil {
		t.Fatalf("restart helper: %v\n%s", err, output.String())
	}
	if crashed.Process.Pid == restarted.Process.Pid || restarted.Process.Pid == os.Getpid() {
		t.Fatal("restart did not use a separate OS process")
	}
	for _, id := range []string{first, second} {
		if j := getJob(t, q, id); j.Status != "done" || j.Attempts != 0 || j.LockedBy != "" {
			t.Fatalf("job lost or not completed after restart: %+v", j)
		}
	}
	var completed, interruptedCalls, recoveredCalls int
	if err := q.db.QueryRow(`SELECT count(*) FROM audit_effects`).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if err := q.db.QueryRow(`SELECT count(*) FROM audit_invocations WHERE mode='interrupt'`).Scan(&interruptedCalls); err != nil {
		t.Fatal(err)
	}
	if err := q.db.QueryRow(`SELECT count(*) FROM audit_invocations WHERE mode='recover'`).Scan(&recoveredCalls); err != nil {
		t.Fatal(err)
	}
	if completed != 2 || interruptedCalls != 1 || recoveredCalls != 2 {
		t.Fatalf("unexpected execution counts: effects=%d interrupted=%d recovered=%d", completed, interruptedCalls, recoveredCalls)
	}
	t.Logf("OS interruption PID=%d, recovery PID=%d: stale running + queued jobs both done, one completion each; interrupted handler replayed (not general exactly-once side effects)\n%s%s", crashed.Process.Pid, restarted.Process.Pid, crashOutput.String(), output.String())
}

func TestQueueCronRunsInOSProcessWithoutHTTP(t *testing.T) {
	root, q := ownedSubprocessFixture(t)
	child, output := startJobsSubprocess(t, root, "cron")
	if err := child.Wait(); err != nil {
		t.Fatalf("cron helper: %v\n%s", err, output.String())
	}
	if child.Process.Pid == os.Getpid() {
		t.Fatal("cron ran in the parent process")
	}
	list, err := q.List(context.Background(), "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("cron lost or duplicated jobs: %+v", list)
	}
	for _, j := range list {
		if j.Status != "done" || j.Attempts != 0 || !strings.HasSuffix(j.RunAt, "Z") || j.LockedBy != "" {
			t.Fatalf("cron job not successfully completed in UTC: %+v", j)
		}
	}
	rows, err := q.db.Query(`SELECT pid,at_utc FROM audit_effects ORDER BY at_utc`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	completed := 0
	for rows.Next() {
		var pid int
		var at string
		if err := rows.Scan(&pid, &at); err != nil {
			t.Fatal(err)
		}
		_, err := time.Parse(time.RFC3339Nano, at)
		if err != nil || !strings.HasSuffix(at, "Z") || pid != child.Process.Pid {
			t.Fatalf("invalid cron execution identity/time: pid=%d at=%q error=%v", pid, at, err)
		}
		completed++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var first, last string
	var ticks int
	if err := q.db.QueryRow(`SELECT count(*),min(at_utc),max(at_utc) FROM audit_ticks`).Scan(&ticks, &first, &last); err != nil {
		t.Fatal(err)
	}
	from, fromErr := time.Parse(time.RFC3339Nano, first)
	to, toErr := time.Parse(time.RFC3339Nano, last)
	if completed != 3 || ticks != 3 || fromErr != nil || toErr != nil || to.Sub(from) < 60*time.Millisecond || !strings.HasSuffix(first, "Z") || !strings.HasSuffix(last, "Z") {
		t.Fatalf("cron did not enqueue across real UTC intervals: ticks=%d completed=%d first=%s last=%s", ticks, completed, first, last)
	}
	t.Logf("PID=%d: real scheduler ticks queued and completed 3 UTC jobs without HTTP/browser/provider; test tick=10ms, cron interval=40ms (production tick remains30s)\n%s", child.Process.Pid, output.String())
}

func TestJobsSubprocessHelper(t *testing.T) {
	mode, root := os.Getenv("AUDIT_JOBS_PROCESS_MODE"), os.Getenv("AUDIT_JOBS_PROCESS_ROOT")
	if mode == "" {
		t.Skip("subprocess helper only")
	}
	marker, err := os.ReadFile(filepath.Join(root, ".owned"))
	if err != nil || string(marker) != subprocessFixtureMarker || (mode != "interrupt" && mode != "recover" && mode != "cron") {
		t.Fatal("invalid owned subprocess fixture", err)
	}
	q := subprocessQueue(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	q.Register("subprocess_fixture", func(ctx context.Context, raw json.RawMessage) error {
		var p subprocessPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if _, err := q.db.ExecContext(ctx, `INSERT INTO audit_invocations VALUES(?,?,?)`, p.Key, mode, os.Getpid()); err != nil {
			return err
		}
		if mode == "interrupt" {
			if err := os.WriteFile(filepath.Join(root, "running.ready"), []byte(p.Key), 0600); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		}
		_, err := q.db.ExecContext(ctx, `INSERT INTO audit_effects VALUES(?,?,?)`, p.Key, os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
		return err
	}, 2)
	want := 2
	if mode == "cron" {
		want = 3
		var ticks atomic.Int64
		q.Cron("subprocess_fixture", 40*time.Millisecond, func(ctx context.Context) error {
			n := ticks.Add(1)
			if n > 3 {
				return nil
			}
			if _, err := q.db.ExecContext(ctx, `INSERT INTO audit_ticks VALUES(?,?)`, n, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return err
			}
			_, err := q.Enqueue(ctx, "subprocess_fixture", subprocessPayload{Key: fmt.Sprintf("cron-%d", n)}, Idempotency(fmt.Sprintf("cron-%d", n)), RunAt(time.Now().UTC()))
			return err
		})
	}
	q.Start(ctx, 1)
	defer q.Wait()
	defer cancel()
	if mode == "interrupt" {
		<-ctx.Done()
		t.Fatal("parent failed to interrupt the running OS process")
	}
	for {
		stats, err := q.Stats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Done == want && stats.Running == 0 && stats.Queued == 0 && stats.Dead == 0 && stats.Failed == 0 {
			time.Sleep(40 * time.Millisecond)
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("bounded helper did not complete all jobs", stats)
		case <-time.After(5 * time.Millisecond):
		}
	}
	t.Logf("helper mode=%s PID=%d done=%d", mode, os.Getpid(), want)
}
