-- A menu can be for one locale of the public site ('' = every locale); a location holds one menu
-- per locale.
ALTER TABLE menus ADD COLUMN locale TEXT NOT NULL DEFAULT '';
