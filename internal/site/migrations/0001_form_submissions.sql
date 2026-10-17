-- Every accepted contact form submission, stored before any mail attempt
-- so nothing is lost when SMTP is unset or fails. delivered = 1 once the
-- mailer sent it.
CREATE TABLE form_submissions (
    id         TEXT PRIMARY KEY,
    form       TEXT NOT NULL,
    name       TEXT NOT NULL,
    email      TEXT NOT NULL,
    message    TEXT NOT NULL,
    page       TEXT NOT NULL DEFAULT '',
    ip         TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    delivered  INTEGER NOT NULL DEFAULT 0 CHECK (delivered IN (0, 1))
);
CREATE INDEX form_submissions_created ON form_submissions (created_at);
