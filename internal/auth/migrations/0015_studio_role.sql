-- The Studio editor: a role for the client or content editor who changes
-- the texts and images of published builder pages where they stand (the
-- admin bar's "Edit in place") and publishes those changes, and nothing
-- else: no layout, sections, links, settings or identity. The builder
-- module declares content.edit_text and content.publish_text;
-- role_permissions.permission has no foreign key, so the grants can
-- precede it.
INSERT OR IGNORE INTO roles (slug, name_json, system) VALUES
    ('studio', '{"en":"Studio editor","ar":"محرر الاستوديو"}', 1);
INSERT OR IGNORE INTO role_permissions (role, permission) VALUES
    ('studio', 'content.view'), ('studio', 'content.edit_text'), ('studio', 'content.publish_text');
