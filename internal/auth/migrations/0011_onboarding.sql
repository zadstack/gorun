ALTER TABLE users ADD COLUMN onboarding_required INTEGER NOT NULL DEFAULT 0 CHECK (onboarding_required IN (0,1));
-- Recognize an unconfigured administrator provisioned by the previous start release.
UPDATE users SET onboarding_required = 1
WHERE login_name = 'admin' AND email = 'admin@localhost.invalid' AND role = 'admin';
