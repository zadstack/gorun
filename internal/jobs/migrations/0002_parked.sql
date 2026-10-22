-- parked_reason marks a queued job whose handler is disabled or missing in
-- the running binary. Workers never claim a parked job and attempts are not
-- counted; Unpark returns the rows to the queue unchanged. Additive only:
-- older binaries ignore the column and keep claiming by status/run_at.
ALTER TABLE jobs ADD COLUMN parked_reason TEXT NOT NULL DEFAULT '';
CREATE INDEX jobs_parked ON jobs (kind) WHERE parked_reason <> '';
