-- Claimed ops stay 'running' for the remote call so a second worker cannot
-- re-claim a still-pending row by matching the bumped attempts count.
ALTER TABLE provisioning_ops RENAME COLUMN status TO previous_status;
ALTER TABLE provisioning_ops ADD COLUMN status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'running', 'done', 'failed'));
UPDATE provisioning_ops SET status = previous_status;
ALTER TABLE provisioning_ops DROP COLUMN previous_status;
