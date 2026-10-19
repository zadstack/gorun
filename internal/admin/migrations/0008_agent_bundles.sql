-- Bundle edits, including empty/removal edits, permanently retire old consent.
CREATE TRIGGER canvas_inbound_bundle_insert AFTER INSERT ON settings WHEN NEW.key LIKE 'agents.bundle.%' BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE key_id=substr(NEW.key,15);
 UPDATE admin_canvas_schedules SET enabled=0,status='needs_review' WHERE board_id IN(SELECT board_id FROM admin_canvas_grants WHERE key_id=substr(NEW.key,15));
END;
CREATE TRIGGER canvas_inbound_bundle_update AFTER UPDATE ON settings WHEN NEW.key LIKE 'agents.bundle.%' BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE key_id=substr(NEW.key,15);
 UPDATE admin_canvas_schedules SET enabled=0,status='needs_review' WHERE board_id IN(SELECT board_id FROM admin_canvas_grants WHERE key_id=substr(NEW.key,15));
END;
CREATE TRIGGER canvas_inbound_bundle_delete AFTER DELETE ON settings WHEN OLD.key LIKE 'agents.bundle.%' BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE key_id=substr(OLD.key,15);
 UPDATE admin_canvas_schedules SET enabled=0,status='needs_review' WHERE board_id IN(SELECT board_id FROM admin_canvas_grants WHERE key_id=substr(OLD.key,15));
END;
