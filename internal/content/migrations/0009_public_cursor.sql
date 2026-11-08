-- Keyset enumeration for public sitemap builds without repeated sorts/offsets.
CREATE INDEX content_public_cursor ON content(type, status, id);
