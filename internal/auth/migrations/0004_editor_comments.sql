-- Editors moderate comments by default (the content module declares the permission;
-- role_permissions.permission has no foreign key, so the grant can precede it).
INSERT OR IGNORE INTO role_permissions (role, permission) VALUES ('editor', 'comments.moderate');
