ALTER TABLE users ADD COLUMN login_name TEXT COLLATE NOCASE;
CREATE UNIQUE INDEX users_login_name ON users(login_name) WHERE login_name IS NOT NULL;
