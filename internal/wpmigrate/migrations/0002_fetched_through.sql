-- fetched_through: index (in the step order) of the last source step whose
-- WordPress fetch completed in the current run; -1 before any. Retry and
-- Resume skip fetching only for steps at or below it.
ALTER TABLE migrations ADD COLUMN fetched_through INTEGER NOT NULL DEFAULT -1;
