-- Inbox state for stored submissions: status (new until a staff member opens
-- it; spam is set by hand), the optional attachment (file in
-- <data>/form-uploads/<id>), and why mailing failed or was skipped.
-- delivery: pending (not tried yet), sent, failed, off (forms.notify false).
ALTER TABLE form_submissions ADD COLUMN status TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'read', 'spam'));
ALTER TABLE form_submissions ADD COLUMN attachment_name TEXT NOT NULL DEFAULT '';
ALTER TABLE form_submissions ADD COLUMN attachment_type TEXT NOT NULL DEFAULT '';
ALTER TABLE form_submissions ADD COLUMN attachment_size INTEGER NOT NULL DEFAULT 0;
ALTER TABLE form_submissions ADD COLUMN delivery TEXT NOT NULL DEFAULT 'pending' CHECK (delivery IN ('pending', 'sent', 'failed', 'off'));
ALTER TABLE form_submissions ADD COLUMN delivery_error TEXT NOT NULL DEFAULT '';
UPDATE form_submissions SET delivery = 'sent', status = 'read' WHERE delivered = 1;
UPDATE form_submissions SET delivery = 'failed' WHERE delivered = 0;
CREATE INDEX form_submissions_status ON form_submissions (status, created_at);
