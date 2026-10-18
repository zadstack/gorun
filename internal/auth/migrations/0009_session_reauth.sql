-- When the session's user last proved their credentials: the sign-in itself,
-- or a later password confirmation (/admin/reauth). Sensitive actions such as
-- downloading a backup require it to be recent. Sessions from before this
-- migration count as not recent.
ALTER TABLE sessions ADD COLUMN reauth_at TEXT NOT NULL DEFAULT '';
