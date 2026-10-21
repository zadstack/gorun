package update

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

// phaseOutcome is what a phase executor reports.
type phaseOutcome struct {
	ok          bool
	reason      string
	remediation string
	retryable   bool
	data        map[string]any
	// wait means the phase is still in progress when the process must stop
	// (shutdown or lease lost); the job is requeued and resumes later.
	wait bool
}

func pass(data map[string]any) phaseOutcome { return phaseOutcome{ok: true, data: data} }

func fail(reason, remediation string, retryable bool, data map[string]any) phaseOutcome {
	return phaseOutcome{reason: reason, remediation: remediation, retryable: retryable, data: data}
}

// run is the job handler. It is resumable: passed phases are skipped, the
// running one continues. Ownership is the durable lease; a second runner
// for the same operation (a recovered job beside a reconcile continuation)
// finds the lease held and exits without touching the rows.
func (s *Service) run(ctx context.Context, raw json.RawMessage) error {
	var p runPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.OperationID == "" || p.Token == "" {
		return errors.New("update: invalid run payload")
	}
	op, err := s.Get(ctx, p.OperationID)
	if err != nil {
		return err
	}
	if op.Terminal() {
		return nil
	}
	if err := s.lease.Acquire(ctx, HolderUpdate, p.Token, op.ID); err != nil {
		var held *ErrLeaseHeld
		if errors.As(err, &held) {
			s.log.Warn("update: another runner owns the operation", "operation", op.ID, "holder", held.Holder)
			return nil
		}
		return err
	}
	wctx := context.WithoutCancel(ctx)
	now := db.FormatTime(s.now())
	if _, err := s.db.ExecContext(wctx, `UPDATE update_operations SET status = 'running', run_token = ?, heartbeat_at = ?, started_at = CASE WHEN started_at = '' THEN ? ELSE started_at END,
		updated_at = ? WHERE id = ?`, p.Token, now, now, now, op.ID); err != nil {
		return err
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := make(chan struct{})
	beat := make(chan struct{})
	go func() {
		defer close(beat)
		s.heartbeat(rctx, op.ID, p.Token, cancel, stop)
	}()
	err = s.drive(rctx, op.ID, p.Token)
	close(stop)
	<-beat
	return err
}

// heartbeat renews the lease and records progress until stop closes; a
// lost lease cancels the run so a fenced-out runner stops at once.
func (s *Service) heartbeat(ctx context.Context, id, token string, cancel context.CancelFunc, stop <-chan struct{}) {
	t := time.NewTicker(s.cfg.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.lease.Renew(ctx, token); err != nil {
				if errors.Is(err, ErrLeaseLost) {
					s.log.Error("update: lease lost, stopping runner", "operation", id)
					cancel()
					return
				}
				continue
			}
			_, _ = s.db.ExecContext(ctx, `UPDATE update_operations SET heartbeat_at = ?, updated_at = ? WHERE id = ? AND run_token = ?`, db.FormatTime(s.now()), db.FormatTime(s.now()), id, token)
		}
	}
}

// drive executes the remaining phases.
func (s *Service) drive(ctx context.Context, id, token string) error {
	for {
		op, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		if op.Terminal() {
			return nil
		}
		name, ok := nextPhase(op)
		if !ok {
			return s.finish(context.WithoutCancel(ctx), op, token, StatusFailed, "lifecycle has no runnable phase", "Review the operation phases and start a new update.", false)
		}
		outcome := s.execute(ctx, &op, name, token)
		wctx := context.WithoutCancel(ctx)
		if outcome.wait {
			// Keep the phase running; the queue requeues this job on
			// shutdown and Reconcile resumes it after a restart.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("update: runner stopped")
		}
		if err := s.recordPhase(wctx, id, name, outcome); err != nil {
			return err
		}
		if outcome.ok {
			if err := s.afterPhase(wctx, &op, name, token); err != nil {
				return err
			}
			if op.Terminal() {
				return nil
			}
			continue
		}
		return s.handleFailure(ctx, op, name, token, outcome)
	}
}

func nextPhase(op Operation) (string, bool) {
	hasRollback := slices.ContainsFunc(op.Phases, func(p Phase) bool { return strings.HasPrefix(p.Name, "rollback_") })
	for _, p := range op.Phases {
		switch p.Status {
		case PhasePending, PhaseRunning:
			return p.Name, true
		case PhaseFailed, PhaseInterrupted:
			if hasRollback && !strings.HasPrefix(p.Name, "rollback_") {
				continue // the failed health phase handed over to rollback
			}
			return "", false
		}
	}
	return "", false
}

func (s *Service) execute(ctx context.Context, op *Operation, name, token string) phaseOutcome {
	now := db.FormatTime(s.now())
	if _, err := s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE update_phases SET status = 'running', started_at = CASE WHEN started_at = '' THEN ? ELSE started_at END,
		attempts = attempts + 1, updated_at = ? WHERE operation_id = ? AND name = ?`, now, now, op.ID, name); err != nil {
		return fail("phase could not be recorded", "Check the database and retry.", true, nil)
	}
	_, _ = s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE update_operations SET current_phase = ?, updated_at = ? WHERE id = ?`, name, now, op.ID)
	switch name {
	case PhaseCompatibility:
		return s.phaseCompatibility(ctx, op)
	case PhaseVerification:
		return s.phaseVerification(ctx, op)
	case PhaseBackup:
		return s.phaseBackup(ctx, op)
	case PhaseActivation:
		return s.phaseActivation(ctx, op, name, op.Artifact, op.RequestedVersion, "deployment_ref")
	case PhaseHealth:
		return s.phaseHealth(ctx, op, op.RequestedVersion)
	case PhaseAcceptance:
		return pass(map[string]any{"accepted_version": op.RequestedVersion, "accepted_at": now})
	case PhaseRollbackCompatibility:
		return s.phaseRollbackCompatibility(ctx, op)
	case PhaseRollbackActivation:
		return s.phaseActivation(ctx, op, name, op.Previous, op.Previous.Version, "rollback_ref")
	case PhaseRollbackHealth:
		return s.phaseHealth(ctx, op, op.Previous.Version)
	}
	return fail("unknown phase "+name, "", false, nil)
}

func (s *Service) recordPhase(ctx context.Context, id, name string, o phaseOutcome) error {
	status := PhasePassed
	if !o.ok {
		status = PhaseFailed
	}
	data, _ := json.Marshal(o.data)
	if o.data == nil {
		data = []byte("null")
	}
	now := s.now()
	var started string
	_ = s.db.QueryRowContext(ctx, `SELECT started_at FROM update_phases WHERE operation_id = ? AND name = ?`, id, name).Scan(&started)
	var duration any
	if t, err := db.ParseTime(started); err == nil {
		duration = now.Sub(t).Milliseconds()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE update_phases SET status = ?, finished_at = ?, duration_ms = ?, progress = NULL, result_json = ?, error = ?, remediation = ?,
		retryable = ?, updated_at = ? WHERE operation_id = ? AND name = ?`, status, db.FormatTime(now), duration, string(data), o.reason, o.remediation, boolInt(o.retryable), db.FormatTime(now), id, name)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// afterPhase persists the durable facts a passed phase established.
func (s *Service) afterPhase(ctx context.Context, op *Operation, name, token string) error {
	now := db.FormatTime(s.now())
	switch name {
	case PhaseActivation:
		_, err := s.db.ExecContext(ctx, `UPDATE update_operations SET activated_version = ?, updated_at = ? WHERE id = ?`, op.RequestedVersion, now, op.ID)
		return err
	case PhaseRollbackActivation:
		_, err := s.db.ExecContext(ctx, `UPDATE update_operations SET activated_version = ?, updated_at = ? WHERE id = ?`, op.Previous.Version, now, op.ID)
		return err
	case PhaseAcceptance:
		if _, err := s.db.ExecContext(ctx, `UPDATE update_operations SET accepted_version = ?, updated_at = ? WHERE id = ?`, op.RequestedVersion, now, op.ID); err != nil {
			return err
		}
		return s.finish(ctx, *op, token, StatusSucceeded, "", "", false)
	case PhaseRollbackHealth:
		return s.finish(ctx, *op, token, StatusRolledBack, op.Error, "The prior artifact serves the current data. Review the failed update phases before trying again.", true)
	}
	return nil
}

// finish sets the terminal status and releases the lease.
func (s *Service) finish(ctx context.Context, op Operation, token, status, reason, remediation string, retryable bool) error {
	now := db.FormatTime(s.now())
	if _, err := s.db.ExecContext(ctx, `UPDATE update_operations SET status = ?, error = ?, remediation = ?, retryable = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
		status, reason, remediation, boolInt(retryable), now, now, op.ID); err != nil {
		return err
	}
	s.log.Info("update: operation finished", "operation", op.ID, "status", status, "reason", reason)
	return s.lease.Release(ctx, token)
}

// handleFailure decides between a plain failure and a runtime rollback.
// Only a health failure after activation triggers rollback, and only when
// a prior artifact is known. Data is never restored from a snapshot.
func (s *Service) handleFailure(runCtx context.Context, op Operation, name, token string, o phaseOutcome) error {
	// Writes must land even during shutdown; only the continued rollback
	// drive keeps the cancellable context so it can stop and resume later.
	ctx := context.WithoutCancel(runCtx)
	rollbackPhase := strings.HasPrefix(name, "rollback_")
	switch {
	case rollbackPhase:
		return s.finish(ctx, op, token, StatusRollbackFailed, o.reason, nonEmpty(o.remediation, "Activate a compatible prior artifact manually on the current data, or repair forward. Do not restore an old backup over newer writes."), false)
	case name == PhaseHealth && op.Previous != nil:
		now := db.FormatTime(s.now())
		for i, phase := range rollbackPlan {
			if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO update_phases (operation_id, name, seq, updated_at) VALUES (?, ?, ?, ?)`, op.ID, phase, len(updatePlan)+i, now); err != nil {
				return err
			}
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE update_operations SET error = ?, remediation = ?, updated_at = ? WHERE id = ?`, o.reason, o.remediation, now, op.ID); err != nil {
			return err
		}
		// Mark the remaining update phases as skipped: the lifecycle moves to rollback.
		_, _ = s.db.ExecContext(ctx, `UPDATE update_phases SET status = 'skipped', updated_at = ? WHERE operation_id = ? AND status = 'pending' AND name NOT LIKE 'rollback_%'`, now, op.ID)
		return s.drive(runCtx, op.ID, token)
	case name == PhaseHealth:
		return s.finish(ctx, op, token, StatusFailed, o.reason, nonEmpty(o.remediation, "")+" No prior artifact is known for automatic rollback: activate the previous compatible release manually on the current data; do not restore an old backup.", o.retryable)
	}
	return s.finish(ctx, op, token, StatusFailed, o.reason, o.remediation, o.retryable)
}

func nonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// --- phases -----------------------------------------------------------------

func (s *Service) phaseCompatibility(ctx context.Context, op *Operation) phaseOutcome {
	data := map[string]any{"running_version": op.RunningVersion, "target_version": op.RequestedVersion, "downgrade": false}
	target, _ := parseVersion(op.RequestedVersion)
	running, ok := parseVersion(op.RunningVersion)
	if !ok {
		data["comparison"] = "unknown"
		return fail("running build is not a release version; compatibility cannot be compared", "Run a released build before using staged updates, or upgrade manually following docs/UPGRADE.md.", false, data)
	}
	switch c := target.compare(running); {
	case c == 0:
		data["comparison"] = "same"
		return fail("target equals the running version", "Choose a different release.", false, data)
	case c < 0:
		data["comparison"], data["downgrade"] = "downgrade", true
		if !s.cfg.AllowDowngrade {
			return fail("downgrade requires review", "Downgrades can leave the data ahead of the binary. Set GORUN_UPDATE_ALLOW_DOWNGRADE after confirming schema, feature and security compatibility, or repair forward.", false, data)
		}
	default:
		data["comparison"] = "upgrade"
	}
	if s.cfg.Schema != nil {
		if applied, err := s.cfg.Schema(ctx); err == nil {
			data["migrations_before"] = applied
		} else {
			data["migrations_before"] = nil
		}
	}
	if s.cfg.Compatibility == nil {
		data["evidence"] = "unavailable"
		return fail("compatibility evidence unavailable", "The host did not provide schema, module and theme evidence.", false, data)
	}
	rep, err := s.cfg.Compatibility(ctx, op.RequestedVersion)
	if err != nil {
		return fail("compatibility check failed: "+short(err.Error()), "Fix the reported problem and start a new update.", true, data)
	}
	for k, v := range rep.Data {
		data[k] = v
	}
	if !rep.OK {
		return fail(rep.Reason, rep.Remediation, rep.Retryable, data)
	}
	return pass(data)
}

func (s *Service) phaseVerification(ctx context.Context, op *Operation) phaseOutcome {
	a, err := s.cfg.Source.Resolve(ctx, op.RequestedVersion)
	if err != nil {
		return fail("release could not be resolved: "+short(err.Error()), "Check the trusted release source and the requested version.", true, nil)
	}
	data := map[string]any{"artifact": a.ID, "digest": a.Digest, "source": a.Source}
	if a.ID == "" {
		return fail("release source returned no artifact identity", "Publish an immutable artifact id in the release source.", false, data)
	}
	if a.Digest == "" && s.cfg.RequireDigest {
		return fail("release source publishes no digest", "Publish a sha256 digest for the artifact or disable digest enforcement for this source.", false, data)
	}
	if op.ExpectedDigest != "" && a.Digest != op.ExpectedDigest {
		data["expected_digest"] = op.ExpectedDigest
		return fail("artifact digest does not match the expected digest", "Confirm the release source and the digest you expect; do not activate an unverified artifact.", false, data)
	}
	raw, _ := json.Marshal(a)
	previous := "null"
	if _, ok := parseVersion(op.RunningVersion); ok {
		if prev, err := s.cfg.Source.Resolve(ctx, op.RunningVersion); err == nil && prev.ID != "" {
			b, _ := json.Marshal(prev)
			previous = string(b)
			data["previous_artifact"] = prev.ID
		}
	}
	if _, err := s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE update_operations SET artifact_json = ?, previous_json = ?, updated_at = ? WHERE id = ?`, string(raw), previous, db.FormatTime(s.now()), op.ID); err != nil {
		return fail("artifact could not be recorded", "Check the database and retry.", true, data)
	}
	op.Artifact = &a
	if previous != "null" {
		var prev Artifact
		_ = json.Unmarshal([]byte(previous), &prev)
		op.Previous = &prev
	}
	return pass(data)
}

func (s *Service) phaseBackup(ctx context.Context, op *Operation) phaseOutcome {
	if s.cfg.Backup == nil {
		return fail("backup is unavailable", "The host did not provide backup machinery; activation is blocked without a verified backup.", false, nil)
	}
	rep, err := s.cfg.Backup(ctx)
	if err != nil {
		return fail("backup failed: "+short(err.Error()), "Fix the backup problem (disk space, permissions) and start a new update. No deployment was requested.", true, nil)
	}
	if !rep.OK {
		return fail(rep.Reason, nonEmpty(rep.Remediation, "Resolve the backup verification failure before updating."), rep.Retryable, rep.Data)
	}
	stamp, _ := rep.Data["stamp"].(string)
	if stamp == "" {
		return fail("backup returned no stamp", "The backup machinery must return the manifest stamp.", false, rep.Data)
	}
	if _, err := s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE update_operations SET backup_stamp = ?, updated_at = ? WHERE id = ?`, stamp, db.FormatTime(s.now()), op.ID); err != nil {
		return fail("backup stamp could not be recorded", "Check the database and retry.", true, rep.Data)
	}
	op.BackupStamp = stamp
	return pass(rep.Data)
}

// phaseActivation requests activation once (the reference is stored before
// polling so a restart never dispatches twice), then waits for the serving
// identity to become expected. A provider's "active" alone is insufficient.
func (s *Service) phaseActivation(ctx context.Context, op *Operation, phase string, artifact *Artifact, expected, column string) phaseOutcome {
	if artifact == nil {
		return fail("no artifact to activate", "Verification did not record an artifact.", false, nil)
	}
	wctx := context.WithoutCancel(ctx)
	ref := op.DeploymentRef
	if column == "rollback_ref" {
		ref = op.RollbackRef
	}
	data := map[string]any{"artifact": artifact.ID, "expected_version": expected, "adapter": s.cfg.Adapter.Name()}
	if ref == "" {
		var err error
		ref, err = s.cfg.Adapter.Activate(ctx, *artifact)
		if err != nil {
			return fail("activation request failed: "+short(err.Error()), "Check the deployment integration, then start a new update. Nothing was activated.", true, data)
		}
		if _, err := s.db.ExecContext(wctx, `UPDATE update_operations SET `+column+` = ?, updated_at = ? WHERE id = ?`, ref, db.FormatTime(s.now()), op.ID); err != nil {
			return fail("deployment reference could not be recorded", "Check the deployment provider for a dispatched deployment before retrying.", false, data)
		}
		if column == "rollback_ref" {
			op.RollbackRef = ref
		} else {
			op.DeploymentRef = ref
		}
	}
	data["deployment_ref"] = ref
	var started string
	_ = s.db.QueryRowContext(wctx, `SELECT started_at FROM update_phases WHERE operation_id = ? AND name = ?`, op.ID, phase).Scan(&started)
	deadline := s.now().Add(s.cfg.ActivationTimeout)
	if t, err := db.ParseTime(started); err == nil {
		deadline = t.Add(s.cfg.ActivationTimeout)
	}
	for {
		if normalizeVersion(s.cfg.RunningVersion) == normalizeVersion(expected) {
			data["evidence"] = "serving_identity"
			return pass(data)
		}
		act, err := s.cfg.Adapter.Status(ctx, ref)
		if err != nil {
			act = Activation{Status: ActivationUnknown, Detail: short(err.Error())}
		}
		data["provider_status"], data["provider_detail"] = act.Status, act.Detail
		if act.Status == ActivationFailed {
			return fail("deployment provider reports the activation failed", "Inspect the provider's deployment log. The running process is unchanged; start a new update when the cause is fixed.", true, data)
		}
		now := s.now()
		if !now.Before(deadline) {
			if act.Status == ActivationActive {
				return fail("provider reports active but the serving identity did not change", "Confirm which artifact the provider started; the serving process still reports "+s.cfg.RunningVersion+".", false, data)
			}
			return fail("activation timed out after upstream acceptance", "Check the provider or activate the artifact manually, then start a new update; the request will reconcile the accepted deployment first.", true, data)
		}
		elapsed := float64(s.cfg.ActivationTimeout-deadline.Sub(now)) / float64(s.cfg.ActivationTimeout)
		_, _ = s.db.ExecContext(wctx, `UPDATE update_phases SET progress = ?, updated_at = ? WHERE operation_id = ? AND name = ?`, elapsed, db.FormatTime(now), op.ID, phase)
		select {
		case <-ctx.Done():
			return phaseOutcome{wait: true}
		case <-time.After(s.cfg.PollInterval):
		}
	}
}

func (s *Service) phaseHealth(ctx context.Context, op *Operation, expected string) phaseOutcome {
	data := map[string]any{"expected_version": expected, "running_version": s.cfg.RunningVersion}
	if normalizeVersion(s.cfg.RunningVersion) != normalizeVersion(expected) {
		return fail("serving identity mismatch", "The process reporting health is not the expected release.", false, data)
	}
	state := MigrationUnknown
	if s.cfg.Schema != nil {
		if after, err := s.cfg.Schema(ctx); err == nil {
			data["migrations_after"] = after
			if before := phaseData(op, PhaseCompatibility, "migrations_before"); before != nil {
				state = MigrationUnchanged
				if len(diff(after, before)) > 0 {
					state = MigrationApplied
					data["migrations_applied"] = diff(after, before)
				}
			}
		}
	}
	if !strings.HasPrefix(op.CurrentPhase, "rollback_") {
		_, _ = s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE update_operations SET migration_state = ?, updated_at = ? WHERE id = ?`, state, db.FormatTime(s.now()), op.ID)
		op.MigrationState = state
	}
	data["migration_state"] = state
	if s.cfg.Health == nil {
		return fail("health evidence unavailable", "The host did not provide readiness and protected checks.", false, data)
	}
	rep, err := s.cfg.Health(ctx)
	if err != nil {
		return fail("health check failed: "+short(err.Error()), "Inspect System Health on the new process.", true, data)
	}
	for k, v := range rep.Data {
		data[k] = v
	}
	if !rep.OK {
		return fail(rep.Reason, rep.Remediation, rep.Retryable, data)
	}
	links := []map[string]any{}
	for _, u := range s.cfg.HealthLinks {
		entry := map[string]any{"url": u, "ok": false}
		if s.cfg.Link == nil {
			entry["error"] = "link checker unavailable"
		} else if err := s.cfg.Link(ctx, u); err != nil {
			entry["error"] = short(err.Error())
		} else {
			entry["ok"] = true
		}
		links = append(links, entry)
	}
	data["links"] = links
	for _, l := range links {
		if ok, _ := l["ok"].(bool); !ok {
			return fail("a configured public or media link failed", "Fix the link or the new process before accepting the update.", true, data)
		}
	}
	return pass(data)
}

// phaseRollbackCompatibility only allows a prior artifact on the current
// data when activation applied no migration the prior artifact does not
// know; otherwise it stops and keeps the current data for forward repair.
func (s *Service) phaseRollbackCompatibility(ctx context.Context, op *Operation) phaseOutcome {
	data := map[string]any{"previous_version": op.Previous.Version, "migration_state": op.MigrationState}
	switch op.MigrationState {
	case MigrationUnchanged:
		return pass(data)
	case MigrationApplied:
		return fail("activation applied migrations the prior artifact may not support", "Compatibility is uncertain: keep the current data, repair forward or recover into a separate directory and reconcile explicitly. No snapshot was restored.", false, data)
	}
	return fail("migration state unknown; rollback compatibility cannot be proven", "Inspect schema_migrations and activate a compatible artifact manually on the current data.", false, data)
}

func phaseData(op *Operation, phase, key string) []string {
	for _, p := range op.Phases {
		if p.Name != phase || len(p.Result) == 0 {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(p.Result, &m) != nil {
			return nil
		}
		raw, ok := m[key]
		if !ok || string(raw) == "null" {
			return nil
		}
		var list []string
		if json.Unmarshal(raw, &list) != nil {
			return nil
		}
		if list == nil {
			list = []string{}
		}
		return list
	}
	return nil
}

func diff(after, before []string) []string {
	var out []string
	for _, a := range after {
		if !slices.Contains(before, a) {
			out = append(out, a)
		}
	}
	return out
}

func short(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// --- reconciliation ------------------------------------------------------------

// Reconcile repairs operations left pending or running by a stopped
// process. atBoot says every previous runner is dead (one process per data
// directory); otherwise only operations whose heartbeat is older than the
// lease TTL are touched. An activation whose expected identity now serves
// passes on that evidence and the lifecycle continues in this process; an
// activation still inside its timeout is resumed; anything else is marked
// interrupted, never left looking successful or in progress.
func (s *Service) Reconcile(ctx context.Context, atBoot bool) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM update_operations WHERE status IN ('pending', 'running') ORDER BY created_at`)
	if err != nil {
		return fmt.Errorf("update: reconcile: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := s.reconcileOne(ctx, id, atBoot); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileOne(ctx context.Context, id string, atBoot bool) error {
	op, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	now := s.now()
	if op.Status == StatusPending {
		if _, inFlight, _ := s.queue.InFlight(ctx, "update:"+id+":start"); inFlight {
			return nil // queued, not started yet; the workers will run it
		}
	}
	if !atBoot {
		hb, err := db.ParseTime(ptr(op.HeartbeatAt))
		if err == nil && now.Sub(hb) < s.cfg.LeaseTTL {
			return nil // alive runner
		}
	}
	name, ok := nextPhase(op)
	if !ok {
		return s.interrupt(ctx, op, "", "lifecycle had no runnable phase")
	}
	expected, isActivation := "", false
	switch name {
	case PhaseActivation:
		expected, isActivation = op.RequestedVersion, op.DeploymentRef != ""
	case PhaseRollbackActivation:
		if op.Previous != nil {
			expected, isActivation = op.Previous.Version, op.RollbackRef != ""
		}
	}
	nowS := db.FormatTime(now)
	if isActivation && normalizeVersion(s.cfg.RunningVersion) == normalizeVersion(expected) {
		data, _ := json.Marshal(map[string]any{"evidence": "serving_identity_after_restart", "expected_version": expected, "running_version": s.cfg.RunningVersion})
		if _, err := s.db.ExecContext(ctx, `UPDATE update_phases SET status = 'passed', finished_at = ?, progress = NULL, result_json = ?, error = '', updated_at = ?
			WHERE operation_id = ? AND name = ?`, nowS, string(data), nowS, id, name); err != nil {
			return err
		}
		if err := s.afterPhase(ctx, &op, name, ""); err != nil {
			return err
		}
		return s.resume(ctx, op, name)
	}
	if isActivation {
		var started string
		_ = s.db.QueryRowContext(ctx, `SELECT started_at FROM update_phases WHERE operation_id = ? AND name = ?`, id, name).Scan(&started)
		if t, err := db.ParseTime(started); err == nil && now.Before(t.Add(s.cfg.ActivationTimeout)) {
			return s.resume(ctx, op, name)
		}
	}
	return s.interrupt(ctx, op, name, "process restarted while the operation was running")
}

func ptr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// resume takes over the lease with a new token and queues a continuation.
func (s *Service) resume(ctx context.Context, op Operation, phase string) error {
	token := newToken()
	nowS := db.FormatTime(s.now())
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM ops_leases WHERE scope = ? AND operation_id = ?`, LeaseScope, op.ID); err != nil {
			return err
		}
		if err := s.lease.acquireTx(ctx, tx, HolderUpdate, token, op.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE update_operations SET run_token = ?, heartbeat_at = ?, updated_at = ? WHERE id = ?`, token, nowS, nowS, op.ID); err != nil {
			return err
		}
		_, err := s.queue.EnqueueTx(ctx, tx, JobKind, runPayload{OperationID: op.ID, Token: token}, jobs.Idempotency("update:"+op.ID+":resume:"+phase+":"+token))
		return err
	})
	if err != nil {
		return fmt.Errorf("update: resume %s: %w", op.ID, err)
	}
	s.log.Warn("update: resuming operation after restart", "operation", op.ID, "phase", phase)
	return nil
}

func (s *Service) interrupt(ctx context.Context, op Operation, phase, reason string) error {
	nowS := db.FormatTime(s.now())
	if phase != "" {
		if _, err := s.db.ExecContext(ctx, `UPDATE update_phases SET status = 'interrupted', finished_at = ?, progress = NULL, error = ?, updated_at = ?
			WHERE operation_id = ? AND name = ? AND status IN ('pending', 'running')`, nowS, reason, nowS, op.ID, phase); err != nil {
			return err
		}
	}
	remediation := "Check the deployment provider and System Health, then start a new update; the request reconciles any accepted deployment first. Nothing was restored."
	if _, err := s.db.ExecContext(ctx, `UPDATE update_operations SET status = 'interrupted', error = ?, remediation = ?, retryable = 1, finished_at = ?, updated_at = ? WHERE id = ? AND status IN ('pending', 'running')`,
		reason, remediation, nowS, nowS, op.ID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM ops_leases WHERE scope = ? AND operation_id = ?`, LeaseScope, op.ID)
	s.log.Warn("update: operation interrupted", "operation", op.ID, "reason", reason)
	return err
}
