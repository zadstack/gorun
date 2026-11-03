-- Why the watchdog stopped a run (interrupted, not_started, agent_timeout); empty for other stops.
ALTER TABLE admin_canvas_runs ADD COLUMN stop_reason TEXT NOT NULL DEFAULT '';
